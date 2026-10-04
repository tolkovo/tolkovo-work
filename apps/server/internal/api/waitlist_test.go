package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail/mailtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/waitlist"
)

const landing = "https://tolkovo.test"

type waitlistEnv struct {
	t       *testing.T
	h       http.Handler
	app     *db.DB
	ownerDB string
	logs    *bytes.Buffer
}

func newWaitlistEnv(t *testing.T) *waitlistEnv {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	rc, prefix := rdbtest.New(t)
	pool, err := pgxpool.New(context.Background(), d.AppURL)
	if err != nil {
		t.Fatalf("river pool: %v", err)
	}
	t.Cleanup(pool.Close)
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
	inserter, err := jobs.New(pool, jobs.Options{Log: quiet})
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	logs := &bytes.Buffer{}
	h := NewRouter(Deps{
		Log: slog.New(slog.NewJSONHandler(logs, nil)), DB: pinger{}, Version: "test",
		Waitlist:        waitlist.New(app, inserter, rc, waitlist.Options{KeyPrefix: prefix}),
		WaitlistOrigins: []string{landing},
	})
	return &waitlistEnv{t: t, h: h, app: app, ownerDB: d.OwnerURL, logs: logs}
}

func (e *waitlistEnv) do(method, path, origin, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.20:40000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func joinBody(email string) string {
	return `{"email":"` + email + `","locale":"ru","consent":true,"channels":["telegram","web"]}`
}

func TestWaitlist_AllowedOriginGetsCORSWithoutCredentials(t *testing.T) {
	e := newWaitlistEnv(t)
	rec := e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("anna@example.test"),
		header("Idempotency-Key", "4b9f6d2e-0000-4000-8000-000000000001"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get("Access-Control-Allow-Origin") != landing || h.Get("Access-Control-Allow-Credentials") != "" ||
		!strings.Contains(h.Get("Vary"), "Origin") || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected headers: %v", h)
	}
	if !strings.Contains(rec.Body.String(), `"status":"pending_confirmation"`) {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
}

func TestWaitlist_OtherOriginIsRejectedOnTheServer(t *testing.T) {
	e := newWaitlistEnv(t)
	for _, origin := range []string{"https://evil.test", "null", landing + ".evil.test"} {
		rec := e.do(http.MethodPost, "/api/v1/waitlist", origin, joinBody("anna@example.test"))
		wantProblem(t, rec, http.StatusForbidden, "cross_origin")
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s: CORS header on a rejected origin", origin)
		}
	}
	var n int
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM waitlist_entries`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected origin stored %d entries (%v)", n, err)
	}
}

func TestWaitlist_Preflight(t *testing.T) {
	e := newWaitlistEnv(t)
	for _, path := range waitlistPaths {
		rec := e.do(http.MethodOptions, path, landing, "",
			header("Access-Control-Request-Method", "POST"),
			header("Access-Control-Request-Headers", "content-type, idempotency-key"))
		h := rec.Header()
		if rec.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != landing ||
			h.Get("Access-Control-Allow-Methods") != "POST" ||
			!strings.Contains(h.Get("Access-Control-Allow-Headers"), "Idempotency-Key") ||
			h.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s: preflight %d %v", path, rec.Code, h)
		}
		denied := e.do(http.MethodOptions, path, "https://evil.test", "", header("Access-Control-Request-Method", "POST"))
		if denied.Code != http.StatusForbidden || denied.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("%s: denied preflight %d %v", path, denied.Code, denied.Header())
		}
	}
}

// The answer must not reveal whether the address is new, pending or
// already confirmed.
func TestWaitlist_SameAnswerForNewPendingAndConfirmed(t *testing.T) {
	e := newWaitlistEnv(t)
	first := e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("anna@example.test"))
	pending := e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("anna@example.test"))

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `UPDATE waitlist_entries SET status = 'confirmed', confirmed_at = now()`); err != nil {
		t.Fatalf("confirm fixture: %v", err)
	}
	confirmed := e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("anna@example.test"))

	for name, rec := range map[string]*httptest.ResponseRecorder{"pending": pending, "confirmed": confirmed} {
		if rec.Code != first.Code || rec.Body.String() != first.Body.String() ||
			rec.Header().Get("Content-Type") != first.Header().Get("Content-Type") {
			t.Fatalf("%s answer differs: %d %s vs %d %s", name, rec.Code, rec.Body, first.Code, first.Body)
		}
	}
}

func TestWaitlist_RejectsInvalidRequests(t *testing.T) {
	e := newWaitlistEnv(t)
	tests := []struct {
		name, body string
		opts       []reqOpt
	}{
		{"bad email", joinBody("not-an-email"), nil},
		{"no consent", `{"email":"anna@example.test","locale":"ru","consent":false}`, nil},
		{"unknown channel", `{"email":"anna@example.test","locale":"ru","consent":true,"channels":["fax"]}`, nil},
		{"unknown field", `{"email":"anna@example.test","locale":"ru","consent":true,"workspace_id":"x"}`, nil},
		{"two objects", joinBody("anna@example.test") + "{}", nil},
		{"form encoded", "email=anna%40example.test", []reqOpt{header("Content-Type", "application/x-www-form-urlencoded")}},
		{"too large", `{"email":"anna@example.test","locale":"ru","consent":true,"website":"` + strings.Repeat("x", 5000) + `"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.do(http.MethodPost, "/api/v1/waitlist", landing, tt.body, tt.opts...)
			wantProblem(t, rec, http.StatusBadRequest, "invalid_request")
			// Errors stay readable by the landing script.
			if rec.Header().Get("Access-Control-Allow-Origin") != landing {
				t.Fatalf("error without CORS header: %v", rec.Header())
			}
		})
	}
}

func TestWaitlist_RateLimitedPerIP(t *testing.T) {
	e := newWaitlistEnv(t)
	var rec *httptest.ResponseRecorder
	for i := range 11 {
		rec = e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("user"+string(rune('a'+i))+"@example.test"))
	}
	wantProblem(t, rec, http.StatusTooManyRequests, "too_many_requests")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
}

func TestWaitlist_ConfirmAndUnsubscribeOverHTTP(t *testing.T) {
	e := newWaitlistEnv(t)
	if rec := e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("anna@example.test")); rec.Code != http.StatusAccepted {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	confirm, unsub := e.sendConfirmation("anna@example.test")

	rec := e.do(http.MethodPost, "/api/v1/waitlist/confirm", landing, `{"token":"`+confirm+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, e.do(http.MethodPost, "/api/v1/waitlist/confirm", landing, `{"token":"`+strings.Repeat("A", 43)+`"}`),
		http.StatusNotFound, "token_invalid")

	if rec := e.do(http.MethodPost, "/api/v1/waitlist/unsubscribe", landing, `{"token":"`+unsub+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unsubscribe: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, e.do(http.MethodPost, "/api/v1/waitlist/unsubscribe", landing, `{"token":"`+unsub+`"}`),
		http.StatusNotFound, "token_invalid")

	// GET must never confirm: mail scanners prefetch links.
	if rec := e.do(http.MethodGet, "/api/v1/waitlist/confirm", landing, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET confirm: want 405, got %d", rec.Code)
	}
}

func TestWaitlist_EmailIsNeverLogged(t *testing.T) {
	e := newWaitlistEnv(t)
	e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("secret.person@example.test"))
	e.do(http.MethodPost, "/api/v1/waitlist", landing, joinBody("bad address@example.test"))
	if e.logs.Len() == 0 || strings.Contains(e.logs.String(), "secret.person") || strings.Contains(e.logs.String(), "bad address") {
		t.Fatalf("logs must have request lines without the address:\n%s", e.logs)
	}
}

// Without the feature flag no waitlist route exists.
func TestWaitlist_NotMountedWhenDisabled(t *testing.T) {
	h := newRouter(pinger{}, io.Discard)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/waitlist", strings.NewReader(joinBody("a@example.test"))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

// Only the three waitlist operations and their preflights are public in
// this group; anything else under /api/v1 needs the panel session.
func TestWaitlist_RoutesAreExactlyTheContract(t *testing.T) {
	e := newWaitlistEnv(t)
	got := map[string]bool{}
	err := chi.Walk(e.h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1/waitlist") {
			got[method+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, p := range waitlistPaths {
		for _, m := range []string{http.MethodPost, http.MethodOptions} {
			if !got[m+" "+p] {
				t.Errorf("missing %s %s", m, p)
			}
			delete(got, m+" "+p)
		}
	}
	if len(got) != 0 {
		t.Fatalf("unexpected waitlist routes: %v", got)
	}
}

// sendConfirmation runs the queued email job with a fake sender and
// returns the tokens from the links.
func (e *waitlistEnv) sendConfirmation(email string) (confirm, unsub string) {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		e.t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var args waitlist.SendConfirmationArgs
	if err := conn.QueryRow(ctx, `SELECT w.id FROM waitlist_entries w WHERE w.email = $1`, email).Scan(&args.EntryID); err != nil {
		e.t.Fatalf("entry: %v", err)
	}
	sender := &mailtest.Sender{}
	w, err := waitlist.NewSendWorker(e.app, sender, waitlist.SendOptions{
		SiteURL: landing, TokenTTL: time.Hour, PendingRetention: 24 * time.Hour,
	})
	if err != nil {
		e.t.Fatalf("worker: %v", err)
	}
	if err := w.Work(ctx, &river.Job[waitlist.SendConfirmationArgs]{JobRow: &rivertype.JobRow{}, Args: args}); err != nil {
		e.t.Fatalf("work: %v", err)
	}
	sent := sender.Sent()
	if len(sent) != 1 {
		e.t.Fatalf("want one email, got %d", len(sent))
	}
	pick := func(marker string) string {
		_, rest, _ := strings.Cut(sent[0].Text, marker)
		tok, _, _ := strings.Cut(rest, "\n")
		return strings.TrimSpace(tok)
	}
	return pick("/waitlist/confirm/#"), pick("/waitlist/unsubscribe/#")
}
