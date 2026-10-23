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
	// AwaitingFix: an open GitHub issue is linked to the conversation
	// (ADR-0012); it does not resolve itself by inactivity.
	AwaitingFix bool
	Version     int // bumps on every change; send it back to detect conflicts
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// LastMessage is the newest message the customer could see (no notes,
	// no system events): the inbox preview. Nil until there is one.
	LastMessage *Preview
	TagIDs      []uuid.UUID // in the order they were added
	// TopicID is what the question is about (docs/02 §7.3); nil: none yet.
	TopicID *uuid.UUID
	// LastReadMessageID and UnreadCount are the viewer's: read marks are per
	// operator. UnreadCount counts customer messages after the mark, up to
	// MaxUnreadCount.
	LastReadMessageID *uuid.UUID
	UnreadCount       int
	// Rating is the customer's rating of the resolved conversation; nil
	// until they rate it.
	Rating *Rating
}

// MaxUnreadCount caps the unread counter of a conversation.
const MaxUnreadCount = 100

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
	MineUnread int64 // of those, with customer messages the operator has not read
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
	ViewerID uuid.UUID // whose unread counters to return
	Statuses []Status  // any of them; empty means all
	// ByAssignee restricts the list to AssigneeID, or to unassigned
	// conversations when AssigneeID is nil.
	ByAssignee bool
	AssigneeID *uuid.UUID
	// ContactID restricts the list to one contact's conversations.
	ContactID *uuid.UUID
	// ByTopic restricts the list to TopicID, or to conversations without a
	// topic when TopicID is nil.
	ByTopic bool
	TopicID *uuid.UUID
	Cursor  string
	Limit   int
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
		if err := s.queued(ctx, tx, workspaceID, nil); err != nil {
			return err
		}
		out, err = getConversation(ctx, q, workspaceID, uuid.Nil, id)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "create conversation")
	}
	return out, nil
}

// Conversation returns a conversation of the workspace as viewerID sees it.
func (s *Service) Conversation(ctx context.Context, workspaceID, viewerID, id uuid.UUID) (Conversation, error) {
	var out Conversation
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		out, err = getConversation(ctx, q, workspaceID, viewerID, id)
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
			UserID: f.ViewerID, WorkspaceID: workspaceID, Statuses: statuses,
			ByAssignee: f.ByAssignee, AssigneeID: f.AssigneeID, ContactID: f.ContactID,
			ByTopic: f.ByTopic, TopicID: f.TopicID, BeforeAt: beforeAt, BeforeID: beforeID, Lim: lim + 1,
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
			c.TagIDs, c.LastReadMessageID, c.UnreadCount = r.TagIds, r.LastReadMessageID, int(r.UnreadCount)
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
		out = InboxCounts{Mine: row.Mine, MineUnread: row.MineUnread, Unassigned: row.Unassigned, Open: row.Open}
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
	return s.change(ctx, workspaceID, actor.UserID, id, version, func(_ *store.Queries, cur state) (state, error) {
		if !actor.AnyConversation && !isAssignee(cur, actor.UserID) {
			return state{}, ErrNotAssignee
		}
		return transition(cur, to, s.now())
	})
}

// Assign gives the conversation to assignee, or releases it to the queue
// when assignee is nil. Without AssignOthers the actor may only take an
// unassigned conversation for themselves and release or transfer their own.
func (s *Service) Assign(
	ctx context.Context, workspaceID uuid.UUID, actor Actor, id uuid.UUID, assignee *uuid.UUID, version *int,
) (Conversation, error) {
	return s.change(ctx, workspaceID, actor.UserID, id, version, func(q *store.Queries, cur state) (state, error) {
		if !actor.AssignOthers {
			takesForSelf := assignee != nil && *assignee == actor.UserID && cur.AssigneeID == nil
			// Releasing or transferring one's own conversation ("transfer (own)" in docs/04 §4.3).
			ownsIt := isAssignee(cur, actor.UserID)
			if !takesForSelf && !ownsIt {
				return state{}, ErrNotAssignee
			}
		}
		if assignee != nil {
			ok, err := q.CanAnswer(ctx, store.CanAnswerParams{WorkspaceID: workspaceID, UserID: *assignee, Answering: answering})
			if err != nil {
				return state{}, err
			}
			if !ok {
				return state{}, fmt.Errorf("%w: assignee is not an active member who answers customers", ErrInvalid)
			}
		}
		return assign(cur, assignee, s.now())
	})
}

// change runs one locked read-modify-write of a conversation's state and
// records the matching events and the system line of the feed. The result
// is the conversation as actorID sees it.
func (s *Service) change(
	ctx context.Context, workspaceID, actorID, id uuid.UUID, version *int,
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
			out, err = getConversation(ctx, q, workspaceID, actorID, id) // nothing to change
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
		if err := s.recordChange(ctx, q, tx, row, actorID, cur, next); err != nil {
			return err
		}
		out, err = getConversation(ctx, q, workspaceID, actorID, id)
		return err
	})
	if err != nil {
		return Conversation{}, wrap(err, "change conversation")
	}
	return out, nil
}

// recordChange writes what follows a state change already applied to the
// row: the events, the system line of the feed, and the reminder of a
// member given the conversation by someone else (docs/04 §5.5).
func (s *Service) recordChange(
	ctx context.Context, q *store.Queries, tx pgx.Tx, row store.Conversation, actorID uuid.UUID, cur, next state,
) error {
	if err := s.addStateEvents(ctx, tx, row, actorID, cur, next); err != nil {
		return err
	}
	if err := addEventMessage(ctx, q, tx, row, actorID, cur, next, s.now()); err != nil {
		return err
	}
	if s.notifier != nil && next.AssigneeID != nil && !sameID(next.AssigneeID, cur.AssigneeID) && *next.AssigneeID != actorID {
		return s.notifier.NeedsAttention(ctx, tx, row.WorkspaceID, row.ID, *next.AssigneeID)
	}
	return nil
}

// queued tells the router that a conversation entered the queue.
func (s *Service) queued(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, releasedBy *uuid.UUID) error {
	if s.router == nil {
		return nil
	}
	if err := s.router.Queued(ctx, tx, workspaceID, releasedBy); err != nil {
		return fmt.Errorf("router: %w", err)
	}
	return nil
}

// canReturnTo reports whether a conversation the customer reopens goes back
// to its former assignee: an active member whom the router finds available
// (with automatic distribution, accepting and with the panel open).
func (s *Service) canReturnTo(ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID, userID uuid.UUID) (bool, error) {
	ok, err := q.CanAnswer(ctx, store.CanAnswerParams{WorkspaceID: workspaceID, UserID: userID, Answering: answering})
	if err != nil || !ok || s.router == nil {
		return ok, err
	}
	ok, err = s.router.Available(ctx, tx, workspaceID, userID)
	if err != nil {
		return false, fmt.Errorf("router: %w", err)
	}
	return ok, nil
}

// addStateEvents writes conversation.status_changed and
// conversation.assigned. Payloads carry ids only; actor_id is null for a
// change made by the system. A conversation entering the queue is told to
// the router, with whoever held it, so it does not go straight back; one
// resolved or reopened is told to its channel.
func (s *Service) addStateEvents(ctx context.Context, tx pgx.Tx, row store.Conversation, actorID uuid.UUID, cur, next state) error {
	workspaceID, id := row.WorkspaceID, row.ID
	if err := s.settled(ctx, tx, row, cur, next); err != nil {
		return err
	}
	var actor *uuid.UUID
	if actorID != uuid.Nil {
		actor = &actorID
	}
	if next.Status != cur.Status {
		err := outbox.Add(ctx, tx, workspaceID, "conversation.status_changed", map[string]any{
			"conversation_id": id, "from": cur.Status, "to": next.Status, "assignee_id": next.AssigneeID, "actor_id": actor,
		})
		if err != nil {
			return err
		}
		if next.Status == StatusQueued {
			if err := s.queued(ctx, tx, workspaceID, cur.AssigneeID); err != nil {
				return err
			}
		}
	}
	if !sameID(next.AssigneeID, cur.AssigneeID) {
		return outbox.Add(ctx, tx, workspaceID, "conversation.assigned", map[string]any{
			"conversation_id": id, "assignee_id": next.AssigneeID, "previous_assignee_id": cur.AssigneeID, "actor_id": actor,
		})
	}
	return nil
}

// getConversation reads a conversation with viewerID's read mark; uuid.Nil
// has none, so everything from the customer counts as unread.
func getConversation(ctx context.Context, q *store.Queries, workspaceID, viewerID, id uuid.UUID) (Conversation, error) {
	row, err := q.GetConversation(ctx, store.GetConversationParams{UserID: viewerID, WorkspaceID: workspaceID, ID: id})
	if err != nil {
		return Conversation{}, err
	}
	c := conversationFrom(row.Conversation, row.ContactName)
	c.LastMessage = previewFrom(row.LastSenderType, row.LastKind, row.LastBody, row.LastAttachments, row.LastCreatedAt)
	c.TagIDs, c.LastReadMessageID, c.UnreadCount = row.TagIds, row.LastReadMessageID, int(row.UnreadCount)
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
		LastMessageAt: c.LastMessageAt, ResolvedAt: c.ResolvedAt, ClosedAt: c.ClosedAt, AwaitingFix: c.AwaitingFix,
		Version: int(c.Version), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Rating: ratingFrom(c), TopicID: c.TopicID,
	}
}
