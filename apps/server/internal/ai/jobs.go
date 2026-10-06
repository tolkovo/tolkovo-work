package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
)

// TextRetention is how long ai_calls keeps the masked request and answer
// (docs/03 §10.2); the rows stay for the budget. Policy system_clear_old in
// migration 00027 has the same interval.
const TextRetention = 30 * 24 * time.Hour

// ClearTextsArgs is the periodic job that clears old texts.
type ClearTextsArgs struct{}

// Kind implements river.JobArgs.
func (ClearTextsArgs) Kind() string { return "ai.clear_texts" }

// ClearTextsWorker runs ClearOldTexts on the system pool.
type ClearTextsWorker struct {
	river.WorkerDefaults[ClearTextsArgs]
	pool *pgxpool.Pool
}

// NewClearTextsWorker builds the worker; pool must connect as tolkovo_system.
func NewClearTextsWorker(pool *pgxpool.Pool) *ClearTextsWorker {
	return &ClearTextsWorker{pool: pool}
}

// Work implements river.Worker.
func (w *ClearTextsWorker) Work(ctx context.Context, _ *river.Job[ClearTextsArgs]) error {
	_, err := ClearOldTexts(ctx, w.pool)
	return err
}

// ClearTextsPeriodic schedules the cleanup every hour and once at start.
func ClearTextsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(time.Hour),
		func() (river.JobArgs, *river.InsertOpts) { return ClearTextsArgs{}, nil },
		&river.PeriodicJobOpts{ID: "ai.clear_texts", RunOnStart: true},
	)
}

// ClearOldTexts clears the texts of up to 5000 calls older than
// TextRetention in every workspace; the next run takes the rest. It runs
// with the system role.
func ClearOldTexts(ctx context.Context, sys store.DBTX) (int64, error) {
	n, err := store.New(sys).ClearOldTexts(ctx)
	if err != nil {
		return 0, fmt.Errorf("ai: clear old texts: %w", err)
	}
	return n, nil
}
