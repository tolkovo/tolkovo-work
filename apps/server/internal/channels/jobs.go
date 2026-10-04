package channels

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
)

// PruneLoginsArgs is the periodic job that deletes personal-account logins
// left unfinished: their sealed secret holds a phone number.
type PruneLoginsArgs struct{}

// Kind implements river.JobArgs.
func (PruneLoginsArgs) Kind() string { return "channels.prune_logins" }

// PruneLoginsWorker runs PruneStaleLogins on the system pool.
type PruneLoginsWorker struct {
	river.WorkerDefaults[PruneLoginsArgs]
	pool *pgxpool.Pool
}

// NewPruneLoginsWorker builds the worker; pool must connect as tolkovo_system.
func NewPruneLoginsWorker(pool *pgxpool.Pool) *PruneLoginsWorker {
	return &PruneLoginsWorker{pool: pool}
}

// Work implements river.Worker.
func (w *PruneLoginsWorker) Work(ctx context.Context, _ *river.Job[PruneLoginsArgs]) error {
	_, err := PruneStaleLogins(ctx, w.pool, time.Now().UTC())
	return err
}

// PruneLoginsPeriodic schedules pruning every 5 minutes and once at start.
func PruneLoginsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(5*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return PruneLoginsArgs{}, nil },
		&river.PeriodicJobOpts{ID: "channels.prune_logins", RunOnStart: true},
	)
}

// PruneStaleLogins deletes personal-account logins left unfinished for
// longer than LoginTTL in every workspace; their secrets go by cascade. It
// runs with the system role (policy system_prune_pending).
func PruneStaleLogins(ctx context.Context, sys store.DBTX, now time.Time) (int64, error) {
	n, err := store.New(sys).PruneStaleLogins(ctx, now.Add(-LoginTTL))
	if err != nil {
		return 0, fmt.Errorf("channels: prune logins: %w", err)
	}
	return n, nil
}
