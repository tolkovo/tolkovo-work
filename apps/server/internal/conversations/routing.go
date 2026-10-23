package conversations

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
)

// Assignment is a conversation the router gave to a member.
type Assignment struct {
	ConversationID uuid.UUID
	UserID         uuid.UUID
}

// HasQueued reports whether the queue still holds conversations one of
// available may take, those another transaction holds included, in the
// router's transaction tx.
func (s *Service) HasQueued(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, available []uuid.UUID) (bool, error) {
	ok, err := store.New(tx).HasQueued(ctx, store.HasQueuedParams{WorkspaceID: workspaceID, Available: available})
	if err != nil {
		return false, wrap(err, "has queued")
	}
	return ok, nil
}

// AssignQueued gives up to limit conversations from the head of the queue
// to assignees in turn: each goes to the first of assignees, in turn order,
// who may take it, and that one moves to the end. A conversation whose
// topic has members goes to one of them only (docs/02 §7.3). It runs in
// the router's tenant transaction tx (docs/02 §9.3) and records each
// assignment as a change by the system: events, the system line and the
// reminder. Conversations another transaction holds are left for the next
// round.
func (s *Service) AssignQueued(
	ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, assignees []uuid.UUID, limit int,
) ([]Assignment, error) {
	if len(assignees) == 0 || limit <= 0 {
		return nil, nil
	}
	q := store.New(tx)
	rows, err := q.LockQueue(ctx, store.LockQueueParams{WorkspaceID: workspaceID, Available: assignees, Lim: pageSize(limit)})
	if err != nil {
		return nil, wrap(err, "lock queue")
	}
	members, err := topicMembers(ctx, q, workspaceID, rows)
	if err != nil {
		return nil, wrap(err, "lock queue")
	}
	turn := slices.Clone(assignees)
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		i := 0
		if row.TopicID != nil && len(members[*row.TopicID]) > 0 {
			i = slices.IndexFunc(turn, func(u uuid.UUID) bool { return slices.Contains(members[*row.TopicID], u) })
			if i < 0 {
				continue // LockQueue picked it for someone: cannot happen
			}
		}
		user := turn[i]
		turn = append(append(turn[:i:i], turn[i+1:]...), user)
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

// topicMembers reads the members of the topics of rows, by topic.
func topicMembers(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, rows []store.Conversation) (map[uuid.UUID][]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, r := range rows {
		if r.TopicID != nil && !slices.Contains(ids, *r.TopicID) {
			ids = append(ids, *r.TopicID)
		}
	}
	out := map[uuid.UUID][]uuid.UUID{}
	if len(ids) == 0 {
		return out, nil
	}
	got, err := q.TopicMembersOf(ctx, store.TopicMembersOfParams{WorkspaceID: workspaceID, TopicIds: ids})
	for _, m := range got {
		out[m.TopicID] = append(out[m.TopicID], m.UserID)
	}
	return out, err
}

// HandOver gives the open conversations of userID to to, or releases them
// to the queue when to is nil, as changes by actorID: a member whose role
// changes or who leaves the team (ADR-0020). leaving also takes the member
// out of the topics. It runs in the caller's tenant transaction tx; the
// caller checked that to may answer customers.
func (s *Service) HandOver(ctx context.Context, tx pgx.Tx, workspaceID, actorID, userID uuid.UUID, to *uuid.UUID, leaving bool) error {
	q := store.New(tx)
	if leaving {
		if err := q.RemoveTopicMember(ctx, store.RemoveTopicMemberParams{WorkspaceID: workspaceID, UserID: userID}); err != nil {
			return wrap(err, "hand over")
		}
	}
	rows, err := q.LockOpenOf(ctx, store.LockOpenOfParams{WorkspaceID: workspaceID, UserID: &userID})
	if err != nil {
		return wrap(err, "hand over")
	}
	for _, row := range rows {
		cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
		next, err := assign(cur, to, s.now())
		if err != nil {
			return err
		}
		n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
			WorkspaceID: workspaceID, ID: row.ID, Version: row.Version, Status: string(next.Status),
			AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
		})
		if err != nil {
			return wrap(err, "hand over")
		}
		if n == 0 {
			return fmt.Errorf("hand over: %w", ErrConflict) // the row is locked: cannot happen
		}
		if err := s.recordChange(ctx, q, tx, row, actorID, cur, next); err != nil {
			return wrap(err, "hand over")
		}
	}
	if to == nil && len(rows) > 0 {
		return s.queued(ctx, tx, workspaceID, &userID)
	}
	return nil
}
