package waitlist

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/waitlist/store"
)

// purgeBatch caps rows per statement so purging never holds long locks.
const purgeBatch = 1000

// PurgeArgs is the periodic job that deletes unconfirmed entries and
// forgets expired confirmation tokens.
type PurgeArgs struct{}

// Kind implements river.JobArgs.
func (PurgeArgs) Kind() string { return "waitlist.purge" }

// PurgeWorker runs on the worker role. The table is global, not tenant
// data, so the application role does it; no system policy is needed.
type PurgeWorker struct {
	river.WorkerDefaults[PurgeArgs]
	db        *db.DB
	retention time.Duration
	now       func() time.Time
}

// NewPurgeWorker deletes pending entries not touched for retention.
func NewPurgeWorker(database *db.DB, retention time.Duration) *PurgeWorker {
	return &PurgeWorker{db: database, retention: retention, now: time.Now}
}

// Work implements river.Worker.
func (w *PurgeWorker) Work(ctx context.Context, _ *river.Job[PurgeArgs]) error {
	_, _, err := Purge(ctx, w.db, w.now().UTC(), w.retention)
	return err
}

// PurgePeriodic schedules purging every hour and once at leader start.
func PurgePeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return PurgeArgs{}, nil },
		&river.PeriodicJobOpts{ID: "waitlist.purge", RunOnStart: true},
	)
}

// Purge deletes pending entries last submitted before now-retention and
// clears confirmation tokens that expired before now. It returns both counts.
func Purge(ctx context.Context, database *db.DB, now time.Time, retention time.Duration) (deleted, cleared int64, err error) {
	deleted, err = inBatches(ctx, database, func(q *store.Queries) (int64, error) {
		return q.PurgePending(ctx, store.PurgePendingParams{Before: now.Add(-retention), MaxRows: purgeBatch})
	})
	if err != nil {
		return deleted, 0, fmt.Errorf("waitlist: purge pending: %w", err)
	}
	cleared, err = inBatches(ctx, database, func(q *store.Queries) (int64, error) {
		return q.ClearExpiredConfirmTokens(ctx, store.ClearExpiredConfirmTokensParams{Now: now, MaxRows: purgeBatch})
	})
	if err != nil {
		return deleted, cleared, fmt.Errorf("waitlist: clear expired tokens: %w", err)
	}
	return deleted, cleared, nil
}

// inBatches repeats a batch statement, each in its own short transaction,
// until a batch comes back short.
func inBatches(ctx context.Context, database *db.DB, batch func(*store.Queries) (int64, error)) (int64, error) {
	var total int64
	for {
		var n int64
		err := database.InGlobalTx(ctx, func(tx pgx.Tx) error {
			var err error
			n, err = batch(store.New(tx))
			return err
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < purgeBatch {
			return total, nil
		}
	}
}
