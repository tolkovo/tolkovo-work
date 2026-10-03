package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func newRouter(db Pinger, logs io.Writer) http.Handler {
	return NewRouter(Deps{Log: slog.New(slog.NewJSONHandler(logs, nil)), DB: db, Version: "test"})
}

func TestHealthz_AlwaysOK(t *testing.T) {
	rec := do(t, newRouter(pinger{errors.New("down")}, io.Discard), http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":"test"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestReadyz(t *testing.T) {
	if rec := do(t, newRouter(pinger{}, io.Discard), http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("db up: want 200, got %d", rec.Code)
	}
	rec := do(t, newRouter(pinger{errors.New("dial tcp: secret-host")}, io.Discard), http.MethodGet, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("db down: want 503, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-host") {
		t.Fatalf("internal error leaked to the client: %s", rec.Body)
	}
}

func TestUnknownRoute_ReturnsProblemJSON(t *testing.T) {
	rec := do(t, newRouter(pinger{}, io.Discard), http.MethodGet, "/api/v1/nope")
	if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Code != "not_found" || p.Status != 404 || p.Type != "about:blank" || p.RequestID == "" {
		t.Fatalf("unexpected problem: %+v", p)
	}
}

// Regression: chi's RequestID embedded the hostname into problem responses.
func TestRequestID_IsUUIDv7AndIgnoresClientValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "forged")
	rec := httptest.NewRecorder()
	newRouter(pinger{}, io.Discard).ServeHTTP(rec, req)

	id, err := uuid.Parse(rec.Header().Get("X-Request-Id"))
	if err != nil || id.Version() != 7 {
		t.Fatalf("want UUIDv7 request id, got %q (%v)", rec.Header().Get("X-Request-Id"), err)
	}
}

func TestRequestLogger_LogsRouteNotRawPath(t *testing.T) {
	var logs bytes.Buffer
	do(t, newRouter(pinger{}, &logs), http.MethodGet, "/healthz?token=abc123")
	if !strings.Contains(logs.String(), `"route":"/healthz"`) || strings.Contains(logs.String(), "abc123") {
		t.Fatalf("unexpected log line: %s", logs.String())
	}
}

func TestRecoverer_TurnsPanicIntoProblem(t *testing.T) {
	var logs bytes.Buffer
	h := recoverer(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := do(t, h, http.MethodGet, "/x")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(logs.String(), "boom") {
		t.Fatalf("got %d, logs %s", rec.Code, logs.String())
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("panic value leaked to the client: %s", rec.Body)
	}
}
