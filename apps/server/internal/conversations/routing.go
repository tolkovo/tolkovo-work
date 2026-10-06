package conversations

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
)

// Assignment is a conversation the router gave to a member.
type Assignment struct {
	ConversationID uuid.UUID
	UserID         uuid.UUID
}

// HasQueued reports whether the queue still holds conversations, those
// another transaction holds included, in the router's transaction tx.
func (s *Service) HasQueued(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) (bool, error) {
	ok, err := store.New(tx).HasQueued(ctx, workspaceID)
	if err != nil {
		return false, wrap(err, "has queued")
	}
	return ok, nil
}

// AssignQueued gives up to limit conversations from the head of the queue
// to assignees in turn: the first to assignees[0], the second to
// assignees[1], and around again. It runs in the router's tenant
// transaction tx (docs/02 §9.3) and records each assignment as a change by
// the system: events, the system line and the reminder. Conversations
// another transaction holds are left for the next round.
func (s *Service) AssignQueued(
	ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, assignees []uuid.UUID, limit int,
) ([]Assignment, error) {
	if len(assignees) == 0 || limit <= 0 {
		return nil, nil
	}
	q := store.New(tx)
	rows, err := q.LockQueue(ctx, store.LockQueueParams{WorkspaceID: workspaceID, Lim: pageSize(limit)})
	if err != nil {
		return nil, wrap(err, "lock queue")
	}
	out := make([]Assignment, 0, len(rows))
	for i, row := range rows {
		user := assignees[i%len(assignees)]
		cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
		next, err := assign(cur, &user, s.now())
		if err != nil {
			return nil, err
		}
		n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
			WorkspaceID: workspaceID, ID: row.ID, Version: row.Version, Status: string(next.Status),
			AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
		})
		if err != nil {
			return nil, wrap(err, "assign queued")
		}
		if n == 0 {
			return nil, fmt.Errorf("assign queued: %w", ErrConflict) // the row is locked: cannot happen
		}
		if err := s.recordChange(ctx, q, tx, row, uuid.Nil, cur, next); err != nil {
			return nil, wrap(err, "assign queued")
		}
		out = append(out, Assignment{ConversationID: row.ID, UserID: user})
	}
	return out, nil
}
