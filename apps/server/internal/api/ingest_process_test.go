package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/clamav"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// sendReport creates a report, uploads its files and completes it, as the
// app does; it returns the report id.
func (e *env) sendReport(a ingestApp, app *client, body map[string]any, files ...reportFile) uuid.UUID {
	e.t.Helper()
	return e.submitReport(a, app, body, files...).ReportId
}

// submitReport is sendReport that also returns the report's token.
func (e *env) submitReport(a ingestApp, app *client, body map[string]any, files ...reportFile) oapi.IngestReportCreated {
	e.t.Helper()
	rec := app.do(http.MethodPost, "/api/v1/ingest/reports", body, bearer(a.key))
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create report: %d %s", rec.Code, rec.Body)
	}
	created := decode[oapi.IngestReportCreated](e.t, rec)
	for i, u := range created.Uploads {
		if code := e.upload(u, files[i].body); code != http.StatusOK {
			e.t.Fatalf("upload %s: %d", u.Name, code)
		}
	}
	rec = app.do(http.MethodPost, "/api/v1/ingest/reports/"+created.ReportId.String()+"/complete", nil,
		bearer(a.key), header("X-Report-Token", created.ReportToken))
	if rec.Code != http.StatusAccepted {
		e.t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	return created
}

// workspaceOf returns the workspace of the app channel.
func (e *env) workspaceOf(a ingestApp) uuid.UUID {
	e.t.Helper()
	var ws uuid.UUID
	e.ownerScan(&ws, "SELECT workspace_id FROM channels WHERE id = $1", a.channel.Id)
	return ws
}

// process runs the job ingest.process as the worker would.
func (e *env) process(svc *ingest.Service, ws, report uuid.UUID, last bool) error {
	e.t.Helper()
	return svc.Process(context.Background(), ingest.ProcessArgs{WorkspaceID: ws, ReportID: report}, last)
}

func TestIngest_ProcessReport(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	ws := e.workspaceOf(a)
	app := e.appClient("198.51.100.1")
	// Node and Electron give a .log file no type: it is taken as text/plain.
	logFile := reportFile{"app.log", "application/octet-stream", []byte("2026-10-05 12:00:01 ERROR sync failed\n")}
	shot := reportFile{"screen.png", "image/png", []byte("\x89PNG\r\n\x1a\nnot really")}
	// Declared a screenshot, really a page: rejected by its bytes.
	page := reportFile{"fake.png", "image/png", []byte("<html><script>alert(1)</script></html>")}

	body := report("Не синхронизируется корзина", logFile, shot, page)
	body["contact"] = map[string]any{
		"external_id": "user-42", "email": "anna@example.test", "name": "Анна", "identity_token": identityToken(a.identity, "user-42"),
	}
	first := e.sendReport(a, app, body, logFile, shot, page)
	// complete queued exactly one job, even when repeated.
	if n := e.ownerCount(`SELECT count(*) FROM river.river_job WHERE kind = 'ingest.process'
		AND args->>'report_id' = $1 AND args->>'workspace_id' = $2`, first.String(), ws.String()); n != 1 {
		t.Fatalf("jobs: %d", n)
	}

	for range 2 { // a repeated job changes nothing
		if err := e.process(e.ingest, ws, first, false); err != nil {
			t.Fatalf("process: %v", err)
		}
	}
	if n := e.ownerCount(`SELECT count(*) FROM conversations WHERE workspace_id = $1`, ws); n != 1 {
		t.Fatalf("conversations: %d", n)
	}
	if n := e.ownerCount(`SELECT count(*) FROM app_reports r
		JOIN conversations c ON c.id = r.conversation_id AND c.channel_id = $2 AND c.status = 'queued' AND c.external_thread_id = r.id::text
		JOIN contacts ct ON ct.id = c.contact_id AND ct.external_id = 'user-42' AND ct.email = 'anna@example.test' AND ct.name = 'Анна'
		WHERE r.id = $1 AND r.status = 'processed' AND r.files_expire_at BETWEEN r.processed_at + interval '30 days' - interval '1 minute'
		  AND r.processed_at + interval '30 days' + interval '1 minute'`, first, a.channel.Id); n != 1 {
		t.Fatal("report is not a conversation of the verified contact")
	}
	if n := e.ownerCount(`SELECT count(*) FROM messages m JOIN app_reports r ON r.conversation_id = m.conversation_id
		WHERE r.id = $1 AND m.direction = 'in' AND m.sender_type = 'contact' AND m.kind = 'text' AND m.body = 'Не синхронизируется корзина'
		  AND m.attachments_count = 3 AND m.meta->'report'->>'contact_verified' = 'true' AND m.meta->'report'->'app'->>'version' = '2.4.1'
		  AND m.meta->'report'->'files' = '["ok", "ok", "type_mismatch"]'::jsonb`, first); n != 1 {
		t.Fatal("report message")
	}
	if n := e.ownerCount(`SELECT count(*) FROM attachments t JOIN app_reports r ON r.conversation_id = t.conversation_id
		WHERE r.id = $1 AND (t.file_name, t.status) IN (('app.log', 'ready'), ('screen.png', 'ready'), ('fake.png', 'rejected'))`, first); n != 3 {
		t.Fatalf("attachments: %d", n)
	}
	if n := e.ownerCount(`SELECT count(*) FROM outbox_events WHERE workspace_id = $1
		AND topic IN ('conversation.created', 'contact.created', 'message.created')`, ws); n != 3 {
		t.Fatalf("events: %d", n)
	}
	// The rejected file is gone from storage, the others stay.
	var keys []string
	for _, name := range []string{"fake.png", "app.log"} {
		var key string
		e.ownerScan(&key, "SELECT storage_key FROM app_report_files WHERE report_id = $1 AND file_name = $2", first, name)
		keys = append(keys, key)
	}
	if _, err := e.files.Stat(context.Background(), keys[0]); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("rejected file still stored: %v", err)
	}
	if _, err := e.files.Stat(context.Background(), keys[1]); err != nil {
		t.Fatalf("log file: %v", err)
	}
	// The operator downloads the log through the usual attachment link.
	var attID uuid.UUID
	e.ownerScan(&attID, "SELECT id FROM attachments WHERE storage_key = $1", keys[1])
	if rec := a.owner.do(http.MethodGet, "/api/v1/attachments/"+attID.String()+"/download-link", nil); rec.Code != http.StatusOK {
		t.Fatalf("download link: %d %s", rec.Code, rec.Body)
	}
	// The operator sees what the app said and why a file is missing, in the
	// order of the attachments.
	var convID uuid.UUID
	e.ownerScan(&convID, "SELECT conversation_id FROM app_reports WHERE id = $1", first)
	rec := a.owner.do(http.MethodGet, "/api/v1/conversations/"+convID.String()+"/messages", nil)
	feed := decode[oapi.MessagePage](t, rec)
	if len(feed.Items) != 1 || feed.Items[0].Report == nil {
		t.Fatalf("feed: %s", rec.Body)
	}
	m := feed.Items[0]
	rep := m.Report
	if rep.ReportId != first || !rep.ContactVerified || deref(rep.App.Version) != "2.4.1" || deref(rep.App.Os) != "Windows" ||
		deref(rep.App.OsVersion) != "11 23H2" || rep.App.Extra == nil || (*rep.App.Extra)["build"] != "1187" || rep.App.Locale != nil {
		t.Fatalf("report: %s", rec.Body)
	}
	got := []string{}
	for i, at := range m.Attachments {
		got = append(got, fmt.Sprintf("%s %s %s %s", at.FileName, at.ContentType, at.Status, rep.Files[i]))
	}
	if want := []string{"app.log text/plain ready ok", "screen.png image/png ready ok", "fake.png image/png rejected type_mismatch"}; !slices.Equal(got, want) {
		t.Fatalf("attachments: %q", got)
	}

	// A second verified report goes to the same contact; an unverified one
	// with the same external id gets a new contact without it.
	second := e.sendReport(a, app, body, logFile, shot, page)
	delete(body["contact"].(map[string]any), "identity_token")
	forged := e.sendReport(a, app, body, logFile, shot, page)
	for _, r := range []uuid.UUID{second, forged} {
		if err := e.process(e.ingest, ws, r, false); err != nil {
			t.Fatalf("process: %v", err)
		}
	}
	if n := e.ownerCount(`SELECT count(DISTINCT c.contact_id) FROM conversations c JOIN app_reports r ON r.conversation_id = c.id
		WHERE r.id IN ($1, $2)`, first, second); n != 1 {
		t.Fatal("verified reports of one user went to different contacts")
	}
	if n := e.ownerCount(`SELECT count(*) FROM conversations c JOIN app_reports r ON r.conversation_id = c.id
		JOIN contacts ct ON ct.id = c.contact_id
		WHERE r.id = $1 AND ct.external_id IS NULL AND ct.name = 'Анна'
		  AND ct.id NOT IN (SELECT contact_id FROM conversations c2 JOIN app_reports r2 ON r2.conversation_id = c2.id WHERE r2.id = $2)`,
		forged, first); n != 1 {
		t.Fatal("unverified report merged into the verified contact")
	}
	if n := e.ownerCount(`SELECT count(*) FROM messages m JOIN app_reports r ON r.conversation_id = m.conversation_id
		WHERE r.id = $1 AND m.meta->'report'->>'contact_verified' = 'false'`, forged); n != 1 {
		t.Fatal("unverified report is not marked")
	}
	// The customer card says whose identity is confirmed (W-04).
	for r, want := range map[uuid.UUID]oapi.ContactIdentity{first: "verified", forged: "unverified"} {
		var contactID uuid.UUID
		e.ownerScan(&contactID, "SELECT c.contact_id FROM conversations c JOIN app_reports r ON r.conversation_id = c.id WHERE r.id = $1", r)
		rec := a.owner.do(http.MethodGet, "/api/v1/contacts/"+contactID.String(), nil)
		if got := decode[oapi.Contact](t, rec); got.Identity != want {
			t.Fatalf("contact identity: %s, want %s", got.Identity, want)
		}
	}
	if rec := a.owner.do(http.MethodGet, "/api/v1/conversations?status=queued", nil); rec.Code != http.StatusOK ||
		len(decode[oapi.ConversationPage](t, rec).Items) != 3 {
		t.Fatalf("inbox: %d %s", rec.Code, rec.Body)
	}
}

// fakeScanner finds a virus in files that contain "EICAR"; down makes it
// unreachable.
type fakeScanner struct{ down bool }

func (f *fakeScanner) Scan(_ context.Context, r io.Reader) (clamav.Verdict, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return clamav.Verdict{}, err
	}
	if f.down {
		return clamav.Verdict{}, clamav.ErrScan
	}
	if bytes.Contains(b, []byte("EICAR")) {
		return clamav.Verdict{Infected: true, Signature: "Eicar-Test-Signature"}, nil
	}
	return clamav.Verdict{}, nil
}

func TestIngest_ProcessScan(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	ws := e.workspaceOf(a)
	app := e.appClient("198.51.100.1")
	scanner := &fakeScanner{down: true}
	svc := ingest.New(e.app, ingest.Options{Files: e.files, Inbox: e.convs, Scanner: scanner})

	clean := reportFile{"app.log", "text/plain", []byte("all good\n")}
	virus := reportFile{"dump.log", "text/plain", []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*\n")}
	id := e.sendReport(a, app, report("Вылетает при запуске", clean, virus), clean, virus)

	// clamd is away: the job fails and River retries; nothing reaches the inbox.
	if err := e.process(svc, ws, id, false); !errors.Is(err, clamav.ErrScan) {
		t.Fatalf("process with clamd away: %v", err)
	}
	if n := e.ownerCount("SELECT count(*) FROM conversations WHERE workspace_id = $1", ws); n != 0 {
		t.Fatal("conversation before the scan")
	}
	scanner.down = false
	if err := e.process(svc, ws, id, false); err != nil {
		t.Fatalf("process: %v", err)
	}
	if n := e.ownerCount(`SELECT count(*) FROM messages m JOIN app_reports r ON r.conversation_id = m.conversation_id
		WHERE r.id = $1 AND m.meta->'report'->'files' = '["ok", "infected"]'::jsonb`, id); n != 1 {
		t.Fatal("infected file is not marked")
	}
	var key string
	e.ownerScan(&key, "SELECT storage_key FROM app_report_files WHERE report_id = $1 AND file_name = 'dump.log'", id)
	if _, err := e.files.Stat(context.Background(), key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("infected file still stored: %v", err)
	}

	// On the last attempt an unscannable file is rejected, the report arrives.
	scanner.down = true
	id = e.sendReport(a, app, report("Ещё раз", clean), clean)
	if err := e.process(svc, ws, id, true); err != nil {
		t.Fatalf("last attempt: %v", err)
	}
	if n := e.ownerCount(`SELECT count(*) FROM messages m JOIN app_reports r ON r.conversation_id = m.conversation_id
		WHERE r.id = $1 AND m.meta->'report'->'files' = '["not_scanned"]'::jsonb`, id); n != 1 {
		t.Fatal("unscanned file is not marked")
	}
}

func TestIngest_Cleanup(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	ws := e.workspaceOf(a)
	app := e.appClient("198.51.100.1")
	sys := e.db.OpenSystem(t)
	ctx := context.Background()
	logFile := reportFile{"app.log", "text/plain", []byte("line\n")}

	// An unfinished report with one uploaded file, an hour old.
	rec := app.do(http.MethodPost, "/api/v1/ingest/reports", report("Не дошло", logFile), bearer(a.key))
	stale := decode[oapi.IngestReportCreated](t, rec)
	if code := e.upload(stale.Uploads[0], logFile.body); code != http.StatusOK {
		t.Fatalf("upload: %d", code)
	}
	var staleKey string
	e.ownerScan(&staleKey, "SELECT storage_key FROM app_report_files WHERE report_id = $1", stale.ReportId)
	e.ownerExec("UPDATE app_reports SET created_at = now() - interval '61 minutes' WHERE id = $1", stale.ReportId)
	// A fresh unfinished report stays.
	fresh := decode[oapi.IngestReportCreated](t, app.do(http.MethodPost, "/api/v1/ingest/reports", report("Загружаю"), bearer(a.key)))

	// A processed report whose retention has ended.
	done := e.sendReport(a, app, report("Готово", logFile), logFile)
	if err := e.process(e.ingest, ws, done, false); err != nil {
		t.Fatalf("process: %v", err)
	}
	var doneKey string
	e.ownerScan(&doneKey, "SELECT storage_key FROM app_report_files WHERE report_id = $1", done)
	e.ownerExec("UPDATE app_reports SET files_expire_at = now() - interval '1 minute' WHERE id = $1", done)

	//nolint:forbidigo // the system pool is what the worker passes in cmd
	if err := e.ingest.Cleanup(ctx, sys.Pool()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n := e.ownerCount("SELECT count(*) FROM app_reports WHERE id = $1", stale.ReportId); n != 0 {
		t.Fatal("unfinished report is not removed")
	}
	if _, err := e.files.Stat(ctx, staleKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("file of the unfinished report: %v", err)
	}
	// A late complete finds nothing.
	wantProblem(t, app.do(http.MethodPost, "/api/v1/ingest/reports/"+stale.ReportId.String()+"/complete", nil,
		bearer(a.key), header("X-Report-Token", stale.ReportToken)), http.StatusNotFound, "not_found")
	if n := e.ownerCount("SELECT count(*) FROM app_reports WHERE id = $1 AND status = 'uploading'", fresh.ReportId); n != 1 {
		t.Fatal("fresh report removed")
	}
	if _, err := e.files.Stat(ctx, doneKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("file past retention: %v", err)
	}
	if n := e.ownerCount(`SELECT count(*) FROM app_reports r JOIN attachments t ON t.storage_key = $2 AND t.status = 'expired'
		JOIN messages m ON m.id = t.message_id AND m.body = 'Готово'
		WHERE r.id = $1 AND r.files_expired_at IS NOT NULL`, done, doneKey); n != 1 {
		t.Fatal("attachment is not expired or the message is gone")
	}
	// Nothing is left for the next run.
	if err := e.ingest.Cleanup(ctx, sys.Pool()); err != nil { //nolint:forbidigo // as above
		t.Fatalf("second cleanup: %v", err)
	}
}
