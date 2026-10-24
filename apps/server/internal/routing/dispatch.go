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

// RetryLocked is when a run comes back for queued conversations another
// transaction held, such as one taking a customer's message: nothing else
// may queue a run for them.
const RetryLocked = 2 * time.Second

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
	return s.dispatchAt(ctx, tx, workspaceID, nil)
}

func (s *Service) dispatchAt(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, opts *river.InsertOpts) error {
	if s.jobs == nil {
		return nil
	}
	_, err := s.jobs.InsertTx(ctx, tx, DispatchArgs{WorkspaceID: workspaceID}, opts)
	if err != nil {
		return fmt.Errorf("queue dispatch: %w", err)
	}
	return nil
}

// ErrBusy means another run distributes the workspace's queue right now.
var ErrBusy = errors.New("routing: another dispatch is running")

// Dispatch gives the head of the queue to the available members with room,
// in turn or to the least loaded (docs/02 §9.3, ADR-0021). One run per workspace at a time (an advisory lock); a
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
		set, err := q.GetSettings(ctx, workspaceID)
		if err != nil || !auto(set.Mode) {
			return err
		}
		avail, err := q.LockAvailable(ctx, store.LockAvailableParams{Answering: answering, WorkspaceID: workspaceID, SeenAfter: s.seenAfter()})
		if err != nil || len(avail) == 0 {
			return err
		}
		p := &pool{
			turn: make([]uuid.UUID, len(avail)), limit: make(map[uuid.UUID]int, len(avail)),
			least: Mode(set.Mode) == ModeLeastLoaded,
		}
		for i, a := range avail {
			p.turn[i] = a.UserID
			switch {
			case a.MaxChats.Valid:
				p.limit[a.UserID] = int(a.MaxChats.Int16)
			case set.MaxChats.Valid:
				p.limit[a.UserID] = int(set.MaxChats.Int16)
			}
		}
		if p.load, err = convs.Loads(ctx, tx, workspaceID, p.turn); err != nil {
			return err
		}
		got, err := convs.AssignQueued(ctx, tx, workspaceID, p, DispatchBatch)
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
		// What is still queued for those with room was held by another
		// transaction; those without room come back when one frees (Freed).
		room := p.Users()
		if len(room) == 0 {
			return nil
		}
		left, err := convs.HasQueued(ctx, tx, workspaceID, room)
		if err != nil || !left {
			return err
		}
		return s.dispatchAt(ctx, tx, workspaceID, &river.InsertOpts{ScheduledAt: now.Add(RetryLocked)})
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
