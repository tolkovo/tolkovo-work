package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Conversation is one dialogue with a contact.
type Conversation struct {
	ID              uuid.UUID
	Number          int64 // human-readable, per workspace
	ContactID       uuid.UUID
	ContactName     string
	ChannelID       *uuid.UUID
	Status          Status
	Priority        int
	LineLevel       int
	AssigneeID      *uuid.UUID
	Subject         *string
	FirstResponseAt *time.Time
	LastMessageAt   time.Time
	ResolvedAt      *time.Time
	ClosedAt        *time.Time
	Version         int // bumps on every change; send it back to detect conflicts
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// LastMessage is the newest message the customer could see (no notes,
	// no system events): the inbox preview. Nil until there is one.
	LastMessage *Preview
}

// Preview is a shortened message for the inbox list.
type Preview struct {
	SenderType  string
	Kind        string
	Text        string // up to 200 characters on one line; empty for files only
	Attachments int
	CreatedAt   time.Time
}

// InboxCounts are the counters of the inbox tabs for one operator.
type InboxCounts struct {
	Mine       int64 // assigned to the operator and not finished
	Unassigned int64 // waiting in the queue
	Open       int64 // every conversation not resolved or closed
}

// CreateConversationInput describes a conversation started from the panel
// or the API. Channels create theirs from inbound messages (T-02, AP-03).
type CreateConversationInput struct {
	ContactID uuid.UUID
	Subject   string
	Priority  int
}

// ListFilter selects conversations for the inbox.
type ListFilter struct {
	Statuses []Status // any of them; empty means all
	// ByAssignee restricts the list to AssigneeID, or to unassigned
	// conversations when AssigneeID is nil.
	ByAssignee bool
	AssigneeID *uuid.UUID
	Cursor     string
	Limit      int
}

// CreateConversation starts a conversation with an existing contact. It
// enters the queue at once: without the AI module (phase 2) nobody decides
// between the bot and the queue, so the new → queued step is immediate.
func (s *Service) CreateConversation(ctx context.Context, workspaceID uuid.UUID, in CreateConversationInput) (Conversation, error) {
	subject := strings.TrimSpace(in.Subject)
	switch {
	case utf8.RuneCountInString(subject) > 200:
		return Conversation{}, fmt.Errorf("%w: subject must be at most 200 characters", ErrInvalid)
	case in.Priority < 0 || in.Priority > 3:
		return Conversation{}, fmt.Errorf("%w: priority must be between 0 and 3", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return Conversation{}, err
	}
	var out Conversation
	err = s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		if _, err := q.GetContact(ctx, store.GetContactParams{WorkspaceID: workspaceID, ID: in.ContactID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: contact does not exist", ErrInvalid)
			}
			return err
		}
		number, err := q.NextConversationNumber(ctx, workspaceID)
		if err != nil {
			return err
		}
		_, err = q.CreateConversation(ctx, store.CreateConversationParams{
			ID: id, WorkspaceID: workspaceID, Number: number, ContactID: in.ContactID,
			Status: string(StatusQueued), Priority: int16(in.Priority), Subject: optional(subject), //nolint:gosec // G115: checked to be 0..3
		})
		if err != nil {
			return err
		}
		err = outbox.Add(ctx, tx, workspaceID, "conversation.created", map[string]any{
			"conversation_id": id, "contact_id": in.ContactID, "number": number, "status": StatusQueued,
		})
		if err != nil {
			return err
		}
		out, err = getConversation(ctx, q, workspaceID, id)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "create conversation")
	}
	return out, nil
}

// Conversation returns a conversation of the workspace.
func (s *Service) Conversation(ctx context.Context, workspaceID, id uuid.UUID) (Conversation, error) {
	var out Conversation
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		out, err = getConversation(ctx, q, workspaceID, id)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "get conversation")
	}
	return out, nil
}

// Conversations lists the inbox, most recent activity first.
func (s *Service) Conversations(ctx context.Context, workspaceID uuid.UUID, f ListFilter) (Page[Conversation], error) {
	beforeAt, beforeID, err := decodeTimeCursor(f.Cursor)
	if err != nil {
		return Page[Conversation]{}, err
	}
	statuses := make([]string, 0, len(f.Statuses))
	for _, st := range f.Statuses {
		if !st.Valid() {
			return Page[Conversation]{}, fmt.Errorf("%w: unknown status %q", ErrInvalid, st)
		}
		statuses = append(statuses, string(st))
	}
	lim := pageSize(f.Limit)
	var rows []store.ListConversationsRow
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		rows, err = q.ListConversations(ctx, store.ListConversationsParams{
			WorkspaceID: workspaceID, Statuses: statuses,
			ByAssignee: f.ByAssignee, AssigneeID: f.AssigneeID,
			BeforeAt: beforeAt, BeforeID: beforeID, Lim: lim + 1,
		})
		return err
	})
	if err != nil {
		return Page[Conversation]{}, wrap(err, "list conversations")
	}
	return paginate(rows, lim,
		func(r store.ListConversationsRow) Conversation {
			c := conversationFrom(r.Conversation, r.ContactName)
			c.LastMessage = previewFrom(r.LastSenderType, r.LastKind, r.LastBody, r.LastAttachments, r.LastCreatedAt)
			return c
		},
		func(r store.ListConversationsRow) string {
			return encodeTimeCursor(r.Conversation.LastMessageAt, r.Conversation.ID)
		}), nil
}

// InboxCounts counts the conversations of the inbox tabs for userID.
func (s *Service) InboxCounts(ctx context.Context, workspaceID, userID uuid.UUID) (InboxCounts, error) {
	var out InboxCounts
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		row, err := q.CountInbox(ctx, store.CountInboxParams{WorkspaceID: workspaceID, UserID: &userID})
		out = InboxCounts{Mine: row.Mine, Unassigned: row.Unassigned, Open: row.Open}
		return err
	})
	if err != nil {
		return InboxCounts{}, wrap(err, "count inbox")
	}
	return out, nil
}

// Transition changes the status along the state machine (docs/02 §8).
// version, when set, must equal the current version (optimistic locking).
// Without AnyConversation the actor may change only their own conversation.
func (s *Service) Transition(
	ctx context.Context, workspaceID uuid.UUID, actor Actor, id uuid.UUID, to Status, version *int,
) (Conversation, error) {
	return s.change(ctx, workspaceID, id, version, func(_ *store.Queries, cur state) (state, error) {
		if !actor.AnyConversation && !isAssignee(cur, actor.UserID) {
			return state{}, ErrNotAssignee
		}
		return transition(cur, to, s.now())
	})
}

// Assign gives the conversation to assignee, or releases it to the queue
// when assignee is nil. Without AssignOthers the actor may only take an
// unassigned conversation for themselves or release their own.
func (s *Service) Assign(
	ctx context.Context, workspaceID uuid.UUID, actor Actor, id uuid.UUID, assignee *uuid.UUID, version *int,
) (Conversation, error) {
	return s.change(ctx, workspaceID, id, version, func(q *store.Queries, cur state) (state, error) {
		if !actor.AssignOthers {
			takesForSelf := assignee != nil && *assignee == actor.UserID && (cur.AssigneeID == nil || isAssignee(cur, actor.UserID))
			releasesOwn := assignee == nil && isAssignee(cur, actor.UserID)
			if !takesForSelf && !releasesOwn {
				return state{}, ErrNotAssignee
			}
		}
		if assignee != nil {
			ok, err := q.IsActiveMember(ctx, store.IsActiveMemberParams{WorkspaceID: workspaceID, UserID: *assignee})
			if err != nil {
				return state{}, err
			}
			if !ok {
				return state{}, fmt.Errorf("%w: assignee is not an active member of the workspace", ErrInvalid)
			}
		}
		return assign(cur, assignee, s.now())
	})
}

// change runs one locked read-modify-write of a conversation's state and
// records the matching events.
func (s *Service) change(
	ctx context.Context, workspaceID, id uuid.UUID, version *int,
	decide func(q *store.Queries, cur state) (state, error),
) (Conversation, error) {
	var out Conversation
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return err
		}
		if version != nil && *version != int(row.Version) {
			return ErrConflict
		}
		cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
		next, err := decide(q, cur)
		if err != nil {
			return err
		}
		if next.Status == cur.Status && sameID(next.AssigneeID, cur.AssigneeID) {
			out, err = getConversation(ctx, q, workspaceID, id) // nothing to change
			return err
		}
		n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
			WorkspaceID: workspaceID, ID: id, Version: row.Version, Status: string(next.Status),
			AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrConflict
		}
		if err := addStateEvents(ctx, tx, workspaceID, id, cur, next); err != nil {
			return err
		}
		out, err = getConversation(ctx, q, workspaceID, id)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "change conversation")
	}
	return out, nil
}

// addStateEvents writes conversation.status_changed and
// conversation.assigned. Payloads carry ids only.
func addStateEvents(ctx context.Context, tx pgx.Tx, workspaceID, id uuid.UUID, cur, next state) error {
	if next.Status != cur.Status {
		err := outbox.Add(ctx, tx, workspaceID, "conversation.status_changed", map[string]any{
			"conversation_id": id, "from": cur.Status, "to": next.Status, "assignee_id": next.AssigneeID,
		})
		if err != nil {
			return err
		}
	}
	if !sameID(next.AssigneeID, cur.AssigneeID) {
		return outbox.Add(ctx, tx, workspaceID, "conversation.assigned", map[string]any{
			"conversation_id": id, "assignee_id": next.AssigneeID, "previous_assignee_id": cur.AssigneeID,
		})
	}
	return nil
}

func getConversation(ctx context.Context, q *store.Queries, workspaceID, id uuid.UUID) (Conversation, error) {
	row, err := q.GetConversation(ctx, store.GetConversationParams{WorkspaceID: workspaceID, ID: id})
	if err != nil {
		return Conversation{}, err
	}
	c := conversationFrom(row.Conversation, row.ContactName)
	c.LastMessage = previewFrom(row.LastSenderType, row.LastKind, row.LastBody, row.LastAttachments, row.LastCreatedAt)
	return c, nil
}

// previewFrom builds the preview columns of a row; kind is empty when the
// conversation has no visible message yet.
func previewFrom(senderType, kind, body string, attachments int32, at time.Time) *Preview {
	if kind == "" {
		return nil
	}
	return &Preview{
		SenderType: senderType, Kind: kind, Text: strings.Join(strings.Fields(body), " "),
		Attachments: int(attachments), CreatedAt: at,
	}
}

func isAssignee(st state, userID uuid.UUID) bool {
	return st.AssigneeID != nil && *st.AssigneeID == userID
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func conversationFrom(c store.Conversation, contactName string) Conversation {
	return Conversation{
		ID: c.ID, Number: c.Number, ContactID: c.ContactID, ContactName: contactName,
		ChannelID: c.ChannelID, Status: Status(c.Status), Priority: int(c.Priority), LineLevel: int(c.LineLevel),
		AssigneeID: c.AssigneeID, Subject: c.Subject, FirstResponseAt: c.FirstResponseAt,
		LastMessageAt: c.LastMessageAt, ResolvedAt: c.ResolvedAt, ClosedAt: c.ClosedAt,
		Version: int(c.Version), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
