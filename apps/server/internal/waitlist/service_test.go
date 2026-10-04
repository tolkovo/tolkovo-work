package waitlist_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

const site = "https://tolkovo.test"

// env is the module on a fresh database (application role) with a real
// insert-only River client, so queued jobs land in river.river_job.
type env struct {
	t       *testing.T
	app     *db.DB
	svc     *waitlist.Service
	ownerDB string
	sender  *mailtest.Sender
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	rc, prefix := rdbtest.New(t)
	pool, err := pgxpool.New(context.Background(), d.AppURL)
	if err != nil {
		t.Fatalf("river pool: %v", err)
	}
	t.Cleanup(pool.Close)
	client, err := jobs.New(pool, jobs.Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("jobs.New: %v", err)
	}
	return &env{
		t: t, app: app, ownerDB: d.OwnerURL, sender: &mailtest.Sender{},
		svc: waitlist.New(app, client, rc, waitlist.Options{KeyPrefix: prefix}),
	}
}

func (e *env) join(email string, opts ...func(*waitlist.JoinInput)) {
	e.t.Helper()
	in := waitlist.JoinInput{Email: email, Locale: "ru", Consent: true, Channels: []string{"telegram", "web"}}
	for _, o := range opts {
		o(&in)
	}
	if err := e.svc.Join(context.Background(), in, "192.0.2.1"); err != nil {
		e.t.Fatalf("join %s: %v", email, err)
	}
}

// owner runs fn with a table-owner connection (fixtures and checks).
func (e *env) owner(fn func(ctx context.Context, conn *pgx.Conn)) {
	e.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, e.ownerDB)
	if err != nil {
		e.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	fn(ctx, conn)
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	e.owner(func(ctx context.Context, conn *pgx.Conn) {
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			e.t.Fatalf("exec %q: %v", sql, err)
		}
	})
}

type entryRow struct {
	ID       uuid.UUID
	Status   string
	Locale   string
	Channels []string
	Sends    int
	Raw      string
}

func (e *env) entry(email string) (entryRow, bool) {
	e.t.Helper()
	var r entryRow
	found := true
	e.owner(func(ctx context.Context, conn *pgx.Conn) {
		err := conn.QueryRow(ctx, `SELECT id, status, locale, channels, send_count, w::text
			FROM waitlist_entries w WHERE email = $1`, email).
			Scan(&r.ID, &r.Status, &r.Locale, &r.Channels, &r.Sends, &r.Raw)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return
		}
		if err != nil {
			e.t.Fatalf("select entry: %v", err)
		}
	})
	return r, found
}

// queued returns the args of queued confirmation jobs.
func (e *env) queued() []string {
	e.t.Helper()
	var args []string
	e.owner(func(ctx context.Context, conn *pgx.Conn) {
		rows, err := conn.Query(ctx, `SELECT args::text FROM river.river_job WHERE kind = 'waitlist.send_confirmation' ORDER BY id`)
		if err != nil {
			e.t.Fatalf("select jobs: %v", err)
		}
		args, err = pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			e.t.Fatalf("collect jobs: %v", err)
		}
	})
	return args
}

func (e *env) worker(now func() time.Time) *waitlist.SendWorker {
	e.t.Helper()
	w, err := waitlist.NewSendWorker(e.app, e.sender, waitlist.SendOptions{
		SiteURL: site, TokenTTL: 48 * time.Hour, PendingRetention: 7 * 24 * time.Hour, Now: now,
	})
	if err != nil {
		e.t.Fatalf("NewSendWorker: %v", err)
	}
	return w
}

// send runs the confirmation job for the entry and returns the links' tokens.
func (e *env) send(id uuid.UUID, now func() time.Time) (confirm, unsubscribe string) {
	e.t.Helper()
	before := len(e.sender.Sent())
	job := &river.Job[waitlist.SendConfirmationArgs]{JobRow: &rivertype.JobRow{}, Args: waitlist.SendConfirmationArgs{EntryID: id}}
	if err := e.worker(now).Work(context.Background(), job); err != nil {
		e.t.Fatalf("work: %v", err)
	}
	sent := e.sender.Sent()
	if len(sent) != before+1 {
		e.t.Fatalf("want one more email, got %d -> %d", before, len(sent))
	}
	text := sent[len(sent)-1].Text
	return tokenAfter(e.t, text, "/waitlist/confirm/#"), tokenAfter(e.t, text, "/waitlist/unsubscribe/#")
}

func tokenAfter(t *testing.T, text, marker string) string {
	t.Helper()
	_, rest, ok := strings.Cut(text, marker)
	if !ok {
		t.Fatalf("no %q link in:\n%s", marker, text)
	}
	token, _, _ := strings.Cut(rest, "\n")
	return strings.TrimSpace(token)
}

func TestJoin_NewAddressIsPendingWithOneQueuedEmail(t *testing.T) {
	e := newEnv(t)
	e.join("  Anna@Example.TEST ", func(in *waitlist.JoinInput) { in.Channels = []string{"web", "telegram", "web"} })

	r, ok := e.entry("anna@example.test")
	if !ok || r.Status != "pending" || r.Locale != "ru" || strings.Join(r.Channels, ",") != "telegram,web" {
		t.Fatalf("unexpected entry: %+v (found %t)", r, ok)
	}
	jobs := e.queued()
	if len(jobs) != 1 || !strings.Contains(jobs[0], r.ID.String()) {
		t.Fatalf("want one job for %s, got %v", r.ID, jobs)
	}
	if strings.Contains(jobs[0], "anna") {
		t.Fatalf("job args carry the address: %s", jobs[0])
	}

	// A second submission while the email waits updates the answers but does
	// not stack another job; the address is case-insensitive.
	e.join("ANNA@example.test", func(in *waitlist.JoinInput) { in.Locale = "en"; in.Channels = nil })
	r, _ = e.entry("anna@example.test")
	if r.Locale != "en" || len(r.Channels) != 0 {
		t.Fatalf("pending entry should take the latest answers: %+v", r)
	}
	if n := len(e.queued()); n != 1 {
		t.Fatalf("want still one queued job, got %d", n)
	}
}

func TestJoin_ConfirmedEntryIsNotChangedAndGetsNoEmail(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	confirm, _ := e.send(r.ID, time.Now)
	if err := e.svc.Confirm(context.Background(), confirm, "192.0.2.1"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	e.exec(`DELETE FROM river.river_job`)

	e.join("anna@example.test", func(in *waitlist.JoinInput) { in.Locale = "en"; in.Channels = []string{"email"} })
	got, _ := e.entry("anna@example.test")
	if got.Status != "confirmed" || got.Locale != "ru" || strings.Join(got.Channels, ",") != "telegram,web" {
		t.Fatalf("confirmed entry changed: %+v", got)
	}
	if n := len(e.queued()); n != 0 {
		t.Fatalf("confirmed entry got %d new jobs", n)
	}
}

func TestJoin_StopsEmailingAfterMaxSends(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	e.exec(`DELETE FROM river.river_job`)
	e.exec(`UPDATE waitlist_entries SET send_count = $1`, waitlist.MaxSends)
	e.join("anna@example.test")
	if n := len(e.queued()); n != 0 {
		t.Fatalf("want no job after %d sends, got %d", waitlist.MaxSends, n)
	}
}

func TestJoin_RejectsInvalidInput(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		edit func(*waitlist.JoinInput)
	}{
		{"no at", func(in *waitlist.JoinInput) { in.Email = "anna.example.test" }},
		{"display name", func(in *waitlist.JoinInput) { in.Email = "Anna <anna@example.test>" }},
		{"no dot in domain", func(in *waitlist.JoinInput) { in.Email = "anna@localhost" }},
		{"too long", func(in *waitlist.JoinInput) { in.Email = strings.Repeat("a", 250) + "@example.test" }},
		{"locale", func(in *waitlist.JoinInput) { in.Locale = "de" }},
		{"no consent", func(in *waitlist.JoinInput) { in.Consent = false }},
		{"unknown channel", func(in *waitlist.JoinInput) { in.Channels = []string{"fax"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := waitlist.JoinInput{Email: "anna@example.test", Locale: "ru", Consent: true}
			tt.edit(&in)
			if err := e.svc.Join(context.Background(), in, "192.0.2.50"); !errors.Is(err, waitlist.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestJoin_HoneypotStoresNothing(t *testing.T) {
	e := newEnv(t)
	e.join("bot@example.test", func(in *waitlist.JoinInput) { in.Honeypot = "https://spam.example" })
	if _, ok := e.entry("bot@example.test"); ok {
		t.Fatal("honeypot submission was stored")
	}
}

func TestJoin_RateLimitsPerEmailAcrossIPs(t *testing.T) {
	e := newEnv(t)
	in := waitlist.JoinInput{Email: "anna@example.test", Locale: "ru", Consent: true}
	for i := range 3 {
		if err := e.svc.Join(context.Background(), in, "192.0.2."+string(rune('1'+i))); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var limited *waitlist.RateLimitedError
	if err := e.svc.Join(context.Background(), in, "192.0.2.9"); !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatalf("want RateLimitedError, got %v", err)
	}
}

func TestSendWorker_EmailsLinksThatConfirm(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test", func(in *waitlist.JoinInput) { in.Locale = "en" })
	r, _ := e.entry("anna@example.test")
	confirm, unsubscribe := e.send(r.ID, time.Now)

	m := e.sender.Sent()[0]
	if m.To != "anna@example.test" || !strings.Contains(m.Subject, "Tolkovo") {
		t.Fatalf("unexpected message: %+v", m)
	}
	for _, want := range []string{site + "/en/waitlist/confirm/#" + confirm, site + "/en/waitlist/unsubscribe/#" + unsubscribe} {
		if !strings.Contains(m.Text, want) || !strings.Contains(m.HTML, want) {
			t.Fatalf("both bodies must contain %q:\n%s\n%s", want, m.Text, m.HTML)
		}
	}
	if strings.Contains(m.HTML, "<img") || strings.Contains(m.HTML, "src=") {
		t.Fatalf("email must not load images or pixels:\n%s", m.HTML)
	}
	// Only hashes are stored: the row never contains a token.
	row, _ := e.entry("anna@example.test")
	if row.Sends != 1 || strings.Contains(row.Raw, confirm) || strings.Contains(row.Raw, unsubscribe) {
		t.Fatalf("tokens stored in clear or send not counted: %+v", row)
	}

	ctx := context.Background()
	if err := e.svc.Confirm(ctx, confirm, "192.0.2.1"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := e.svc.Confirm(ctx, confirm, "192.0.2.1"); err != nil {
		t.Fatalf("confirming twice with a valid link must succeed: %v", err)
	}
	if row, _ := e.entry("anna@example.test"); row.Status != "confirmed" {
		t.Fatalf("want confirmed, got %s", row.Status)
	}
}

func TestSendWorker_RussianEmailLinksToRootPages(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	confirm, _ := e.send(r.ID, time.Now)
	m := e.sender.Sent()[0]
	if !strings.Contains(m.Text, site+"/waitlist/confirm/#"+confirm) || !strings.Contains(m.Subject, "Подтвердите") {
		t.Fatalf("unexpected ru message: %s\n%s", m.Subject, m.Text)
	}
}

func TestSendWorker_ResendInvalidatesPreviousLinks(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	oldConfirm, oldUnsub := e.send(r.ID, time.Now)
	newConfirm, _ := e.send(r.ID, time.Now)

	ctx := context.Background()
	if err := e.svc.Confirm(ctx, oldConfirm, "192.0.2.1"); !errors.Is(err, waitlist.ErrTokenInvalid) {
		t.Fatalf("old confirm link: want ErrTokenInvalid, got %v", err)
	}
	if err := e.svc.Unsubscribe(ctx, oldUnsub, "192.0.2.1"); !errors.Is(err, waitlist.ErrTokenInvalid) {
		t.Fatalf("old unsubscribe link: want ErrTokenInvalid, got %v", err)
	}
	if err := e.svc.Confirm(ctx, newConfirm, "192.0.2.1"); err != nil {
		t.Fatalf("new link: %v", err)
	}
}

func TestConfirm_RejectsExpiredAndMalformedTokens(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	sentLongAgo := func() time.Time { return time.Now().Add(-49 * time.Hour) }
	expired, _ := e.send(r.ID, sentLongAgo)

	ctx := context.Background()
	for name, token := range map[string]string{
		"expired": expired, "malformed": "not-a-token", "empty": "", "unknown": strings.Repeat("A", 43),
	} {
		if err := e.svc.Confirm(ctx, token, "192.0.2.1"); !errors.Is(err, waitlist.ErrTokenInvalid) {
			t.Errorf("%s: want ErrTokenInvalid, got %v", name, err)
		}
	}
	if row, _ := e.entry("anna@example.test"); row.Status != "pending" {
		t.Fatalf("expired link confirmed the entry: %s", row.Status)
	}
}

func TestUnsubscribe_DeletesTheEntry(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	confirm, unsub := e.send(r.ID, time.Now)
	ctx := context.Background()
	if err := e.svc.Confirm(ctx, confirm, "192.0.2.1"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := e.svc.Unsubscribe(ctx, unsub, "192.0.2.1"); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if _, ok := e.entry("anna@example.test"); ok {
		t.Fatal("entry still exists after unsubscribe")
	}
	if err := e.svc.Unsubscribe(ctx, unsub, "192.0.2.1"); !errors.Is(err, waitlist.ErrTokenInvalid) {
		t.Fatalf("second unsubscribe: want ErrTokenInvalid, got %v", err)
	}
}

func TestSendWorker_SkipsConfirmedAndDeletedEntries(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	e.exec(`UPDATE waitlist_entries SET status = 'confirmed', confirmed_at = now()`)
	w := e.worker(time.Now)
	ctx := context.Background()
	for _, id := range []uuid.UUID{r.ID, uuid.New()} {
		job := &river.Job[waitlist.SendConfirmationArgs]{JobRow: &rivertype.JobRow{}, Args: waitlist.SendConfirmationArgs{EntryID: id}}
		if err := w.Work(ctx, job); err != nil {
			t.Fatalf("work: %v", err)
		}
	}
	if n := len(e.sender.Sent()); n != 0 {
		t.Fatalf("want no email, got %d", n)
	}
}

func TestSendWorker_DeliveryErrorIsRetriedWithoutTheAddress(t *testing.T) {
	e := newEnv(t)
	e.join("anna@example.test")
	r, _ := e.entry("anna@example.test")
	e.sender.SetErr(errors.New("connection refused"))
	job := &river.Job[waitlist.SendConfirmationArgs]{JobRow: &rivertype.JobRow{}, Args: waitlist.SendConfirmationArgs{EntryID: r.ID}}
	err := e.worker(time.Now).Work(context.Background(), job)
	if err == nil || strings.Contains(err.Error(), "anna") {
		t.Fatalf("want an error without the address, got %v", err)
	}
}

func TestPurge_DeletesStalePendingAndForgetsExpiredTokens(t *testing.T) {
	e := newEnv(t)
	for _, email := range []string{"stale@example.test", "fresh@example.test", "kept@example.test"} {
		e.join(email)
	}
	kept, _ := e.entry("kept@example.test")
	confirm, _ := e.send(kept.ID, func() time.Time { return time.Now().Add(-72 * time.Hour) })
	e.exec(`UPDATE waitlist_entries SET status = 'confirmed', confirmed_at = now() WHERE email = 'kept@example.test'`)
	e.exec(`UPDATE waitlist_entries SET updated_at = now() - interval '8 days', created_at = now() - interval '8 days'
		WHERE email IN ('stale@example.test', 'kept@example.test')`)

	deleted, cleared, err := waitlist.Purge(context.Background(), e.app, time.Now(), 7*24*time.Hour)
	if err != nil || deleted != 1 || cleared != 1 {
		t.Fatalf("purge: deleted %d cleared %d err %v", deleted, cleared, err)
	}
	if _, ok := e.entry("stale@example.test"); ok {
		t.Fatal("stale pending entry survived")
	}
	for _, email := range []string{"fresh@example.test", "kept@example.test"} {
		if _, ok := e.entry(email); !ok {
			t.Fatalf("%s was deleted", email)
		}
	}
	var hash []byte
	e.owner(func(ctx context.Context, conn *pgx.Conn) {
		if err := conn.QueryRow(ctx, `SELECT confirm_token_hash FROM waitlist_entries WHERE email = 'kept@example.test'`).Scan(&hash); err != nil {
			t.Fatalf("select: %v", err)
		}
	})
	if hash != nil || confirm == "" {
		t.Fatal("expired confirm token hash was not cleared")
	}
}

// The job args are the only part of the queue that persists for a while
// after the job completes: they must not carry personal data.
func TestSendConfirmationArgs_CarryOnlyTheEntryID(t *testing.T) {
	b, err := json.Marshal(waitlist.SendConfirmationArgs{EntryID: uuid.Nil})
	if err != nil || string(b) != `{"entry_id":"00000000-0000-0000-0000-000000000000"}` {
		t.Fatalf("args: %s %v", b, err)
	}
}
