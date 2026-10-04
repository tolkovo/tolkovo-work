package jobs_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type echoArgs struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
}

func (echoArgs) Kind() string { return "test.echo" }

type echoWorker struct {
	river.WorkerDefaults[echoArgs]
	got chan uuid.UUID
}

func (w *echoWorker) Work(_ context.Context, job *river.Job[echoArgs]) error {
	w.got <- job.Args.WorkspaceID
	return nil
}

// A job inserted in a tenant transaction exists only if that transaction
// commits (ADR-0004), and the application role can run the queue.
func TestInsertTx_FollowsTheBusinessTransaction(t *testing.T) {
	d := dbtest.New(t)
	app := d.OpenApp(t)
	ctx := context.Background()

	w := &echoWorker{got: make(chan uuid.UUID, 4)}
	workers := river.NewWorkers()
	river.AddWorker(workers, w)
	client, err := jobs.New(app.Pool(), jobs.Options{Log: quiet, Workers: workers, StopTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = client.Stop(ctx) }()

	rolledBack, committed := uuid.New(), uuid.New()
	errRollback := errors.New("rollback")
	err = app.InTenantTx(ctx, rolledBack, func(tx pgx.Tx) error {
		if _, err := client.InsertTx(ctx, tx, echoArgs{WorkspaceID: rolledBack}, nil); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("want rollback, got %v", err)
	}
	if err := app.InTenantTx(ctx, committed, func(tx pgx.Tx) error {
		_, err := client.InsertTx(ctx, tx, echoArgs{WorkspaceID: committed}, nil)
		return err
	}); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	select {
	case ws := <-w.got:
		if ws != committed {
			t.Fatalf("worker ran the rolled-back job for %s", ws)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("committed job did not run")
	}
	select {
	case ws := <-w.got:
		t.Fatalf("unexpected second job for %s", ws)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestNew_PeriodicJobsNeedWorkers(t *testing.T) {
	_, err := jobs.New(nil, jobs.Options{Log: quiet, Periodic: []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return echoArgs{}, nil
		}, nil),
	}})
	if err == nil {
		t.Fatal("want an error for periodic jobs on an insert-only client")
	}
}
