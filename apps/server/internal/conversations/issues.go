package conversations

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// GitHub issues from conversations (ADR-0012). The integrations module owns
// the issues; conversations shows them in the feed and keeps the
// conversation waiting for the fix (docs/02 §8).

// IssueSource is what an issue draft starts from: no contact data.
type IssueSource struct {
	Number  int64
	Subject string
	// CustomerText is the customer's first message with text, up to
	// MaxBodyLength characters; empty when there is none.
	CustomerText string
	// App is what the app said about itself in its report; nil for other
	// channels.
	App *ReportApp
}

// IssueSource returns what an issue draft for the conversation starts from.
func (s *Service) IssueSource(ctx context.Context, workspaceID, conversationID uuid.UUID) (IssueSource, error) {
	var out IssueSource
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		row, err := q.GetConversation(ctx, store.GetConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		out.Number = row.Conversation.Number
		if row.Conversation.Subject != nil {
			out.Subject = *row.Conversation.Subject
		}
		first, err := q.FirstCustomerText(ctx, store.FirstCustomerTextParams{WorkspaceID: workspaceID, ConversationID: conversationID})
		if err != nil || len(first) == 0 {
			return err
		}
		out.CustomerText = first[0].Body
		var meta messageMeta
		if len(first[0].Meta) > 2 && json.Unmarshal(first[0].Meta, &meta) == nil && meta.Report != nil {
			app := meta.Report.App
			out.App = &app
		}
		return nil
	})
	if err != nil {
		return IssueSource{}, wrap(err, "issue source")
	}
	return out, nil
}

// IssueNote is an issue event for a conversation feed.
type IssueNote struct {
	// Type is EventIssueCreated, EventIssueLinked, EventIssueClosed or EventIssueReopened.
	Type  string
	Issue IssueRef
	// ActorID is the member who created or linked the issue; nil for the
	// system (GitHub closing or reopening it).
	ActorID *uuid.UUID
	// AwaitingFix: an issue of the conversation is open, so it waits for
	// the fix. The caller looks at every issue of the conversation.
	AwaitingFix bool
	// Key tells apart events of one issue that may repeat (closed, reopened,
	// closed again): the webhook delivery id. Empty for created and linked,
	// which happen once per issue.
	Key string
}

// RecordIssue writes the feed line of an issue event and keeps the
// conversation waiting for the fix while an issue is open (docs/02 §8):
//
//   - waiting starts (an open issue created, linked or reopened): the mark
//     is set and a conversation an operator works on goes to pending, out of
//     their way; queued and finished ones keep their status;
//   - waiting ends with an issue closed as fixed: a pending conversation
//     comes back to its assignee (pending → assigned), or to the queue when
//     they are no longer a member (presence, Q-02, will add "if online");
//   - an issue closed as not planned only clears the mark.
//
// Recording the same event again does nothing, so a retried job or a
// repeated webhook is safe. It runs inside the caller's tenant transaction
// (tx), with the change of the issue link that caused it; it never calls
// the network.
func (s *Service) RecordIssue(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, n IssueNote) error {
	switch n.Type {
	case EventIssueCreated, EventIssueLinked, EventIssueReopened:
	case EventIssueClosed:
		if n.Issue.Reason != IssueFixed && n.Issue.Reason != IssueNotPlanned {
			return fmt.Errorf("%w: unknown close reason %q", ErrInvalid, n.Issue.Reason)
		}
	default:
		return fmt.Errorf("%w: unknown issue event %q", ErrInvalid, n.Type)
	}
	q := store.New(tx)
	row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
	if err != nil {
		return wrap(err, "record issue")
	}
	cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
	next, err := s.issueState(ctx, q, tx, row, cur, n)
	if err != nil {
		return err
	}
	ev := Event{Type: n.Type, From: cur.Status, To: next.Status, AssigneeID: next.AssigneeID, PreviousAssigneeID: cur.AssigneeID, Issue: &n.Issue}
	meta, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	id, err := newID()
	if err != nil {
		return err
	}
	key := n.Type + ":" + n.Issue.LinkID.String()
	if n.Key != "" {
		key += ":" + n.Key
	}
	ids, err := q.CreateKeyedEventMessage(ctx, store.CreateKeyedEventMessageParams{
		ID: id, WorkspaceID: workspaceID, ConversationID: conversationID, SenderID: n.ActorID, Meta: meta,
		IdempotencyKey: ptr(key), CreatedAt: s.now(),
	})
	if err != nil {
		return wrap(err, "record issue")
	}
	if len(ids) == 0 {
		return nil // recorded before
	}
	if err := outbox.Add(ctx, tx, workspaceID, "message.created", map[string]any{
		"conversation_id": conversationID, "message_id": id, "kind": KindEvent, "is_private": false,
		"sender_type": SenderSystem, "sender_id": n.ActorID,
	}); err != nil {
		return err
	}
	if next.Status != cur.Status {
		changed, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
			WorkspaceID: workspaceID, ID: conversationID, Version: row.Version, Status: string(next.Status),
			AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
		})
		if err != nil {
			return err
		}
		if changed == 0 {
			return ErrConflict // the row is locked: cannot happen
		}
		if err := s.addStateEvents(ctx, tx, row, uuid.Nil, cur, next); err != nil {
			return err
		}
	}
	if n.AwaitingFix != row.AwaitingFix {
		if err := q.SetAwaitingFix(ctx, store.SetAwaitingFixParams{AwaitingFix: n.AwaitingFix, WorkspaceID: workspaceID, ID: conversationID}); err != nil {
			return err
		}
	}
	return nil
}

// issueState is the state an issue event leaves the conversation in.
func (s *Service) issueState(ctx context.Context, q *store.Queries, tx pgx.Tx, row store.Conversation, cur state, n IssueNote) (state, error) {
	switch {
	case n.AwaitingFix && cur.Status == StatusAssigned:
		return transition(cur, StatusPending, s.now())
	case !n.AwaitingFix && row.AwaitingFix && cur.Status == StatusPending &&
		n.Type == EventIssueClosed && n.Issue.Reason == IssueFixed:
		next, err := transition(cur, StatusAssigned, s.now())
		if err != nil {
			return state{}, err
		}
		ok, err := s.canReturnTo(ctx, q, tx, row.WorkspaceID, *cur.AssigneeID)
		if err != nil || ok {
			return next, err
		}
		return transition(next, StatusQueued, s.now()) // released, as when the operator is away
	}
	return cur, nil
}

// ReplySource is who a reply about a fix is drafted for (GH-03).
type ReplySource struct {
	ContactName string
	// ContactLocale is "ru", "en" or empty when unknown.
	ContactLocale string
	// LastReplyAt is when an operator last wrote to the customer, or when
	// the conversation started.
	LastReplyAt time.Time
}

// ReplySource returns what a reply about a fix is drafted from.
func (s *Service) ReplySource(ctx context.Context, workspaceID, conversationID uuid.UUID) (ReplySource, error) {
	var out ReplySource
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		row, err := q.ReplySource(ctx, store.ReplySourceParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		out = ReplySource{ContactName: row.ContactName, LastReplyAt: row.LastReplyAt}
		if row.ContactLocale != nil {
			out.ContactLocale = *row.ContactLocale
		}
		return nil
	})
	if err != nil {
		return ReplySource{}, wrap(err, "reply source")
	}
	return out, nil
}
