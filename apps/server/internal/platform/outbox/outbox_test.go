package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestAdd_RejectsForeignWorkspace(t *testing.T) {
	d := dbtest.New(t)
	app := d.OpenApp(t)
	a, b := newWorkspace(t, d), newWorkspace(t, d)
	ctx := context.Background()

	if err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
		return outbox.Add(ctx, tx, a, "test.thing.created", map[string]string{"k": "v"})
	}); err != nil {
		t.Fatalf("add to own workspace: %v", err)
	}
	err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
		return outbox.Add(ctx, tx, b, "test.thing.created", nil)
	})
	if err == nil {
		t.Fatal("RLS must reject an event for another workspace")
	}
}

func TestAdd_RejectsInvalidTopic(t *testing.T) {
	d := dbtest.New(t)
	app := d.OpenApp(t)
	a := newWorkspace(t, d)
	ctx := context.Background()

	for _, topic := range []string{"", "nodot", "Upper.case", "a.b c", "a..b"} {
		err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
			return outbox.Add(ctx, tx, a, topic, nil)
		})
		if err == nil {
			t.Errorf("topic %q must be rejected", topic)
		}
	}
}

func TestAppRole_ReadsOnlyOwnWorkspaceAndCannotChangeEvents(t *testing.T) {
	d := dbtest.New(t)
	app := d.OpenApp(t)
	a, b := newWorkspace(t, d), newWorkspace(t, d)
	ctx := context.Background()
	add(t, app, a, "test.thing.created")
	add(t, app, b, "test.thing.created")

	// RLS is the second line: even a query without the workspace filter
	// sees only the transaction's workspace (docs/00 §11.3).
	var n int
	if err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("want 1 own event visible, got %d (%v)", n, err)
	}
	for _, stmt := range []string{
		"UPDATE outbox_events SET topic = 'test.thing.changed'",
		"DELETE FROM outbox_events",
		"UPDATE outbox_watermark SET pruned_seq = 0",
	} {
		err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, stmt)
			return err
		})
		if err == nil {
			t.Errorf("the application role must not run %q", stmt)
		}
	}
}

func TestSince_ReturnsPublishedEventsOfOneWorkspaceAfterSeq(t *testing.T) {
	d := dbtest.New(t)
	app, sys := d.OpenApp(t), d.OpenSystem(t)
	a, b := newWorkspace(t, d), newWorkspace(t, d)
	ctx := context.Background()
	add(t, app, a, "conversation.created")
	add(t, app, b, "conversation.created")
	add(t, app, a, "contact.created")
	add(t, app, a, "conversation.assigned")

	pub := &recorder{}
	stop := runRelay(t, sys, pub)
	all := pub.waitFor(t, 4)
	waitMarked(t, d)
	stop()
	add(t, app, a, "conversation.updated") // unpublished: never returned

	var got []outbox.Event
	var latest int64
	if err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
		var err error
		if got, err = outbox.Since(ctx, tx, a, all[0].Seq, []string{"conversation.%"}, 100); err != nil {
			return err
		}
		latest, err = outbox.LatestSeq(ctx, tx, a)
		return err
	}); err != nil {
		t.Fatalf("catch-up: %v", err)
	}
	if len(got) != 1 || got[0].Topic != "conversation.assigned" || got[0].Seq != all[3].Seq || got[0].WorkspaceID != a {
		t.Fatalf("want only the later conversation event of workspace A, got %+v", got)
	}
	if latest != all[3].Seq {
		t.Fatalf("latest seq: want %d, got %d", all[3].Seq, latest)
	}

	// Workspace B asking with A's id sees nothing: RLS hides A's rows.
	if err := app.InTenantTx(ctx, b, func(tx pgx.Tx) error {
		var err error
		got, err = outbox.Since(ctx, tx, a, 0, []string{"%"}, 100)
		return err
	}); err != nil || len(got) != 0 {
		t.Fatalf("foreign workspace must see no events, got %d (%v)", len(got), err)
	}
}

func TestPrune_RaisesWatermarkToHighestDeletedSeq(t *testing.T) {
	d := dbtest.New(t)
	app, sys := d.OpenApp(t), d.OpenSystem(t)
	a := newWorkspace(t, d)
	ctx := context.Background()
	for range 3 {
		add(t, app, a, "conversation.created")
	}
	pub := &recorder{}
	stop := runRelay(t, sys, pub)
	all := pub.waitFor(t, 3)
	waitMarked(t, d)
	stop()

	if _, err := ownerConn(t, d).Exec(ctx, `UPDATE outbox_events SET published_at = now() - interval '48 hours'
		WHERE seq <= $1`, all[1].Seq); err != nil {
		t.Fatalf("age events: %v", err)
	}
	n, err := outbox.Prune(ctx, sys.Pool(), time.Now().Add(-24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("want 2 pruned, got %d (%v)", n, err)
	}
	var pruned, latest int64
	if err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
		var err error
		if pruned, err = outbox.PrunedSeq(ctx, tx); err != nil {
			return err
		}
		latest, err = outbox.LatestSeq(ctx, tx, a)
		return err
	}); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if pruned != all[1].Seq || latest != all[2].Seq {
		t.Fatalf("want watermark %d and latest %d, got %d and %d", all[1].Seq, all[2].Seq, pruned, latest)
	}
	// Pruning nothing keeps the watermark.
	if n, err := outbox.Prune(ctx, sys.Pool(), time.Now().Add(-24*time.Hour)); err != nil || n != 0 {
		t.Fatalf("second prune: %d (%v)", n, err)
	}
}

func TestRelay_PublishesCommittedEventsInOrder(t *testing.T) {
	d := dbtest.New(t)
	app, sys := d.OpenApp(t), d.OpenSystem(t)
	a, b := newWorkspace(t, d), newWorkspace(t, d)
	ctx := context.Background()

	add(t, app, a, "test.thing.created")
	add(t, app, b, "test.thing.created")
	errRollback := errors.New("rollback")
	if err := app.InTenantTx(ctx, a, func(tx pgx.Tx) error {
		if err := outbox.Add(ctx, tx, a, "test.thing.rolled_back", nil); err != nil {
			return err
		}
		return errRollback
	}); !errors.Is(err, errRollback) {
		t.Fatalf("want rollback, got %v", err)
	}
	add(t, app, a, "test.thing.updated")

	pub := &recorder{}
	stop := runRelay(t, sys, pub)
	got := pub.waitFor(t, 3)
	waitMarked(t, d)
	stop()

	wantTopics := []string{"test.thing.created", "test.thing.created", "test.thing.updated"}
	wantWS := []uuid.UUID{a, b, a}
	for i, e := range got {
		if e.Topic != wantTopics[i] || e.WorkspaceID != wantWS[i] {
			t.Errorf("event %d: got %s/%s, want %s/%s", i, e.Topic, e.WorkspaceID, wantTopics[i], wantWS[i])
		}
		if i > 0 && (e.ID <= got[i-1].ID || e.Seq != got[i-1].Seq+1) {
			t.Errorf("event %d out of order: id %d seq %d after id %d seq %d", i, e.ID, e.Seq, got[i-1].ID, got[i-1].Seq)
		}
	}
	if n := countUnpublished(t, d); n != 0 {
		t.Errorf("want every event marked published, %d left", n)
	}
}

func TestRelay_RefusesApplicationRole(t *testing.T) {
	d := dbtest.New(t)
	app := d.OpenApp(t)
	r := outbox.NewRelay(app.Pool(), &recorder{}, quiet)
	if err := r.CheckRole(context.Background()); !errors.Is(err, outbox.ErrNotSystemRole) {
		t.Fatalf("want ErrNotSystemRole, got %v", err)
	}
	sys := d.OpenSystem(t)
	if err := outbox.NewRelay(sys.Pool(), &recorder{}, quiet).CheckRole(context.Background()); err != nil {
		t.Fatalf("system role must pass: %v", err)
	}
}

func TestRelay_OneLeaderPublishesEachEventOnce(t *testing.T) {
	d := dbtest.New(t)
	app := d.OpenApp(t)
	a := newWorkspace(t, d)
	const total = 50
	for range total {
		add(t, app, a, "test.thing.created")
	}

	pub := &recorder{}
	stop1 := runRelay(t, d.OpenSystem(t), pub)
	stop2 := runRelay(t, d.OpenSystem(t), pub)
	got := pub.waitFor(t, total)
	stop1()
	stop2()

	seen := map[int64]bool{}
	for i, e := range got {
		if seen[e.ID] {
			t.Fatalf("event %d published twice", e.ID)
		}
		seen[e.ID] = true
		if i > 0 && e.Seq <= got[i-1].Seq {
			t.Fatalf("seq must grow in delivery order: %d after %d", e.Seq, got[i-1].Seq)
		}
	}
}

func TestRelay_RetriesAfterPublisherError(t *testing.T) {
	d := dbtest.New(t)
	app, sys := d.OpenApp(t), d.OpenSystem(t)
	a := newWorkspace(t, d)
	add(t, app, a, "test.thing.created")

	pub := &recorder{failures: 2}
	stop := runRelay(t, sys, pub)
	got := pub.waitFor(t, 1)
	waitMarked(t, d)
	stop()

	if got[0].Topic != "test.thing.created" {
		t.Fatalf("unexpected event %+v", got[0])
	}
	if n := countUnpublished(t, d); n != 0 {
		t.Fatalf("want the event marked published after retries, %d left", n)
	}
}

func TestPrune_RunsAsPeriodicRiverJob(t *testing.T) {
	d := dbtest.New(t)
	app, sys := d.OpenApp(t), d.OpenSystem(t)
	a := newWorkspace(t, d)
	ctx := context.Background()
	for range 3 {
		add(t, app, a, "test.thing.created")
	}
	pub := &recorder{}
	stop := runRelay(t, sys, pub)
	pub.waitFor(t, 3)
	waitMarked(t, d)
	stop()
	add(t, app, a, "test.thing.pending") // unpublished: must survive

	// Age two of the published events past the retention.
	owner := ownerConn(t, d)
	if _, err := owner.Exec(ctx, `UPDATE outbox_events SET published_at = now() - interval '48 hours'
		WHERE id IN (SELECT id FROM outbox_events WHERE published_at IS NOT NULL ORDER BY id LIMIT 2)`); err != nil {
		t.Fatalf("age events: %v", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, outbox.NewPruneWorker(sys.Pool(), 24*time.Hour))
	client, err := jobs.New(app.Pool(), jobs.Options{
		Log: quiet, Workers: workers, Periodic: []*river.PeriodicJob{outbox.PrunePeriodic()},
	})
	if err != nil {
		t.Fatalf("jobs client: %v", err)
	}
	done, cancel := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start river: %v", err)
	}
	defer func() { _ = client.Stop(ctx) }()

	select {
	case ev := <-done:
		if ev.Kind != river.EventKindJobCompleted {
			t.Fatalf("prune job failed: %v", ev.Job.Errors)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("prune job did not run")
	}

	var published, pending int
	if err := owner.QueryRow(ctx, `SELECT count(*) FILTER (WHERE published_at IS NOT NULL),
		count(*) FILTER (WHERE published_at IS NULL) FROM outbox_events`).Scan(&published, &pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if published != 1 || pending != 1 {
		t.Fatalf("want 1 recent published and 1 pending event left, got %d and %d", published, pending)
	}
}

// --- helpers ---

type recorder struct {
	mu       sync.Mutex
	events   []outbox.Event
	failures int
}

func (r *recorder) Publish(_ context.Context, events []outbox.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		r.failures--
		return errors.New("publisher unavailable")
	}
	r.events = append(r.events, events...)
	return nil
}

func (r *recorder) waitFor(t *testing.T, n int) []outbox.Event {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.events) >= n {
			got := append([]outbox.Event(nil), r.events...)
			r.mu.Unlock()
			if len(got) > n {
				t.Fatalf("want %d events, got %d", n, len(got))
			}
			return got
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events", n)
	return nil
}

func runRelay(t *testing.T, sys *db.DB, pub outbox.Publisher) (stop func()) {
	t.Helper()
	r := outbox.NewRelay(sys.Pool(), pub, quiet)
	r.PollInterval = 10 * time.Millisecond
	r.RetryInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("relay: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func add(t *testing.T, app *db.DB, ws uuid.UUID, topic string) {
	t.Helper()
	ctx := context.Background()
	if err := app.InTenantTx(ctx, ws, func(tx pgx.Tx) error {
		return outbox.Add(ctx, tx, ws, topic, map[string]any{"n": 1})
	}); err != nil {
		t.Fatalf("add event: %v", err)
	}
}

func newWorkspace(t *testing.T, d dbtest.Database) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := ownerConn(t, d).Exec(context.Background(),
		"INSERT INTO workspaces (id, slug, name) VALUES ($1, $2, 'Test')", id, "ws-"+id.String()[24:])
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	return id
}

// waitMarked waits until the relay has marked every event published. The
// recorder hears of a batch before the relay writes its seq and
// published_at; stopping the relay in between cancels that write.
func waitMarked(t *testing.T, d dbtest.Database) {
	t.Helper()
	conn := ownerConn(t, d)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := conn.QueryRow(context.Background(),
			"SELECT count(*) FROM outbox_events WHERE published_at IS NULL").Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the relay to mark the events published")
}

func countUnpublished(t *testing.T, d dbtest.Database) int {
	t.Helper()
	var n int
	if err := ownerConn(t, d).QueryRow(context.Background(),
		"SELECT count(*) FROM outbox_events WHERE published_at IS NULL").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ownerConn connects as the table owner for fixtures; it bypasses RLS.
func ownerConn(t *testing.T, d dbtest.Database) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d.OwnerURL)
	if err != nil {
		t.Fatalf("connect owner: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	return conn
}
