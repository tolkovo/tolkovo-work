package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// env is the whole HTTP stack on a fresh database (application role, so
// RLS applies) and a unique Redis key prefix.
type env struct {
	t       *testing.T
	h       http.Handler
	tenancy *tenancy.Service
	ownerDB string // bypasses RLS: test fixtures only
}

type envOptions struct {
	secure  bool
	trusted []string
}

func newEnv(t *testing.T, o envOptions) *env {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	rc, prefix := rdbtest.New(t)
	ten := tenancy.New(app)
	a, err := auth.New(context.Background(), ten, rc, auth.Options{
		KeyPrefix: prefix, SessionIdleTimeout: time.Hour, SessionMaxAge: 24 * time.Hour,
		HashParams: auth.HashParams{MemoryKiB: 64, Time: 1, Threads: 1}, HashConcurrency: 4,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	h := NewRouter(Deps{
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), DB: pinger{}, Version: "test",
		Tenancy: ten, Auth: a, SecureCookies: o.secure, SessionMaxAge: 24 * time.Hour, TrustedOrigins: o.trusted,
	})
	return &env{t: t, h: h, tenancy: ten, ownerDB: d.OwnerURL}
}

// client is one browser: it keeps the session cookie and the CSRF token.
type client struct {
	e      *env
	cookie *http.Cookie
	csrf   string
	ip     string
}

func (e *env) client() *client { return &client{e: e, ip: "192.0.2.10"} }

type reqOpt func(*http.Request)

func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (c *client) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	c.e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.e.t.Fatalf("encode body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = c.ip + ":40000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set(csrfHeader, c.csrf)
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	c.e.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == cookieName || ck.Name == secureCookieName {
			if ck.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	if rec.Code < 300 && rec.Header().Get("Content-Type") == "application/json" {
		var s oapi.Session
		if json.Unmarshal(rec.Body.Bytes(), &s) == nil && s.CsrfToken != "" {
			c.csrf = s.CsrfToken
		}
	}
	return rec
}

func (e *env) register(email, slug string) (*client, oapi.Session) {
	e.t.Helper()
	c := e.client()
	rec := c.do(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"email": email, "password": "correct horse", "name": "User " + slug,
		"workspace_name": "WS " + slug, "workspace_slug": slug,
	})
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("register %s: %d %s", email, rec.Code, rec.Body)
	}
	return c, decode[oapi.Session](e.t, rec)
}

func (e *env) login(email string) (*client, oapi.Session) {
	e.t.Helper()
	c := e.client()
	rec := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "password": "correct horse"})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", email, rec.Code, rec.Body)
	}
	return c, decode[oapi.Session](e.t, rec)
}

// ownerExec changes data behind the application's back (fixtures).
func (e *env) ownerExec(sql string, args ...any) {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		e.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		e.t.Fatalf("exec %q: %v", sql, err)
	}
}

func (e *env) addMember(ws, user uuid.UUID, role string) {
	e.t.Helper()
	if err := e.tenancy.AddMember(context.Background(), ws, user, role); err != nil {
		e.t.Fatalf("add member: %v", err)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T from %d %s: %v", v, rec.Code, rec.Body, err)
	}
	return v
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("want %d problem, got %d %q %s", status, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if p := decode[Problem](t, rec); p.Code != code {
		t.Fatalf("want code %q, got %+v", code, p)
	}
}
