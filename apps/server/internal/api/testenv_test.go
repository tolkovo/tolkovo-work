package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations/githubtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/config"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/mail/mailtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
	"github.com/tolkovo/tolkovo/apps/server/internal/widget"
)

// env is the whole HTTP stack on a fresh database (application role, so
// RLS applies) and a unique Redis key prefix.
type env struct {
	t       *testing.T
	h       http.Handler
	tenancy *tenancy.Service
	files   *storage.FS
	ownerDB string // bypasses RLS: test fixtures only
	// mail records the emails of the auth jobs; nil without envOptions.mail.
	mail *mailtest.Sender
	// bots is the fake Bot API behind the channels module.
	bots *channelstest.Bots
	// For the jobs of app reports, which the worker role runs (AP-03).
	db     dbtest.Database
	app    *db.DB
	convs  *conversations.Service
	ingest *ingest.Service
	// rdb and prefix reach the rate counters of the test.
	rdb    *redis.Client
	prefix string
	// github is the fake GitHub behind the integrations module.
	github       *githubtest.GitHub
	integrations *integrations.Service
	widget       *widget.Service
	routing      *routing.Service
	// llm is the fake model provider behind the ai module: it plays both
	// Anthropic and OpenAI.
	llm *fakeLLM
}

// fakeLLM answers each request with the next queued answer; none queued
// is a 500.
type fakeLLM struct {
	mu       sync.Mutex
	answers  []fakeLLMAnswer
	requests int
}

type fakeLLMAnswer struct {
	status  int
	headers map[string]string
	body    string
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	f.requests++
	a := fakeLLMAnswer{status: http.StatusInternalServerError, body: `{}`}
	if len(f.answers) > 0 {
		a, f.answers = f.answers[0], f.answers[1:]
	}
	f.mu.Unlock()
	for k, v := range a.headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

func (f *fakeLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeLLM) answer(a ...fakeLLMAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, a...)
}

type envOptions struct {
	secure  bool
	trusted []string
	proxies []netip.Prefix
	// realtime mounts the WebSocket gateway and runs the outbox relay with
	// the Redis publisher, like role all.
	realtime realtime.Options
	withWS   bool
	// mail runs the River worker with the auth email jobs and a recording
	// sender, like a server with TOLKOVO_SMTP_HOST set.
	mail bool
	// registration is TOLKOVO_REGISTRATION; empty means open.
	registration string
	// widgetScanner checks visitors' files, like TOLKOVO_CLAMAV_ADDR.
	widgetScanner widget.Scanner
}

// panelURL is where test links point.
const panelURL = "http://panel.test"

func newEnv(t *testing.T, o envOptions) *env {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	rc, prefix := rdbtest.New(t)
	ten := tenancy.New(app)
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var sender *mailtest.Sender
	workers := river.NewWorkers()
	if o.mail {
		sender = &mailtest.Sender{}
		if err := auth.AddMailWorkers(workers, auth.MailOptions{
			Tenancy: ten, Redis: rc, Sender: sender, PanelURL: panelURL, KeyPrefix: prefix,
		}); err != nil {
			t.Fatalf("mail workers: %v", err)
		}
	}
	// The queue is platform infrastructure on the application pool, as in cmd.
	jobsClient, err := jobs.New(app.Pool(), jobs.Options{Log: quiet, Workers: workers, StopTimeout: 5 * time.Second}) //nolint:forbidigo // see above
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	if o.mail {
		if err := jobsClient.Start(context.Background()); err != nil {
			t.Fatalf("start jobs: %v", err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = jobsClient.Stop(ctx)
		})
	}
	a, err := auth.New(context.Background(), ten, rc, auth.Options{
		KeyPrefix: prefix, SessionIdleTimeout: time.Hour, SessionMaxAge: 24 * time.Hour,
		HashParams: auth.HashParams{MemoryKiB: 64, Time: 1, Threads: 1}, HashConcurrency: 4,
		Jobs: jobsClient, MailEnabled: o.mail, PanelURL: panelURL, Registration: o.registration,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	files, err := storage.NewFS(storage.FSOptions{
		Root: t.TempDir(), LinkBaseURL: "http://example.test" + FilesPath,
		SigningKey: bytes.Repeat([]byte("k"), storage.MinSigningKeyLen),
	})
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = files.Close() })
	var mailJobs *jobs.Client
	if o.mail {
		mailJobs = jobsClient
	}
	notifs := notifications.New(app, mailJobs)
	convs := conversations.New(app, files)
	convs.SetNotifier(notifs)
	keys, err := crypto.ParseLocalKeys(config.DevMasterKey, 1, "")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	sealer, err := crypto.NewSealer(keys)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	bots := &channelstest.Bots{}
	// Replies are queued as the api role does: an insert-only client.
	inserter, err := jobs.New(app.Pool(), jobs.Options{Log: quiet}) //nolint:forbidigo // see above
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	chans := channels.New(app, channels.Options{
		Sealer: sealer, Bots: bots, Users: &channelstest.Users{}, HookBaseURL: "https://support.example.com",
		Inbox: convs, Files: files, Jobs: inserter, Redis: rc,
	})
	convs.SetDeliverer(chans)
	// The router's jobs are queued as in the api role; tests run Dispatch.
	router := routing.New(app, inserter)
	convs.SetRouter(router)
	reports := ingest.New(app, ingest.Options{Files: files, Keys: chans, Redis: rc, KeyPrefix: prefix, Jobs: inserter, Inbox: convs})
	gh := &githubtest.GitHub{}
	integ := integrations.New(app, integrations.Options{
		Sealer: sealer, GitHub: gh, PublicURL: "https://support.example.com", PanelURL: panelURL,
		Conversations: convs, Jobs: inserter,
	})
	widgetOpts := widget.Options{Channels: chans, Inbox: convs, Presence: router, Redis: rc, Files: files, KeyPrefix: prefix, Log: quiet}
	if o.widgetScanner != nil {
		widgetOpts.Scanner = o.widgetScanner
	}
	widgets := widget.New(app, widgetOpts)
	llm := &fakeLLM{}
	llmSrv := httptest.NewServer(llm)
	t.Cleanup(llmSrv.Close)
	ais := ai.New(app, ai.Options{Sealer: sealer, HTTP: http.DefaultClient, AnthropicURL: llmSrv.URL, OpenAIURL: llmSrv.URL})
	deps := Deps{
		Log: quiet, DB: pinger{}, Version: "test", Files: files,
		Tenancy: ten, Auth: a, Conversations: convs, Notifications: notifs, Channels: chans,
		Ingest: reports, Integrations: integ,
		Widget:        widgets,
		Routing:       router,
		AI:            ais,
		SecureCookies: o.secure, SessionMaxAge: 24 * time.Hour, TrustedOrigins: o.trusted, TrustedProxies: o.proxies,
	}
	if o.withWS {
		if o.realtime.OnPresence == nil {
			o.realtime.OnPresence = router.Seen
		}
		deps.Realtime = startRealtime(t, d, app, rc, prefix, o)
	}
	return &env{
		t: t, h: NewRouter(deps), tenancy: ten, files: files, ownerDB: d.OwnerURL, mail: sender, bots: bots,
		db: d, app: app, convs: convs, ingest: reports, rdb: rc, prefix: prefix, github: gh, integrations: integ,
		widget: widgets, routing: router, llm: llm,
	}
}

// resetRate empties a rate counter (ratelimit keys are prefix + "rl:" + key).
func (e *env) resetRate(key string) {
	e.t.Helper()
	if err := e.rdb.Del(context.Background(), e.prefix+"rl:"+key).Err(); err != nil {
		e.t.Fatalf("reset rate: %v", err)
	}
}

// waitMail waits for the next email to the address and returns it along
// with the token in its link (the URL fragment after path).
func (e *env) waitMail(to, path string) (mail.Message, string) {
	e.t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		sent := e.mail.Sent()
		for i := len(sent) - 1; i >= 0; i-- {
			m := sent[i]
			if m.To != to {
				continue
			}
			prefix := panelURL + path + "#"
			start := strings.Index(m.Text, prefix)
			if start < 0 {
				e.t.Fatalf("email to %s has no %s link:\n%s", to, path, m.Text)
			}
			token := strings.Fields(m.Text[start+len(prefix):])[0]
			e.mail.Forget(to)
			return m, token
		}
	}
	e.t.Fatalf("no email to %s", to)
	return mail.Message{}, ""
}

// startRealtime runs a gateway and the outbox relay publishing to it until
// the test ends.
func startRealtime(t *testing.T, d dbtest.Database, app *db.DB, rc *redis.Client, prefix string, o envOptions) *realtime.Gateway {
	t.Helper()
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ro := o.realtime
	ro.ChannelPrefix, ro.TrustedOrigins = prefix, o.trusted
	gw := realtime.New(app, rc, quiet, ro)
	// The relay is platform infrastructure on the system role, as in cmd.
	relay := outbox.NewRelay(d.OpenSystem(t).Pool(), realtime.NewPublisher(rc, prefix), quiet) //nolint:forbidigo // see above
	relay.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = gw.Run(ctx) }()
	go func() { defer wg.Done(); _ = relay.Run(ctx) }()
	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = gw.Shutdown(sctx)
		cancel()
		wg.Wait()
	})
	for deadline := time.Now().Add(10 * time.Second); !gw.Ready(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("realtime did not subscribe to Redis")
		}
	}
	return gw
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

// ownerScan reads one value behind the application's back.
func (e *env) ownerScan(dst any, sql string, args ...any) {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		e.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := conn.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
		e.t.Fatalf("query %q: %v", sql, err)
	}
}

// ownerCount counts rows behind the application's back.
func (e *env) ownerCount(sql string, args ...any) int {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		e.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", sql, err)
	}
	return n
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
