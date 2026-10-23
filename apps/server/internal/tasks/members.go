package tasks

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// HandOver gives the open tasks of a member who leaves the team to to, or
// to nobody when to is nil, as changes by actorID, and takes the member out
// of the queues (ADR-0020). It runs in the caller's tenant transaction tx;
// the caller checked that to may edit tasks.
func (s *Service) HandOver(ctx context.Context, tx pgx.Tx, workspaceID, actorID, userID uuid.UUID, to *uuid.UUID) error {
	q := store.New(tx)
	if err := q.RemoveQueueMember(ctx, store.RemoveQueueMemberParams{WorkspaceID: workspaceID, UserID: userID}); err != nil {
		return fmt.Errorf("hand over tasks: %w", err)
	}
	rows, err := q.LockOpenTasksOf(ctx, store.LockOpenTasksOfParams{WorkspaceID: workspaceID, UserID: &userID})
	if err != nil {
		return fmt.Errorf("hand over tasks: %w", err)
	}
	for _, cur := range rows {
		row, err := q.SetTaskAssignee(ctx, store.SetTaskAssigneeParams{AssigneeID: to, UpdatedAt: s.now(), WorkspaceID: workspaceID, ID: cur.ID})
		if err != nil {
			return fmt.Errorf("hand over tasks: %w", err)
		}
		if err := s.recordChanges(ctx, tx, q, cur, row, actorID); err != nil {
			return fmt.Errorf("hand over tasks: %w", err)
		}
	}
	return nil
}
