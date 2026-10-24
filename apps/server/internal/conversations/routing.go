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

// Pool is who the router may give conversations to in one run, in its
// strategy's order (ADR-0021). AssignQueued asks it for each conversation.
type Pool interface {
	// Users are the members with room for one more conversation now.
	Users() []uuid.UUID
	// Pick chooses the next of Users for whom may holds and counts the
	// conversation against them; false when none fits.
	Pick(may func(uuid.UUID) bool) (uuid.UUID, bool)
}

// Loads returns the conversations in work (assigned or pending) of each of
// userIDs, in the router's transaction tx; members without any are missing.
func (s *Service) Loads(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := store.New(tx).CountOpenOf(ctx, store.CountOpenOfParams{WorkspaceID: workspaceID, UserIds: userIDs})
	if err != nil {
		return nil, wrap(err, "loads")
	}
	out := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		out[r.UserID] = int(r.Open)
	}
	return out, nil
}

// AssignQueued gives up to limit conversations from the head of the queue
// to the members of pool: each goes to whom the pool picks among those who
// may take it. A conversation of a line goes to one of its members only
// (ADR-0021); on the first line one whose topic has members goes to one of
// them only (docs/02 §7.3). It runs in the router's tenant transaction tx
// (docs/02 §9.3) and records each assignment as a change by the system:
// events, the system line and the reminder. Conversations another
// transaction holds, or nobody in the pool may take, are left for the next
// round.
func (s *Service) AssignQueued(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, pool Pool, limit int) ([]Assignment, error) {
	users := pool.Users()
	if len(users) == 0 || limit <= 0 {
		return nil, nil
	}
	q := store.New(tx)
	rows, err := q.LockQueue(ctx, store.LockQueueParams{WorkspaceID: workspaceID, Available: users, Lim: pageSize(limit)})
	if err != nil {
		return nil, wrap(err, "lock queue")
	}
	members, err := topicMembers(ctx, q, workspaceID, rows)
	if err != nil {
		return nil, wrap(err, "lock queue")
	}
	lines, err := lineMembers(ctx, q, workspaceID, rows)
	if err != nil {
		return nil, wrap(err, "lock queue")
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		may := func(uuid.UUID) bool { return true }
		switch {
		case row.LineID != nil:
			only := lines[*row.LineID]
			may = func(u uuid.UUID) bool { return slices.Contains(only, u) }
		case row.TopicID != nil && len(members[*row.TopicID]) > 0:
			only := members[*row.TopicID]
			may = func(u uuid.UUID) bool { return slices.Contains(only, u) }
		}
		user, ok := pool.Pick(may)
		if !ok {
			continue // those who may take it are full now
		}
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
// out of the topics and the lines. It runs in the caller's tenant transaction tx; the
// caller checked that to may answer customers.
func (s *Service) HandOver(ctx context.Context, tx pgx.Tx, workspaceID, actorID, userID uuid.UUID, to *uuid.UUID, leaving bool) error {
	q := store.New(tx)
	if leaving {
		if err := q.RemoveTopicMember(ctx, store.RemoveTopicMemberParams{WorkspaceID: workspaceID, UserID: userID}); err != nil {
			return wrap(err, "hand over")
		}
		if err := q.RemoveLineMember(ctx, store.RemoveLineMemberParams{WorkspaceID: workspaceID, UserID: userID}); err != nil {
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
