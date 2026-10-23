package tenancy

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy/store"
)

// Changing the role of a member and removing one from the team (ADR-0020).
// What the member keeps in other modules (conversations, tasks, the
// Telegram link) is handed over by MemberHooks, in the transaction of the
// change.

// ErrProtectedMember is returned for the owner and for the actor themselves,
// who are not changed or removed here; ownership is handed over separately.
var ErrProtectedMember = errors.New("tenancy: this member cannot be changed")

// AssignableRoles are the roles a member may be given. Ownership is never
// given this way.
var AssignableRoles = []string{RoleAdmin, RoleOperator}

// MemberChange is a role change or a removal, as hooks see it.
type MemberChange struct {
	WorkspaceID uuid.UUID
	ActorID     uuid.UUID
	UserID      uuid.UUID
	// Role is the new role; empty when the member leaves.
	Role    string
	Leaving bool
	// ConversationsTo takes the member's open conversations when they leave
	// or their new role cannot answer customers; nil is the queue.
	ConversationsTo *uuid.UUID
	// TasksTo takes the open tasks of a member who leaves; nil is nobody.
	TasksTo *uuid.UUID
}

// HandsOverConversations reports whether the member's open conversations
// leave them with this change.
func (c MemberChange) HandsOverConversations() bool {
	return c.Leaving || !authz.Allowed(c.Role, authz.ConversationsReply)
}

// MemberHook hands over what a module keeps for the member, in tx.
type MemberHook func(ctx context.Context, tx pgx.Tx, ch MemberChange) error

// AddMemberHook registers h; hooks run in the order added.
func (s *Service) AddMemberHook(h MemberHook) { s.hooks = append(s.hooks, h) }

// Member returns one member of the workspace, active or not.
func (s *Service) Member(ctx context.Context, workspaceID, userID uuid.UUID) (Member, error) {
	var out Member
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = getMember(ctx, store.New(tx), workspaceID, userID)
		return err
	})
	if err != nil {
		return Member{}, memberError(err, "get member")
	}
	return out, nil
}

// ChangeRole gives the member role. Open conversations go to
// conversationsTo, or to the queue, when the new role cannot answer
// customers.
func (s *Service) ChangeRole(
	ctx context.Context, workspaceID, actorID, userID uuid.UUID, role string, conversationsTo *uuid.UUID,
) (Member, error) {
	if !slices.Contains(AssignableRoles, role) {
		return Member{}, fmt.Errorf("%w: role must be one of %v", ErrInvalid, AssignableRoles)
	}
	ch := MemberChange{WorkspaceID: workspaceID, ActorID: actorID, UserID: userID, Role: role, ConversationsTo: conversationsTo}
	var out Member
	err := s.changeMember(ctx, ch, func(q *store.Queries, cur string) (bool, error) {
		if cur == role {
			return false, nil
		}
		n, err := q.SetMemberRole(ctx, store.SetMemberRoleParams{WorkspaceID: workspaceID, UserID: userID, RoleKey: role})
		if err == nil && n == 0 {
			err = fmt.Errorf("%w: %q", ErrUnknownRole, role)
		}
		return true, err
	}, func(q *store.Queries) error {
		var err error
		out, err = getMember(ctx, q, workspaceID, userID)
		return err
	})
	if err != nil {
		return Member{}, err
	}
	return out, nil
}

// Deactivate removes the member from the team: access ends at their next
// request, their personal access tokens are revoked, and the hooks hand
// over the rest. An invitation brings them back.
func (s *Service) Deactivate(ctx context.Context, workspaceID, actorID, userID uuid.UUID, conversationsTo, tasksTo *uuid.UUID) error {
	ch := MemberChange{
		WorkspaceID: workspaceID, ActorID: actorID, UserID: userID, Leaving: true,
		ConversationsTo: conversationsTo, TasksTo: tasksTo,
	}
	return s.changeMember(ctx, ch, func(q *store.Queries, _ string) (bool, error) {
		if _, err := q.DeactivateMember(ctx, store.DeactivateMemberParams{WorkspaceID: workspaceID, UserID: userID}); err != nil {
			return false, err
		}
		return true, q.DeleteMemberAccessTokens(ctx, store.DeleteMemberAccessTokensParams{WorkspaceID: workspaceID, UserID: userID})
	}, nil)
}

// changeMember locks the member, refuses the protected ones, checks who
// takes over, applies the change and runs the hooks. apply reports whether
// anything changed; done reads the result in the same transaction.
func (s *Service) changeMember(
	ctx context.Context, ch MemberChange,
	apply func(q *store.Queries, curRole string) (bool, error), done func(q *store.Queries) error,
) error {
	err := s.db.InTenantTx(ctx, ch.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.LockMember(ctx, store.LockMemberParams{WorkspaceID: ch.WorkspaceID, UserID: ch.UserID})
		if err != nil {
			return err
		}
		if !cur.IsActive {
			return ErrNotFound
		}
		if ch.UserID == ch.ActorID || cur.RoleKey == RoleOwner {
			return ErrProtectedMember
		}
		if err := checkTakesOver(ctx, q, ch, ch.ConversationsTo, authz.ConversationsReply, "conversations_to"); err != nil {
			return err
		}
		if err := checkTakesOver(ctx, q, ch, ch.TasksTo, authz.TasksEdit, "tasks_to"); err != nil {
			return err
		}
		changed, err := apply(q, cur.RoleKey)
		if err != nil {
			return err
		}
		if changed {
			for _, h := range s.hooks {
				if err := h(ctx, tx, ch); err != nil {
					return err
				}
			}
			if err := outbox.Add(ctx, tx, ch.WorkspaceID, "member.changed", map[string]any{"user_id": ch.UserID}); err != nil {
				return err
			}
		}
		if done != nil {
			return done(q)
		}
		return nil
	})
	return memberError(err, "change member")
}

// checkTakesOver refuses a successor who is the member themselves, not an
// active member, or whose role lacks perm.
func checkTakesOver(ctx context.Context, q *store.Queries, ch MemberChange, to *uuid.UUID, perm authz.Permission, field string) error {
	if to == nil {
		return nil
	}
	if *to == ch.UserID {
		return fmt.Errorf("%w: %s is the member themselves", ErrInvalid, field)
	}
	m, err := q.GetMember(ctx, store.GetMemberParams{WorkspaceID: ch.WorkspaceID, UserID: *to})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: %s is not a member", ErrInvalid, field)
	case err != nil:
		return err
	case !m.IsActive || !authz.Allowed(m.RoleKey, perm):
		return fmt.Errorf("%w: %s cannot take them over", ErrInvalid, field)
	}
	return nil
}

func getMember(ctx context.Context, q *store.Queries, workspaceID, userID uuid.UUID) (Member, error) {
	r, err := q.GetMember(ctx, store.GetMemberParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return Member{}, err
	}
	return Member{
		UserID: r.UserID, Email: r.Email, Name: r.Name, Role: r.RoleKey,
		MaxConcurrentChats: int(r.MaxConcurrentChats), LineLevel: int(r.LineLevel), Active: r.IsActive,
	}, nil
}

func memberError(err error, op string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalid), errors.Is(err, ErrProtectedMember), errors.Is(err, ErrUnknownRole):
		return err
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}
