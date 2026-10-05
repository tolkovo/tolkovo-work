package widget

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/widget/store"
)

// PruneSessionsArgs is the periodic job that deletes expired sessions.
type PruneSessionsArgs struct{}

// Kind implements river.JobArgs.
func (PruneSessionsArgs) Kind() string { return "widget.prune_sessions" }

// PruneSessionsWorker runs PruneExpiredSessions on the system pool.
type PruneSessionsWorker struct {
	river.WorkerDefaults[PruneSessionsArgs]
	pool *pgxpool.Pool
}

// NewPruneSessionsWorker builds the worker; pool must connect as
// tolkovo_system.
func NewPruneSessionsWorker(pool *pgxpool.Pool) *PruneSessionsWorker {
	return &PruneSessionsWorker{pool: pool}
}

// Work implements river.Worker.
func (w *PruneSessionsWorker) Work(ctx context.Context, _ *river.Job[PruneSessionsArgs]) error {
	_, err := PruneExpiredSessions(ctx, w.pool)
	return err
}

// PruneSessionsPeriodic schedules pruning every hour and once at start.
func PruneSessionsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return PruneSessionsArgs{}, nil },
		&river.PeriodicJobOpts{ID: "widget.prune_sessions", RunOnStart: true},
	)
}

// PruneExpiredSessions deletes up to 5000 expired sessions in every
// workspace; the next run takes the rest. It runs with the system role
// (policy system_prune_expired).
func PruneExpiredSessions(ctx context.Context, sys store.DBTX) (int64, error) {
	n, err := store.New(sys).PruneExpiredSessions(ctx)
	if err != nil {
		return 0, fmt.Errorf("widget: prune sessions: %w", err)
	}
	return n, nil
}
