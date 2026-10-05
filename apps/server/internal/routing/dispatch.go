package routing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing/store"
)

// DispatchBatch is how many conversations one run assigns; a full batch
// queues the next run.
const DispatchBatch = 50

// DispatchArgs is the job that distributes a workspace's queue.
type DispatchArgs struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
}

// Kind implements river.JobArgs.
func (DispatchArgs) Kind() string { return "routing.dispatch" }

// dispatchLater queues routing.dispatch in tx. Every change queues its
// own run, without uniqueness: a run already going may have read the queue
// before tx commits, and River's unique jobs would drop the new one while
// another runs. A run with nothing to do is one short transaction.
func (s *Service) dispatchLater(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) error {
	if s.jobs == nil {
		return nil
	}
	_, err := s.jobs.InsertTx(ctx, tx, DispatchArgs{WorkspaceID: workspaceID}, nil)
	if err != nil {
		return fmt.Errorf("queue dispatch: %w", err)
	}
	return nil
}

// ErrBusy means another run distributes the workspace's queue right now.
var ErrBusy = errors.New("routing: another dispatch is running")

// Dispatch gives the head of the queue to the available members in turn
// (docs/02 §9.3). One run per workspace at a time (an advisory lock); a
// second one gets ErrBusy and is retried after the first, so whatever the
// first could not see yet is not left waiting.
func (s *Service) Dispatch(ctx context.Context, convs *conversations.Service, workspaceID uuid.UUID) (int, error) {
	var assigned int
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var locked bool
		if err := tx.QueryRow(ctx,
			"SELECT pg_try_advisory_xact_lock(hashtextextended('routing.dispatch:' || $1::text, 0))", workspaceID,
		).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return ErrBusy
		}
		q := store.New(tx)
		mode, err := q.GetMode(ctx, workspaceID)
		if err != nil || Mode(mode) != ModeRoundRobin {
			return err
		}
		users, err := q.LockAvailable(ctx, store.LockAvailableParams{WorkspaceID: workspaceID, SeenAfter: s.seenAfter()})
		if err != nil || len(users) == 0 {
			return err
		}
		got, err := convs.AssignQueued(ctx, tx, workspaceID, users, DispatchBatch)
		if err != nil {
			return err
		}
		// The turn order follows the order of the assignments: whoever got
		// the last one goes to the end.
		now := s.now()
		for i, a := range got {
			if err := q.SetLastAssigned(ctx, store.SetLastAssignedParams{
				AssignedAt: now.Add(time.Duration(i) * time.Microsecond), WorkspaceID: workspaceID, UserID: a.UserID,
			}); err != nil {
				return err
			}
		}
		assigned = len(got)
		if assigned == DispatchBatch {
			return s.dispatchLater(ctx, tx, workspaceID)
		}
		return nil
	})
	if errors.Is(err, ErrBusy) {
		return 0, ErrBusy
	}
	if err != nil {
		return 0, fmt.Errorf("dispatch: %w", err)
	}
	return assigned, nil
}

// DispatchWorker runs routing.dispatch.
type DispatchWorker struct {
	river.WorkerDefaults[DispatchArgs]
	routing *Service
	convs   *conversations.Service
}

// Work implements river.Worker.
func (w *DispatchWorker) Work(ctx context.Context, job *river.Job[DispatchArgs]) error {
	_, err := w.routing.Dispatch(ctx, w.convs, job.Args.WorkspaceID)
	if errors.Is(err, ErrBusy) {
		return river.JobSnooze(time.Second)
	}
	return err
}

// Timeout bounds one run: it only reads and writes rows.
func (w *DispatchWorker) Timeout(*river.Job[DispatchArgs]) time.Duration { return 30 * time.Second }

// AddWorkers registers the router's job. convs is the conversations
// module the router assigns through.
func (s *Service) AddWorkers(workers *river.Workers, convs *conversations.Service) {
	river.AddWorker(workers, &DispatchWorker{routing: s, convs: convs})
}
