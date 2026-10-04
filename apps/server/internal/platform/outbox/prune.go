package outbox

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox/store"
)

// pruneBatch caps rows per DELETE so pruning never holds long locks.
const pruneBatch = 5000

// PruneArgs is the periodic job that deletes old published events.
type PruneArgs struct{}

// Kind implements river.JobArgs.
func (PruneArgs) Kind() string { return "outbox.prune" }

// PruneWorker deletes events published more than Retention ago. Published
// events are kept for a while so realtime clients can catch up after a
// reconnect (ADR-0005). It runs on the system pool, like the relay.
type PruneWorker struct {
	river.WorkerDefaults[PruneArgs]
	pool      *pgxpool.Pool
	retention time.Duration
}

// NewPruneWorker builds the worker; pool must connect as tolkovo_system.
func NewPruneWorker(pool *pgxpool.Pool, retention time.Duration) *PruneWorker {
	return &PruneWorker{pool: pool, retention: retention}
}

// Work implements river.Worker.
func (w *PruneWorker) Work(ctx context.Context, _ *river.Job[PruneArgs]) error {
	_, err := Prune(ctx, w.pool, time.Now().Add(-w.retention))
	return err
}

// PrunePeriodic schedules pruning every hour and once at leader start.
func PrunePeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return PruneArgs{}, nil },
		&river.PeriodicJobOpts{ID: "outbox.prune", RunOnStart: true},
	)
}

// Prune deletes events published before the cutoff, in batches, and returns
// how many were deleted.
func Prune(ctx context.Context, pool *pgxpool.Pool, before time.Time) (int64, error) {
	q := store.New(pool)
	var total int64
	for {
		n, err := q.PrunePublished(ctx, store.PrunePublishedParams{Before: before, MaxRows: pruneBatch})
		if err != nil {
			return total, fmt.Errorf("outbox: prune: %w", err)
		}
		total += n
		if n < pruneBatch {
			return total, nil
		}
	}
}
