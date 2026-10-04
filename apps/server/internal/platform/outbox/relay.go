package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox/store"
)

// relayLockKey is the session advisory lock that elects one relay leader.
// A single publisher keeps Seq equal to the order consumers receive events.
const relayLockKey int64 = 0x746f6c6b6f766f01 // "tolkovo" + 0x01

// ErrNotSystemRole means the relay pool does not connect as a member of
// tolkovo_system. Without that membership RLS shows the relay no events and
// it would silently publish nothing.
var ErrNotSystemRole = errors.New("outbox: the relay must connect as a member of tolkovo_system (TOLKOVO_SYSTEM_DATABASE_URL)")

// Relay publishes unpublished events. Any number of relays may run; one
// holds the leader lock and the others wait for it to go away.
type Relay struct {
	pool *pgxpool.Pool
	pub  Publisher
	log  *slog.Logger

	// PollInterval is the pause after an empty batch. It bounds the extra
	// latency the outbox adds. Default 100ms.
	PollInterval time.Duration
	// RetryInterval is the pause after an error or a lost leader election.
	// Default 1s.
	RetryInterval time.Duration
	// BatchSize caps events per publish. Default 100.
	BatchSize int
}

// NewRelay builds a relay on a pool that connects as a tolkovo_system member.
func NewRelay(pool *pgxpool.Pool, pub Publisher, log *slog.Logger) *Relay {
	return &Relay{
		pool:          pool,
		pub:           pub,
		log:           log.With("component", "outbox_relay"),
		PollInterval:  100 * time.Millisecond,
		RetryInterval: time.Second,
		BatchSize:     100,
	}
}

// CheckRole fails fast when the pool connects as the wrong role.
func (r *Relay) CheckRole(ctx context.Context) error {
	ok, err := store.New(r.pool).IsSystemMember(ctx)
	if err != nil {
		return fmt.Errorf("outbox: check relay role: %w", err)
	}
	if !ok {
		return ErrNotSystemRole
	}
	return nil
}

// Run publishes until ctx is cancelled and then returns nil. Errors are
// logged and retried: a broken database or publisher must not stop the
// process, events wait safely in the table.
func (r *Relay) Run(ctx context.Context) error {
	for {
		err := r.lead(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is the normal way to stop
		}
		if err != nil {
			r.log.Warn("relay interrupted", "err", err)
		}
		if !sleep(ctx, r.RetryInterval) {
			return nil
		}
	}
}

// lead takes the leader lock on a dedicated connection and publishes while
// holding it. Returns nil when another relay is the leader.
func (r *Relay) lead(ctx context.Context) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	var leader bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", relayLockKey).Scan(&leader); err != nil {
		return fmt.Errorf("take leader lock: %w", err)
	}
	if !leader {
		return nil
	}
	defer func() {
		// The connection goes back to the pool, so the session lock must not
		// stay with it. A cancelled ctx would skip the unlock, hence a fresh one.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", relayLockKey); err != nil {
			// Closing the connection releases the lock on the server side.
			_ = conn.Conn().Close(unlockCtx)
		}
	}()
	r.log.Info("relay is the leader")

	q := store.New(conn)
	for {
		n, err := r.publishBatch(ctx, q)
		if err != nil {
			return err
		}
		if n == 0 && !sleep(ctx, r.PollInterval) {
			return nil
		}
	}
}

// publishBatch publishes one batch and returns its size. It runs outside a
// transaction: the leader lock, not row locks, keeps other relays away, so
// no transaction stays open while the publisher does network I/O.
func (r *Relay) publishBatch(ctx context.Context, q *store.Queries) (int, error) {
	rows, err := q.UnpublishedEvents(ctx, int32(r.BatchSize)) //nolint:gosec // G115: BatchSize is a small positive constant
	if err != nil {
		return 0, fmt.Errorf("read events: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	last, err := q.ReserveSeq(ctx, int64(len(rows)))
	if err != nil {
		return 0, fmt.Errorf("reserve seq: %w", err)
	}
	first := last - int64(len(rows)) + 1

	events := make([]Event, len(rows))
	ids := make([]int64, len(rows))
	seqs := make([]int64, len(rows))
	for i, row := range rows {
		seq := first + int64(i)
		events[i] = Event{
			ID:          row.ID,
			Seq:         seq,
			WorkspaceID: row.WorkspaceID,
			Topic:       row.Topic,
			Payload:     row.Payload,
			CreatedAt:   row.CreatedAt,
		}
		ids[i], seqs[i] = row.ID, seq
	}
	if err := r.pub.Publish(ctx, events); err != nil {
		// Seq values of this attempt are skipped; consumers tolerate gaps.
		return 0, fmt.Errorf("publish %d event(s): %w", len(events), err)
	}
	if _, err := q.MarkPublished(ctx, store.MarkPublishedParams{Ids: ids, Seqs: seqs}); err != nil {
		return 0, fmt.Errorf("mark published: %w", err)
	}
	return len(events), nil
}

// LogPublisher logs event metadata at debug level. It stands in until the
// realtime transport is built (docs/adr/0005-realtime-transport.md) and never
// logs payloads, which may reference personal data.
type LogPublisher struct {
	Log *slog.Logger
}

// Publish implements Publisher.
func (p LogPublisher) Publish(ctx context.Context, events []Event) error {
	for _, e := range events {
		p.Log.DebugContext(ctx, "outbox event", "id", e.ID, "seq", e.Seq, "topic", e.Topic, "workspace_id", e.WorkspaceID)
	}
	return nil
}

// sleep waits d or until ctx is done; it reports whether to keep going.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
