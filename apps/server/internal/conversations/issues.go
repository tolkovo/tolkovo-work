package conversations

import (
	"context"
	"encoding/json"
	"fmt"

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
	Type  string // EventIssueCreated or EventIssueLinked
	Issue IssueRef
	// ActorID is the member who created or linked the issue; nil for the system.
	ActorID *uuid.UUID
	// AwaitingFix: the issue is open, so the conversation waits for the fix.
	AwaitingFix bool
}

// RecordIssue writes the feed line of an issue and, for an open one, puts
// the conversation to wait for the fix: the mark is set and a conversation
// an operator works on goes to pending, out of their way (docs/02 §8).
// Conversations in the queue or finished keep their status. Recording the
// same issue event again does nothing, so a retried job is safe.
//
// It runs inside the caller's tenant transaction (tx), with the change of
// the issue link that caused it; it never calls the network.
func (s *Service) RecordIssue(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, n IssueNote) error {
	if n.Type != EventIssueCreated && n.Type != EventIssueLinked {
		return fmt.Errorf("%w: unknown issue event %q", ErrInvalid, n.Type)
	}
	q := store.New(tx)
	row, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
	if err != nil {
		return wrap(err, "record issue")
	}
	cur := state{Status: Status(row.Status), AssigneeID: row.AssigneeID, ResolvedAt: row.ResolvedAt, ClosedAt: row.ClosedAt}
	next := cur
	if n.AwaitingFix && cur.Status == StatusAssigned {
		if next, err = transition(cur, StatusPending, s.now()); err != nil {
			return err
		}
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
	ids, err := q.CreateKeyedEventMessage(ctx, store.CreateKeyedEventMessageParams{
		ID: id, WorkspaceID: workspaceID, ConversationID: conversationID, SenderID: n.ActorID, Meta: meta,
		IdempotencyKey: ptr(n.Type + ":" + n.Issue.LinkID.String()), CreatedAt: s.now(),
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
		if err := addStateEvents(ctx, tx, workspaceID, uuid.Nil, conversationID, cur, next); err != nil {
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
