package api

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func panelRouter() http.Handler {
	panel := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Tolkovo</title>")},
		"theme-init.js":        {Data: []byte("// theme")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc.css": {Data: []byte("body{}")},
	}
	return NewRouter(Deps{Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), DB: pinger{}, Version: "test", Panel: panel})
}

func TestPanel_ServesBuildAndClientRoutes(t *testing.T) {
	h := panelRouter()
	for _, tc := range []struct {
		path, body, cache string
	}{
		{"/", "<title>Tolkovo</title>", "no-cache"},
		{"/inbox/0199f000-0000-7000-8000-000000000001", "<title>Tolkovo</title>", "no-cache"},
		{"/theme-init.js", "// theme", "no-cache"},
		{"/assets/index-abc.js", "console.log(1)", "public, max-age=31536000, immutable"},
	} {
		rec := do(t, h, http.MethodGet, tc.path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: got %d %q", tc.path, rec.Code, rec.Body)
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.cache {
			t.Errorf("%s: Cache-Control %q, want %q", tc.path, got, tc.cache)
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: security headers missing: %v", tc.path, rec.Header())
		}
	}
	if ct := do(t, h, http.MethodGet, "/assets/index-abc.css").Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("css Content-Type %q", ct)
	}
}

func TestPanel_NeverAnswersAPIOrMissingFilesWithHTML(t *testing.T) {
	h := panelRouter()
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/nope"},
		{http.MethodGet, "/api"},
		{http.MethodGet, "/assets/gone-123.js"},
		{http.MethodPost, "/inbox"},
	} {
		rec := do(t, h, tc.method, tc.path)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s: got %d %q", tc.method, tc.path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// The file server refuses ".." itself; nothing outside the build is read.
	if rec := do(t, h, http.MethodGet, "/../../etc/passwd"); rec.Code != http.StatusBadRequest {
		t.Errorf("dot-dot path: got %d %q", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("healthz shadowed by the panel: %d %s", rec.Code, rec.Body)
	}
}

func TestCheckPanel(t *testing.T) {
	if err := CheckPanel(fstest.MapFS{"index.html": {Data: []byte("x")}}); err != nil {
		t.Fatalf("built panel: %v", err)
	}
	if err := CheckPanel(fstest.MapFS{"assets/a.js": {Data: []byte("x")}}); err == nil {
		t.Fatal("no index.html: want an error")
	}
}
