package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

var filesWS = uuid.MustParse("0192f0a0-0000-7000-8000-0000000000aa")

type fileEnv struct {
	fs  *storage.FS
	h   http.Handler
	now time.Time
}

func newFileEnv(t *testing.T) *fileEnv {
	t.Helper()
	env := &fileEnv{now: time.Now()}
	fs, err := storage.NewFS(storage.FSOptions{
		Root:        t.TempDir(),
		LinkBaseURL: "http://files.test" + FilesPath,
		SigningKey:  bytes.Repeat([]byte("k"), storage.MinSigningKeyLen),
		Now:         func() time.Time { return env.now },
	})
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	env.fs = fs
	env.h = NewRouter(Deps{Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), DB: pinger{}, Version: "test", Files: fs})
	return env
}

func (e *fileEnv) key(t *testing.T) string {
	t.Helper()
	k, err := storage.NewKey(filesWS, storage.KindReports)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// send performs a signed request against the router, as a client would with
// the URL and headers it was given.
func (e *fileEnv) send(t *testing.T, sr storage.SignedRequest, rawURL string, body []byte, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	if rawURL == "" {
		rawURL = sr.URL
	}
	req := httptest.NewRequest(sr.Method, rawURL, bytes.NewReader(body))
	for k, v := range sr.Header {
		req.Header[k] = v
	}
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem response (%d): %s", rec.Code, rec.Body)
	}
	return p.Code
}

func TestFiles_UploadThenDownload(t *testing.T) {
	ctx := context.Background()
	e := newFileEnv(t)
	key := e.key(t)
	body := []byte("app crashed at line 42\n")
	sum := sha256.Sum256(body)

	put, err := e.fs.PresignPut(ctx, key, storage.PresignPutOptions{
		ContentType: "text/plain", MaxSize: 1024, Size: int64(len(body)), SHA256: sum[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.send(t, put, "", body, nil); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}

	get, err := e.fs.PresignGet(ctx, key, storage.PresignGetOptions{Filename: "отчёт.log", ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.send(t, get, "", nil, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("download: %d %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/plain" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), "attachment; filename*=utf-8''") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download headers: %v", h)
	}
}

func TestFiles_DownloadDefaultsToOctetStream(t *testing.T) {
	ctx := context.Background()
	e := newFileEnv(t)
	key := e.key(t)
	if _, err := e.fs.Put(ctx, key, strings.NewReader("<html><script>alert(1)</script>"), storage.PutOptions{MaxSize: 100}); err != nil {
		t.Fatal(err)
	}
	get, _ := e.fs.PresignGet(ctx, key, storage.PresignGetOptions{Filename: "x.html"})
	rec := e.send(t, get, "", nil, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("Content-Disposition") != "attachment; filename=x.html" {
		t.Fatalf("got %d %v", rec.Code, rec.Header())
	}
}

func TestFiles_UploadRejections(t *testing.T) {
	ctx := context.Background()
	e := newFileEnv(t)
	other := sha256.Sum256([]byte("other"))

	tests := []struct {
		name   string
		opts   storage.PresignPutOptions
		body   string
		header http.Header
		status int
		code   string
	}{
		{"over max size", storage.PresignPutOptions{MaxSize: 4}, "12345", nil, http.StatusRequestEntityTooLarge, "file_too_large"},
		{"size mismatch", storage.PresignPutOptions{MaxSize: 10, Size: 3}, "1234", nil, http.StatusBadRequest, "size_mismatch"},
		{"checksum mismatch", storage.PresignPutOptions{MaxSize: 10, SHA256: other[:]}, "1234", nil, http.StatusBadRequest, "checksum_mismatch"},
		{"wrong content type", storage.PresignPutOptions{MaxSize: 10, ContentType: "text/plain"}, "1234",
			http.Header{"Content-Type": {"text/html"}}, http.StatusUnsupportedMediaType, "content_type_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := e.key(t)
			put, err := e.fs.PresignPut(ctx, key, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			rec := e.send(t, put, "", []byte(tt.body), tt.header)
			if rec.Code != tt.status || problemCode(t, rec) != tt.code {
				t.Fatalf("want %d %s, got %d %s", tt.status, tt.code, rec.Code, rec.Body)
			}
			if _, err := e.fs.Stat(ctx, key); err == nil {
				t.Fatalf("rejected upload was stored")
			}
		})
	}
}

// The body limit must hold even when the client lies about Content-Length
// (chunked transfer, no length): MaxBytesReader and the driver stop it.
func TestFiles_UploadLimitWithoutContentLength(t *testing.T) {
	e := newFileEnv(t)
	key := e.key(t)
	put, _ := e.fs.PresignPut(context.Background(), key, storage.PresignPutOptions{MaxSize: 8})
	req := httptest.NewRequest(http.MethodPut, put.URL, io.MultiReader(strings.NewReader(strings.Repeat("x", 1<<20))))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d %s", rec.Code, rec.Body)
	}
}

func TestFiles_UploadLinkIsSingleUse(t *testing.T) {
	e := newFileEnv(t)
	put, _ := e.fs.PresignPut(context.Background(), e.key(t), storage.PresignPutOptions{MaxSize: 10})
	if rec := e.send(t, put, "", []byte("one"), nil); rec.Code != http.StatusOK {
		t.Fatalf("first upload: %d", rec.Code)
	}
	rec := e.send(t, put, "", []byte("two"), nil)
	if rec.Code != http.StatusConflict || problemCode(t, rec) != "already_uploaded" {
		t.Fatalf("second upload: %d %s", rec.Code, rec.Body)
	}
}

func TestFiles_LinkChecks(t *testing.T) {
	ctx := context.Background()
	e := newFileEnv(t)
	key := e.key(t)
	if _, err := e.fs.Put(ctx, key, strings.NewReader("data"), storage.PutOptions{MaxSize: 10}); err != nil {
		t.Fatal(err)
	}
	get, _ := e.fs.PresignGet(ctx, key, storage.PresignGetOptions{Filename: "a.txt"})
	put, _ := e.fs.PresignPut(ctx, e.key(t), storage.PresignPutOptions{MaxSize: 10})

	tamper := func(raw string, mutate func(u *url.URL)) string {
		u, _ := url.Parse(raw)
		mutate(u)
		return u.String()
	}
	otherKey := e.key(t)
	cases := []struct {
		name   string
		sr     storage.SignedRequest
		rawURL string
		code   string
	}{
		{"raised limit", put, tamper(put.URL, func(u *url.URL) { q := u.Query(); q.Set("max", "999999"); u.RawQuery = q.Encode() }), "link_invalid"},
		{"swapped key", get, tamper(get.URL, func(u *url.URL) { u.Path = FilesPath + "/" + otherKey }), "link_invalid"},
		{"traversal key", get, tamper(get.URL, func(u *url.URL) { u.Path = FilesPath + "/ws/../../etc/passwd" }), "link_invalid"},
		{"no signature", get, tamper(get.URL, func(u *url.URL) { u.RawQuery = "" }), "link_invalid"},
		{"GET link used for PUT", storage.SignedRequest{Method: http.MethodPut}, get.URL, "link_invalid"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.send(t, tt.sr, tt.rawURL, []byte("x"), nil)
			if rec.Code != http.StatusForbidden || problemCode(t, rec) != tt.code {
				t.Fatalf("want 403 %s, got %d %s", tt.code, rec.Code, rec.Body)
			}
		})
	}

	t.Run("expired", func(t *testing.T) {
		e.now = e.now.Add(storage.DefaultURLTTL + time.Second)
		defer func() { e.now = e.now.Add(-storage.DefaultURLTTL - time.Second) }()
		rec := e.send(t, get, "", nil, nil)
		if rec.Code != http.StatusForbidden || problemCode(t, rec) != "link_expired" {
			t.Fatalf("want 403 link_expired, got %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("missing object", func(t *testing.T) {
		missing, _ := e.fs.PresignGet(ctx, e.key(t), storage.PresignGetOptions{})
		if rec := e.send(t, missing, "", nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", rec.Code)
		}
	})
}

func TestFiles_NotMountedWithoutLocalDriver(t *testing.T) {
	rec := do(t, newRouter(pinger{}, io.Discard), http.MethodGet, FilesPath+"/ws/x")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 without the fs driver, got %d", rec.Code)
	}
}

func TestFiles_LogsDoNotContainKeyOrSignature(t *testing.T) {
	var logs bytes.Buffer
	e := newFileEnv(t)
	e.h = NewRouter(Deps{Log: slog.New(slog.NewJSONHandler(&logs, nil)), DB: pinger{}, Files: e.fs})
	key := e.key(t)
	get, _ := e.fs.PresignGet(context.Background(), key, storage.PresignGetOptions{})
	e.send(t, get, "", nil, nil)
	u, _ := url.Parse(get.URL)
	if strings.Contains(logs.String(), key) || strings.Contains(logs.String(), u.Query().Get("sig")) {
		t.Fatalf("key or signature leaked into logs: %s", logs.String())
	}
}
