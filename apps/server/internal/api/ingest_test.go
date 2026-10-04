package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// ingestApp is an app channel with its key and identity secret.
type ingestApp struct {
	owner    *client
	channel  oapi.Channel
	key      string
	identity string
}

func (e *env) connectApp(email, slug string) ingestApp {
	e.t.Helper()
	c, _ := e.register(email, slug)
	rec := c.do(http.MethodPost, "/api/v1/channels/app", map[string]any{"name": "Зерно"})
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("connect app: %d %s", rec.Code, rec.Body)
	}
	app := decode[oapi.AppChannelCreated](e.t, rec)
	return ingestApp{owner: c, channel: app.Channel, key: app.IngestKey.Secret, identity: app.IdentitySecret}
}

// appClient is the company app: no cookies, its own address.
func (e *env) appClient(ip string) *client { return &client{e: e, ip: ip} }

func bearer(key string) reqOpt { return header("Authorization", "Bearer "+key) }

type reportFile struct {
	name, contentType string
	body              []byte
}

func (f reportFile) field() map[string]any {
	sum := sha256.Sum256(f.body)
	return map[string]any{"name": f.name, "size": len(f.body), "content_type": f.contentType, "sha256": hex.EncodeToString(sum[:])}
}

func report(message string, files ...reportFile) map[string]any {
	list := make([]map[string]any, 0, len(files))
	for _, f := range files {
		list = append(list, f.field())
	}
	return map[string]any{
		"message": message,
		"app":     map[string]any{"name": "Зерно", "version": "2.4.1", "os": "Windows", "os_version": "11 23H2", "extra": map[string]any{"build": "1187"}},
		"files":   list,
	}
}

// upload puts a file by its signed link, as the app does.
func (e *env) upload(u oapi.IngestUpload, body []byte) int {
	e.t.Helper()
	req := httptest.NewRequest(u.Upload.Method, u.Upload.Url, bytes.NewReader(body))
	for k, v := range u.Upload.Headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code
}

func identityToken(secret, externalID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(externalID))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestIngest_ReportWithFiles(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	app := e.appClient("198.51.100.1")
	logFile := reportFile{"app.log", "text/plain", []byte("2026-10-05 12:00:01 ERROR sync failed\n")}
	shot := reportFile{"screen.png", "image/png", []byte("\x89PNG\r\n\x1a\nnot really")}

	body := report("Не синхронизируется корзина", logFile, shot)
	body["contact"] = map[string]any{
		"external_id": "user-42", "email": "anna@example.test", "name": "Анна", "identity_token": identityToken(a.identity, "user-42"),
	}
	rec := app.do(http.MethodPost, "/api/v1/ingest/reports", body, bearer(a.key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("RateLimit-Limit") != "60" || rec.Header().Get("RateLimit-Remaining") != "59" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", rec.Header())
	}
	created := decode[oapi.IngestReportCreated](t, rec)
	if !created.ContactVerified || !strings.HasPrefix(created.ReportToken, "tk_report_") || len(created.Uploads) != 2 ||
		created.Uploads[0].Name != "app.log" || created.Uploads[1].Name != "screen.png" || created.Uploads[0].Upload.Method != http.MethodPut {
		t.Fatalf("created: %+v", created)
	}
	tokenHash := sha256.Sum256([]byte(created.ReportToken))
	if n := e.ownerCount(`SELECT count(*) FROM app_reports WHERE id = $1 AND status = 'uploading' AND token_hash = $2
		AND contact_verified AND contact_external_id = 'user-42' AND total_size = $3 AND app->>'version' = '2.4.1'`,
		created.ReportId, tokenHash[:], len(logFile.body)+len(shot.body)); n != 1 {
		t.Fatal("report row")
	}
	if n := e.ownerCount("SELECT count(*) FROM ingest_keys WHERE channel_id = $1 AND last_used_at IS NOT NULL", a.channel.Id); n != 1 {
		t.Fatal("last_used_at is not set")
	}

	completePath := "/api/v1/ingest/reports/" + created.ReportId.String() + "/complete"
	token := header("X-Report-Token", created.ReportToken)
	if code := e.upload(created.Uploads[0], logFile.body); code != http.StatusOK {
		t.Fatalf("upload log: %d", code)
	}
	// The link takes only the declared content.
	if code := e.upload(created.Uploads[1], []byte("something else")); code == http.StatusOK {
		t.Fatal("the link accepted another file")
	}
	rec = app.do(http.MethodPost, completePath, nil, bearer(a.key), token)
	wantProblem(t, rec, http.StatusConflict, "upload_incomplete")
	if !strings.Contains(decode[Problem](t, rec).Detail, "screen.png") {
		t.Fatalf("detail does not name the file: %s", rec.Body)
	}
	if code := e.upload(created.Uploads[1], shot.body); code != http.StatusOK {
		t.Fatalf("upload screenshot: %d", code)
	}

	// A wrong token, another key's channel or no token find nothing.
	wantProblem(t, app.do(http.MethodPost, completePath, nil, bearer(a.key), header("X-Report-Token", "tk_report_x")), http.StatusNotFound, "not_found")
	other := e.connectApp("other@example.test", "other")
	wantProblem(t, app.do(http.MethodPost, completePath, nil, bearer(other.key), token), http.StatusNotFound, "not_found")
	wantProblem(t, app.do(http.MethodPost, completePath, nil, bearer(a.key)), http.StatusBadRequest, "invalid_request")
	wantProblem(t, app.do(http.MethodPost, "/api/v1/ingest/reports/nope/complete", nil, bearer(a.key), token), http.StatusNotFound, "not_found")

	for range 2 { // completing twice is fine
		if rec := app.do(http.MethodPost, completePath, nil, bearer(a.key), token); rec.Code != http.StatusAccepted {
			t.Fatalf("complete: %d %s", rec.Code, rec.Body)
		}
	}
	if n := e.ownerCount("SELECT count(*) FROM app_reports WHERE id = $1 AND status = 'submitted' AND submitted_at IS NOT NULL", created.ReportId); n != 1 {
		t.Fatal("not submitted")
	}

	// A wrong identity token makes an unverified contact, not an error.
	body["contact"] = map[string]any{"external_id": "user-42", "identity_token": identityToken(a.identity, "user-43")}
	rec = app.do(http.MethodPost, "/api/v1/ingest/reports", body, bearer(a.key))
	if rec.Code != http.StatusCreated || decode[oapi.IngestReportCreated](t, rec).ContactVerified {
		t.Fatalf("forged identity: %d %s", rec.Code, rec.Body)
	}
	// Without files the report is complete at once.
	rec = app.do(http.MethodPost, "/api/v1/ingest/reports", map[string]any{"message": "Просто вопрос"}, bearer(a.key))
	plain := decode[oapi.IngestReportCreated](t, rec)
	if rec.Code != http.StatusCreated || plain.ContactVerified || len(plain.Uploads) != 0 {
		t.Fatalf("no files: %d %s", rec.Code, rec.Body)
	}
	if rec := app.do(http.MethodPost, "/api/v1/ingest/reports/"+plain.ReportId.String()+"/complete", nil,
		bearer(a.key), header("X-Report-Token", plain.ReportToken)); rec.Code != http.StatusAccepted {
		t.Fatalf("complete without files: %d %s", rec.Code, rec.Body)
	}
}

func TestIngest_Keys(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	body := map[string]any{"message": "Не работает"}
	path := "/api/v1/ingest/reports"

	// A forged workspace in an otherwise well-formed key finds nothing.
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(a.key, "tk_ingest_"))
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	forged := "tk_ingest_" + base64.RawURLEncoding.EncodeToString(append(raw[:32:32], other[:]...))
	for i, opt := range []reqOpt{
		func(*http.Request) {}, bearer(""), bearer("tk_ingest_short"), bearer(forged), header("Authorization", a.key),
		bearer(strings.Replace(a.key, "tk_ingest_", "tk_identity_", 1)),
	} {
		rec := e.appClient("198.51.100.2").do(http.MethodPost, path, body, opt)
		wantProblem(t, rec, http.StatusUnauthorized, "invalid_key")
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("case %d: no WWW-Authenticate", i)
		}
	}

	// A revoked key stops at once.
	keys := decode[oapi.IngestKeyList](t, a.owner.do(http.MethodGet, "/api/v1/channels/"+a.channel.Id.String()+"/ingest-keys", nil))
	a.owner.do(http.MethodDelete, "/api/v1/channels/"+a.channel.Id.String()+"/ingest-keys/"+keys.Items[0].Id.String(), nil)
	wantProblem(t, e.appClient("198.51.100.3").do(http.MethodPost, path, body, bearer(a.key)), http.StatusUnauthorized, "invalid_key")

	// So does every key of a disconnected channel.
	b := e.connectApp("b@example.test", "bravo")
	if rec := e.appClient("198.51.100.3").do(http.MethodPost, path, body, bearer(b.key)); rec.Code != http.StatusCreated {
		t.Fatalf("before disconnect: %d %s", rec.Code, rec.Body)
	}
	b.owner.do(http.MethodDelete, "/api/v1/channels/"+b.channel.Id.String(), nil)
	wantProblem(t, e.appClient("198.51.100.3").do(http.MethodPost, path, body, bearer(b.key)), http.StatusUnauthorized, "invalid_key")
}

func TestIngest_Limits(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	path := "/api/v1/ingest/reports"
	ip := 0
	send := func(body map[string]any, opts ...reqOpt) *httptest.ResponseRecorder {
		ip++ // a fresh address each time: the per-IP rate is tested apart
		return e.appClient(fmt.Sprintf("203.0.113.%d", ip)).do(http.MethodPost, path, body, append(opts, bearer(a.key))...)
	}
	small := reportFile{"a.txt", "text/plain", []byte("x")}

	limits := map[string]any{"max_file_mb": 1, "max_files": 2, "max_report_mb": 1, "retention_days": 30, "daily_reports": 3, "daily_mb": 2000}
	if rec := a.owner.do(http.MethodPut, "/api/v1/channels/"+a.channel.Id.String()+"/app-limits", limits); rec.Code != http.StatusOK {
		t.Fatalf("limits: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, send(report("три файла", small, small, small)), http.StatusRequestEntityTooLarge, "too_many_files")
	big := report("большой", small)
	big["files"].([]map[string]any)[0]["size"] = 1<<20 + 1
	wantProblem(t, send(big), http.StatusRequestEntityTooLarge, "file_too_large")
	two := report("два по мегабайту", small, small)
	for _, f := range two["files"].([]map[string]any) {
		f["size"] = 1 << 20
	}
	wantProblem(t, send(two), http.StatusRequestEntityTooLarge, "report_too_large")
	wantProblem(t, send(report("exe", reportFile{"setup.exe", "application/x-msdownload", []byte("MZ")})), http.StatusUnsupportedMediaType, "unsupported_type")
	wantProblem(t, send(report("html", reportFile{"a.html", "text/html", []byte("<p>")})), http.StatusUnsupportedMediaType, "unsupported_type")
	wantProblem(t, send(report("  ")), http.StatusBadRequest, "invalid_request")
	wantProblem(t, send(report(strings.Repeat("я", 10001))), http.StatusBadRequest, "invalid_request")
	badSum := report("sha", small)
	badSum["files"].([]map[string]any)[0]["sha256"] = strings.Repeat("A", 64)
	wantProblem(t, send(badSum), http.StatusBadRequest, "invalid_request")
	wantProblem(t, send(map[string]any{"message": "x", "workspace_id": uuid.New()}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, send(map[string]any{"message": "x", "contact": map[string]any{"identity_token": "ab"}}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, send(map[string]any{"message": "x", "contact": map[string]any{"email": "not an email"}}), http.StatusBadRequest, "invalid_request")
	extra := map[string]any{}
	for i := range 21 {
		extra[string(rune('a'+i))] = "v"
	}
	wantProblem(t, send(map[string]any{"message": "x", "app": map[string]any{"extra": extra}}), http.StatusBadRequest, "invalid_request")
	if n := e.ownerCount("SELECT count(*) FROM app_reports"); n != 0 {
		t.Fatalf("rejected reports were stored: %d", n)
	}

	// The daily quota: 3 reports, then 429 until UTC midnight.
	for i := range 3 {
		if rec := send(report("ок", small)); rec.Code != http.StatusCreated {
			t.Fatalf("report %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := send(report("четвёртый", small))
	wantProblem(t, rec, http.StatusTooManyRequests, "daily_quota_exceeded")
	if rec.Header().Get("Retry-After") == "" || rec.Header().Get("RateLimit-Remaining") == "" {
		t.Fatalf("headers: %v", rec.Header())
	}

	// Ten reports a minute from one address.
	b := e.connectApp("b@example.test", "bravo")
	same := e.appClient("192.0.2.77")
	for i := range 10 {
		if rec := same.do(http.MethodPost, path, map[string]any{"message": "x"}, bearer(b.key)); rec.Code != http.StatusCreated {
			t.Fatalf("report %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec = same.do(http.MethodPost, path, map[string]any{"message": "x"}, bearer(b.key))
	wantProblem(t, rec, http.StatusTooManyRequests, "too_many_requests")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestIngest_Idempotency(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	app := e.appClient("198.51.100.9")
	f := reportFile{"app.log", "text/plain", []byte("log")}
	body := report("Падает при запуске", f)
	key := header("Idempotency-Key", "c0a8f2c4-retry-1")
	path := "/api/v1/ingest/reports"

	first := decode[oapi.IngestReportCreated](t, app.do(http.MethodPost, path, body, bearer(a.key), key))
	rec := app.do(http.MethodPost, path, body, bearer(a.key), key)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	retry := decode[oapi.IngestReportCreated](t, rec)
	if retry.ReportId != first.ReportId || retry.ReportToken == first.ReportToken || len(retry.Uploads) != 1 {
		t.Fatalf("retry: %+v", retry)
	}
	if n := e.ownerCount("SELECT count(*) FROM app_reports"); n != 1 {
		t.Fatalf("reports: %d", n)
	}
	wantProblem(t, app.do(http.MethodPost, path, report("Другой текст", f), bearer(a.key), key), http.StatusUnprocessableEntity, "idempotency_key_reused")

	completePath := "/api/v1/ingest/reports/" + first.ReportId.String() + "/complete"
	if code := e.upload(retry.Uploads[0], f.body); code != http.StatusOK {
		t.Fatalf("upload: %d", code)
	}
	// The first token stopped working when the retry issued a new one.
	wantProblem(t, app.do(http.MethodPost, completePath, nil, bearer(a.key), header("X-Report-Token", first.ReportToken)), http.StatusNotFound, "not_found")
	if rec := app.do(http.MethodPost, completePath, nil, bearer(a.key), header("X-Report-Token", retry.ReportToken)); rec.Code != http.StatusAccepted {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, app.do(http.MethodPost, path, body, bearer(a.key), key), http.StatusConflict, "report_submitted")
}
