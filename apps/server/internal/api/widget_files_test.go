package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

var pngFile = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR screenshot")

// uploadFile registers a file in the widget and puts it by its link, as the
// widget does from the page.
func (v *visitor) uploadFile(name, contentType string, body []byte) oapi.WidgetUpload {
	v.c.e.t.Helper()
	up := v.register(name, contentType, int64(len(body)))
	if code := v.put(up, body); code != http.StatusOK {
		v.c.e.t.Fatalf("put %s: %d", name, code)
	}
	return up
}

func (v *visitor) register(name, contentType string, size int64) oapi.WidgetUpload {
	v.c.e.t.Helper()
	rec := v.do(http.MethodPost, "/api/v1/widget/uploads", map[string]any{"file_name": name, "content_type": contentType, "size": size})
	if rec.Code != http.StatusCreated {
		v.c.e.t.Fatalf("register %s: %d %s", name, rec.Code, rec.Body)
	}
	return decode[oapi.WidgetUpload](v.c.e.t, rec)
}

// put sends the bytes from the page's origin, as a browser would.
func (v *visitor) put(up oapi.WidgetUpload, body []byte) int {
	req := httptest.NewRequest(up.Upload.Method, up.Upload.Url, bytes.NewReader(body))
	for k, val := range up.Upload.Headers {
		req.Header.Set(k, val)
	}
	req.Header.Set("Origin", v.origin)
	rec := httptest.NewRecorder()
	v.c.e.h.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK && rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		v.c.e.t.Fatalf("the page cannot read the upload's answer: %v", rec.Header())
	}
	return rec.Code
}

func (v *visitor) sendFiles(text, key string, ids ...uuid.UUID) *httptest.ResponseRecorder {
	v.c.e.t.Helper()
	body := map[string]any{"attachment_ids": ids}
	if text != "" {
		body["message"] = text
	}
	var opts []reqOpt
	if key != "" {
		opts = append(opts, header("Idempotency-Key", key))
	}
	return v.do(http.MethodPost, "/api/v1/widget/messages", body, opts...)
}

func wantUploadProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string, upload uuid.UUID) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("want %d, got %d %s", status, rec.Code, rec.Body)
	}
	if p := decode[Problem](t, rec); p.Code != code || p.UploadID != upload.String() {
		t.Fatalf("want %s for upload %s, got %+v", code, upload, p)
	}
}

// A visitor sends a screenshot and a log; the operator gets them as the
// files of the message, and the visitor sees them in the chat.
func TestWidget_Files(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.7", shopOrigin)
	v.start(w.key, nil)

	// A browser asks before a cross-origin PUT.
	pre := httptest.NewRequest(http.MethodOptions, "/api/v1/files/ws/x/conv/y", nil)
	pre.Header.Set("Origin", shopOrigin)
	pre.Header.Set("Access-Control-Request-Method", http.MethodPut)
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, pre)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" ||
		rec.Header().Get("Access-Control-Allow-Methods") != http.MethodPut || rec.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf("upload preflight: %d %v", rec.Code, rec.Header())
	}

	shot := v.uploadFile("../Скриншот.png", "image/png", pngFile)
	logFile := v.uploadFile("app.log", "text/plain; charset=utf-8", []byte("12:00:01 ERROR корзина пуста\n"))
	// Opening the widget and uploading create nothing in the inbox.
	if n := e.ownerCount("SELECT count(*) FROM contacts WHERE workspace_id = $1", w.ws); n != 0 {
		t.Fatalf("contacts before the first message: %d", n)
	}

	rec = v.sendFiles("Вот скриншот и лог", "f1", shot.Id, logFile.Id)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	msg := decode[oapi.WidgetMessage](t, rec)
	if len(msg.Files) != 2 || msg.Files[0].Name != "Скриншот.png" || msg.Files[0].ContentType != "image/png" ||
		msg.Files[1].Name != "app.log" || msg.Files[1].ContentType != "text/plain" || msg.Files[1].Size != int64(len("12:00:01 ERROR корзина пуста\n")) {
		t.Fatalf("files of the message: %+v", msg.Files)
	}
	if got := e.download(msg.Files[0].Download); got != string(pngFile) {
		t.Fatalf("the visitor's own file: %q", got)
	}
	// A retry with the key returns the message; the files go once.
	if rec := v.sendFiles("Вот скриншот и лог", "f1", shot.Id, logFile.Id); rec.Code != http.StatusOK ||
		decode[oapi.WidgetMessage](t, rec).Id != msg.Id {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	// Another message cannot take them.
	wantProblem(t, v.sendFiles("", "f2", shot.Id), http.StatusBadRequest, "invalid_request")

	// The operator sees the files in the conversation.
	conv := "/api/v1/conversations/" + msg.ConversationId.String()
	feed := decode[oapi.MessagePage](t, w.owner.do(http.MethodGet, conv+"/messages", nil))
	var got *oapi.Message
	for i := range feed.Items {
		if feed.Items[i].Id == msg.Id {
			got = &feed.Items[i]
		}
	}
	if got == nil || got.Body != "Вот скриншот и лог" || len(got.Attachments) != 2 || got.Attachments[0].Status != oapi.AttachmentStatusReady {
		t.Fatalf("operator's feed: %+v", feed.Items)
	}
	link := decode[oapi.SignedRequest](t, w.owner.do(http.MethodGet, "/api/v1/attachments/"+got.Attachments[1].Id.String()+"/download-link", nil))
	if body := e.download(link); body != "12:00:01 ERROR корзина пуста\n" {
		t.Fatalf("operator's download: %q", body)
	}

	// A message of files only.
	only := v.uploadFile("photo.webp", "image/webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 data"))
	rec = v.sendFiles("", "", only.Id)
	if rec.Code != http.StatusCreated {
		t.Fatalf("files only: %d %s", rec.Code, rec.Body)
	}
	if m := decode[oapi.WidgetMessage](t, rec); m.Body != "" || len(m.Files) != 1 {
		t.Fatalf("files only: %+v", m)
	}
	var kind string
	e.ownerScan(&kind, "SELECT kind FROM messages WHERE body = '' AND conversation_id = $1", msg.ConversationId)
	if kind != "media" {
		t.Fatalf("kind of a files-only message: %s", kind)
	}
	page := v.messages("")
	if len(page.Items) != 2 || len(page.Items[0].Files) != 2 || len(page.Items[1].Files) != 1 {
		t.Fatalf("chat: %+v", page.Items)
	}
}

// What a visitor may not upload or send.
func TestWidget_FilesRefused(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.7", shopOrigin)
	v.start(w.key, nil)
	register := func(name, ct string, size int64) *httptest.ResponseRecorder {
		return v.do(http.MethodPost, "/api/v1/widget/uploads", map[string]any{"file_name": name, "content_type": ct, "size": size})
	}
	wantProblem(t, register("page.html", "text/html", 10), http.StatusBadRequest, "invalid_request")
	wantProblem(t, register("run.exe", "application/octet-stream", 10), http.StatusBadRequest, "invalid_request")
	wantProblem(t, register("big.png", "image/png", 10<<20+1), http.StatusBadRequest, "invalid_request")
	wantProblem(t, register("   ", "image/png", 10), http.StatusBadRequest, "invalid_request")
	// Without a session nothing is registered.
	anon := e.visitor("198.51.100.8", shopOrigin)
	anon.token = "tk_wsess_nope"
	wantProblem(t, anon.do(http.MethodPost, "/api/v1/widget/uploads", map[string]any{"file_name": "a.png", "content_type": "image/png", "size": 10}),
		http.StatusUnauthorized, "widget_session_ended")

	// An executable passed off as a screenshot is deleted and refused.
	fake := v.uploadFile("screen.png", "image/png", []byte("MZ\x90\x00 not a picture"))
	wantUploadProblem(t, v.sendFiles("Смотрите", "", fake.Id), http.StatusUnprocessableEntity, "file_type_mismatch", fake.Id)
	if n := e.ownerCount("SELECT count(*) FROM widget_uploads WHERE id = $1", fake.Id); n != 0 {
		t.Fatal("a rejected upload stays")
	}
	if n := e.ownerCount("SELECT count(*) FROM messages WHERE body = 'Смотрите'"); n != 0 {
		t.Fatal("the message of a rejected file was stored")
	}

	// Registered but never uploaded, or not whole.
	missing := v.register("later.png", "image/png", int64(len(pngFile)))
	wantUploadProblem(t, v.sendFiles("", "", missing.Id), http.StatusConflict, "attachment_not_uploaded", missing.Id)
	if code := v.put(missing, pngFile[:5]); code == http.StatusOK {
		t.Fatal("a shorter body was stored")
	}

	// Another visitor's file, an unknown id, too many, repeats.
	other := e.visitor("198.51.100.9", shopOrigin)
	other.start(w.key, nil)
	theirs := other.uploadFile("theirs.png", "image/png", pngFile)
	wantProblem(t, v.sendFiles("", "", theirs.Id), http.StatusBadRequest, "invalid_request")
	wantProblem(t, v.sendFiles("", "", uuid.New()), http.StatusBadRequest, "invalid_request")
	mine := v.uploadFile("mine.png", "image/png", pngFile)
	wantProblem(t, v.sendFiles("", "", mine.Id, mine.Id), http.StatusBadRequest, "invalid_request")
	six := make([]uuid.UUID, 6)
	for i := range six {
		six[i] = uuid.New()
	}
	wantProblem(t, v.sendFiles("", "", six...), http.StatusBadRequest, "invalid_request")
	wantProblem(t, v.sendFiles("", ""), http.StatusBadRequest, "invalid_request")

	// An upload older than an hour no longer goes.
	e.ownerExec("UPDATE widget_uploads SET created_at = created_at - interval '61 minutes' WHERE id = $1", mine.Id)
	wantProblem(t, v.sendFiles("", "", mine.Id), http.StatusBadRequest, "invalid_request")

	// Uploads are rate limited per session.
	e.resetRate("widget:uploads-ip:198.51.100.7")
	for i := 0; ; i++ {
		rec := register("a.png", "image/png", 10)
		if rec.Code == http.StatusTooManyRequests {
			if i == 0 || rec.Header().Get("Retry-After") == "" {
				t.Fatalf("limited after %d uploads: %v", i, rec.Header())
			}
			break
		}
		if rec.Code != http.StatusCreated || i > 25 {
			t.Fatalf("upload %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

// With an antivirus, an infected file is deleted and refused; when the
// antivirus does not answer, the visitor tries again later.
func TestWidget_FilesScanned(t *testing.T) {
	scanner := &fakeScanner{}
	e := newEnv(t, envOptions{widgetScanner: scanner})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.7", shopOrigin)
	v.start(w.key, nil)

	bad := v.uploadFile("notes.txt", "text/plain", []byte("X5O!P%@AP EICAR test"))
	wantUploadProblem(t, v.sendFiles("", "", bad.Id), http.StatusUnprocessableEntity, "file_infected", bad.Id)

	good := v.uploadFile("notes.txt", "text/plain", []byte("заказ 1187"))
	scanner.down = true
	rec := v.sendFiles("", "s1", good.Id)
	wantProblem(t, rec, http.StatusServiceUnavailable, "scan_unavailable")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	scanner.down = false
	if rec := v.sendFiles("", "s1", good.Id); rec.Code != http.StatusCreated {
		t.Fatalf("after the antivirus is back: %d %s", rec.Code, rec.Body)
	}
}

// The worker deletes uploads no message took, with their files, and the
// rows of sent ones, whose files stay with the messages.
func TestWidget_UploadsCleanup(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.7", shopOrigin)
	v.start(w.key, nil)
	sent := v.uploadFile("sent.png", "image/png", pngFile)
	if rec := v.sendFiles("", "", sent.Id); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	unsent := v.uploadFile("unsent.png", "image/png", pngFile)
	fresh := v.uploadFile("fresh.png", "image/png", pngFile)
	key := func(id uuid.UUID) string {
		var k string
		e.ownerScan(&k, "SELECT storage_key FROM widget_uploads WHERE id = $1", id)
		return k
	}
	sentKey, unsentKey, freshKey := key(sent.Id), key(unsent.Id), key(fresh.Id)
	e.ownerExec("UPDATE widget_uploads SET created_at = now() - interval '2 hours' WHERE id = ANY($1)", []uuid.UUID{sent.Id, unsent.Id})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sys := e.db.OpenSystem(t)
	if err := e.widget.CleanupUploads(ctx, sys.Pool()); err != nil { //nolint:forbidigo // the system pool is what the worker passes in cmd
		t.Fatalf("cleanup: %v", err)
	}
	if n := e.ownerCount("SELECT count(*) FROM widget_uploads WHERE workspace_id = $1", w.ws); n != 1 {
		t.Fatalf("uploads left: %d, want the fresh one", n)
	}
	for k, want := range map[string]bool{sentKey: true, unsentKey: false, freshKey: true} {
		_, err := e.files.Stat(ctx, k)
		if exists := err == nil; exists != want {
			t.Fatalf("file %s exists %v, want %v (%v)", k, exists, want, err)
		}
	}
}
