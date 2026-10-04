package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// The app user's side of a report (AP-06): the app reads the replies and
// writes back into the same conversation with the key and the report token.
func TestIngest_ReportThread(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	ws := e.workspaceOf(a)
	app := e.appClient("198.51.100.1")
	created := e.submitReport(a, app, report("Не синхронизируется корзина"))
	base := "/api/v1/ingest/reports/" + created.ReportId.String()
	auth := []reqOpt{bearer(a.key), header("X-Report-Token", created.ReportToken)}
	replies := func(since string) *httptest.ResponseRecorder {
		path := base + "/replies"
		if since != "" {
			path += "?since=" + since
		}
		return app.do(http.MethodGet, path, nil, auth...)
	}
	write := func(text, key string) *httptest.ResponseRecorder {
		opts := auth
		if key != "" {
			opts = append(append([]reqOpt{}, auth...), header("Idempotency-Key", key))
		}
		return app.do(http.MethodPost, base+"/messages", map[string]any{"message": text}, opts...)
	}

	// Before the report is a conversation: nothing to read, writing waits.
	rec := replies("")
	if rec.Code != http.StatusOK {
		t.Fatalf("replies before processing: %d %s", rec.Code, rec.Body)
	}
	if p := decode[oapi.IngestReplies](t, rec); len(p.Items) != 0 || p.Cursor != "" || p.More {
		t.Fatalf("replies before processing: %s", rec.Body)
	}
	rec = write("Ещё не работает", "")
	wantProblem(t, rec, http.StatusConflict, "report_processing")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("report_processing without Retry-After")
	}

	// The token is what lets the app in: the key alone is extractable.
	other := e.connectApp("other@example.test", "other")
	for _, c := range []struct {
		opts   []reqOpt
		status int
		code   string
	}{
		{[]reqOpt{bearer(a.key), header("X-Report-Token", "tk_report_wrong")}, http.StatusNotFound, "not_found"},
		{[]reqOpt{bearer(other.key), header("X-Report-Token", created.ReportToken)}, http.StatusNotFound, "not_found"},
		{[]reqOpt{header("X-Report-Token", created.ReportToken)}, http.StatusUnauthorized, "invalid_key"},
	} {
		wantProblem(t, app.do(http.MethodGet, base+"/replies", nil, c.opts...), c.status, c.code)
	}

	if err := e.process(e.ingest, ws, created.ReportId, false); err != nil {
		t.Fatalf("process: %v", err)
	}
	var convID uuid.UUID
	e.ownerScan(&convID, "SELECT conversation_id FROM app_reports WHERE id = $1", created.ReportId)
	conv := "/api/v1/conversations/" + convID.String()

	// The operator takes it, leaves a note and replies with a file.
	if rec := a.owner.do(http.MethodPut, conv+"/assignee", map[string]any{"assignee_id": e.ownerID(a)}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	if rec := a.owner.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Похоже на баг 1187", "private": true}); rec.Code != http.StatusCreated {
		t.Fatalf("note: %d %s", rec.Code, rec.Body)
	}
	file := []byte("Шаги: Настройки → Синхронизация → Сбросить")
	sum := sha256.Sum256(file)
	rec = a.owner.do(http.MethodPost, conv+"/attachments", map[string]any{
		"file_name": "шаги.txt", "content_type": "text/plain", "size": len(file), "sha256": hex.EncodeToString(sum[:]),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	up := decode[oapi.AttachmentUpload](t, rec)
	if code := e.upload(oapi.IngestUpload{Upload: up.Upload}, file); code != http.StatusOK {
		t.Fatalf("put: %d", code)
	}
	rec = a.owner.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Сбросьте синхронизацию", "attachment_ids": []uuid.UUID{up.Attachment.Id}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	reply := decode[oapi.Message](t, rec)

	// A fresh reply waits a few seconds, so a reply still committing with a
	// smaller id is not skipped by the cursor.
	if p := decode[oapi.IngestReplies](t, replies("")); len(p.Items) != 0 {
		t.Fatalf("an unsettled reply was returned: %+v", p)
	}
	e.ownerExec("UPDATE messages SET created_at = created_at - interval '1 minute' WHERE conversation_id = $1", convID)

	// Rates are per report: a third read within the minute is refused.
	rec = replies("")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third read in a minute: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("RateLimit-Limit") != "2" {
		t.Fatalf("rate headers: %v", rec.Header())
	}
	e.resetRate("ingest:replies:" + created.ReportId.String())

	// Only the reply: no note, no "taken" line, no names.
	rec = replies("")
	if rec.Code != http.StatusOK || rec.Header().Get("RateLimit-Remaining") != "1" {
		t.Fatalf("replies: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	p := decode[oapi.IngestReplies](t, rec)
	if len(p.Items) != 1 || p.More || p.Cursor == "" {
		t.Fatalf("replies: %s", rec.Body)
	}
	got := p.Items[0]
	if got.Id != reply.Id || got.Sender != oapi.IngestReplySenderOperator || got.Body != "Сбросьте синхронизацию" ||
		len(got.Files) != 1 || got.Files[0].Name != "шаги.txt" || got.Files[0].Size != int64(len(file)) {
		t.Fatalf("reply: %s", rec.Body)
	}
	// The file downloads by its link.
	dl := httptest.NewRequest(got.Files[0].Download.Method, got.Files[0].Download.Url, nil)
	dlRec := httptest.NewRecorder()
	e.h.ServeHTTP(dlRec, dl)
	if dlRec.Code != http.StatusOK || dlRec.Body.String() != string(file) {
		t.Fatalf("download: %d", dlRec.Code)
	}
	// After the cursor: nothing new, the same cursor.
	if q := decode[oapi.IngestReplies](t, replies(p.Cursor)); len(q.Items) != 0 || q.Cursor != p.Cursor {
		t.Fatalf("after the cursor: %+v", q)
	}
	e.resetRate("ingest:replies:" + created.ReportId.String())
	wantProblem(t, replies("not-a-cursor"), http.StatusBadRequest, "invalid_request")

	// The user writes back: a customer message in the same conversation; a
	// retry with the same key stores nothing new.
	rec = write("Помогло, спасибо", "m-1")
	if rec.Code != http.StatusCreated || rec.Header().Get("RateLimit-Limit") != "10" {
		t.Fatalf("write: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	first := decode[oapi.IngestMessage](t, rec)
	if rec := write("Помогло, спасибо", "m-1"); rec.Code != http.StatusOK || decode[oapi.IngestMessage](t, rec).Id != first.Id {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, write("Другое", "m-1"), http.StatusUnprocessableEntity, "idempotency_key_reused")
	wantProblem(t, write("   ", ""), http.StatusBadRequest, "invalid_request")
	if n := e.ownerCount(`SELECT count(*) FROM messages WHERE conversation_id = $1 AND direction = 'in' AND sender_type = 'contact'
		AND body = 'Помогло, спасибо' AND id = $2`, convID, first.Id); n != 1 {
		t.Fatal("the user's message is not a customer message of the conversation")
	}

	// A closed conversation reopens to its operator instead of a new one.
	for _, st := range []string{"resolved", "closed"} {
		if rec := a.owner.do(http.MethodPost, conv+"/status", map[string]any{"status": st}); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", st, rec.Code, rec.Body)
		}
	}
	if rec := write("Снова сломалось после обновления", ""); rec.Code != http.StatusCreated {
		t.Fatalf("write after close: %d %s", rec.Code, rec.Body)
	}
	c := decode[oapi.Conversation](t, a.owner.do(http.MethodGet, conv, nil))
	if c.Status != "assigned" || c.AssigneeId == nil || *c.AssigneeId != e.ownerID(a) || c.ClosedAt != nil || c.ResolvedAt != nil {
		t.Fatalf("reopened: %+v", c)
	}
	if n := e.ownerCount("SELECT count(*) FROM conversations WHERE workspace_id = $1", ws); n != 1 {
		t.Fatalf("conversations: %d", n)
	}
	if n := e.ownerCount(`SELECT count(*) FROM messages WHERE conversation_id = $1 AND kind = 'event'
		AND meta->>'from' = 'closed' AND meta->>'to' = 'assigned'`, convID); n != 1 {
		t.Fatal("the reopening is not in the feed")
	}

	// Ten messages an hour per report.
	for i := 0; ; i++ {
		rec := write("ещё", "")
		if rec.Code == http.StatusTooManyRequests {
			if i != 4 { // six requests counted above, the refused ones too
				t.Fatalf("limited after %d more", i)
			}
			break
		}
		if rec.Code != http.StatusCreated || i > 10 {
			t.Fatalf("write %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

// ownerID is the user id of the app channel's owner.
func (e *env) ownerID(a ingestApp) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	e.ownerScan(&id, "SELECT created_by FROM channels WHERE id = $1", a.channel.Id)
	return id
}
