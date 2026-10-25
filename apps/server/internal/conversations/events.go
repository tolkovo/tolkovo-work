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

// Feed event types: system lines of the conversation feed (docs/04 §3.1).
const (
	EventStatusChanged = "status_changed"
	// EventAssigned covers taking, transferring and releasing; the status
	// change that comes with it is in the same event.
	EventAssigned = "assigned"
	// EventIssueCreated and EventIssueLinked: a GitHub issue was created
	// from the conversation or the conversation was linked to one
	// (ADR-0012). From and To are set when the conversation went to wait
	// for the fix with it.
	EventIssueCreated = "issue_created"
	EventIssueLinked  = "issue_linked"
	// EventIssueClosed and EventIssueReopened: GitHub closed (IssueRef.Reason
	// says why) or reopened a linked issue (GH-03). From and To are set when
	// the conversation came back from waiting for the fix or went to wait again.
	EventIssueClosed   = "issue_closed"
	EventIssueReopened = "issue_reopened"
	// Task events (ADR-0015): a task of the tasks section was created from
	// the conversation, linked to it or unlinked, closed (TaskRef.Reason)
	// or reopened. From and To are set as for the issue events.
	EventTaskCreated  = "task_created"
	EventTaskLinked   = "task_linked"
	EventTaskUnlinked = "task_unlinked"
	EventTaskClosed   = "task_closed"
	EventTaskReopened = "task_reopened"
	// EventTopicChanged: the topic was set, changed or removed, by the
	// customer (ByCustomer) or an operator (docs/02 §7.3).
	EventTopicChanged = "topic_changed"
	// EventBotHandoff: the AI bot handed the conversation to people
	// (bot → queued) for Reason (ADR-0018).
	EventBotHandoff = "bot_handoff"
	// EventLineChanged: an operator moved the conversation to another line
	// (ADR-0021), up, across or down; it left its assignee for the line's
	// queue. The reason, when given, is the note after it.
	EventLineChanged = "line_changed"
)

// Why GitHub closed an issue (IssueRef.Reason).
const (
	IssueFixed      = "completed"
	IssueNotPlanned = "not_planned"
)

// How a task was closed (TaskRef.Reason).
const (
	TaskDone     = "done"
	TaskCanceled = "canceled"
)

// Event is what a system message records. It holds ids and statuses only:
// the panel renders the line in the reader's language with member names.
type Event struct {
	Type               string     `json:"type"`
	From               Status     `json:"from"`
	To                 Status     `json:"to"`
	AssigneeID         *uuid.UUID `json:"assignee_id"`
	PreviousAssigneeID *uuid.UUID `json:"previous_assignee_id"`
	// Issue is set on the issue events.
	Issue *IssueRef `json:"issue,omitempty"`
	// Task is set on the task events.
	Task *TaskRef `json:"task,omitempty"`
	// TopicID and PreviousTopicID are set on EventTopicChanged; nil is no
	// topic. The panel names them from the workspace's topics.
	TopicID         *uuid.UUID `json:"topic_id,omitempty"`
	PreviousTopicID *uuid.UUID `json:"previous_topic_id,omitempty"`
	ByCustomer      bool       `json:"by_customer,omitempty"`
	// Reason is set on EventBotHandoff: one of the Handoff… reasons.
	Reason string `json:"reason,omitempty"`
	// LineID and PreviousLineID are set on EventLineChanged; nil is the
	// first line. The panel names them from the workspace's lines.
	LineID         *uuid.UUID `json:"line_id,omitempty"`
	PreviousLineID *uuid.UUID `json:"previous_line_id,omitempty"`
}

// IssueRef names an issue in a feed line: no title, the panel links to it.
type IssueRef struct {
	LinkID     uuid.UUID `json:"link_id"`
	Repository string    `json:"repository"`
	Number     int64     `json:"number"`
	URL        string    `json:"url"`
	// Reason is set on EventIssueClosed: IssueFixed or IssueNotPlanned.
	Reason string `json:"reason,omitempty"`
}

// TaskRef names a task in a feed line: no title, the panel links to it.
type TaskRef struct {
	TaskID uuid.UUID `json:"task_id"`
	Number int64     `json:"number"`
	// Reason is set on EventTaskClosed: TaskDone or TaskCanceled.
	Reason string `json:"reason,omitempty"`
}

// addEventMessage writes the system line of a state change, by actorID.
// It does not move last_message_at: system lines are not activity the
// inbox sorts by.
func addEventMessage(
	ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, actorID uuid.UUID, cur, next state, now time.Time,
) error {
	ev := Event{Type: EventStatusChanged, From: cur.Status, To: next.Status, AssigneeID: next.AssigneeID, PreviousAssigneeID: cur.AssigneeID}
	if !sameID(next.AssigneeID, cur.AssigneeID) {
		ev.Type = EventAssigned
	}
	return addEvent(ctx, q, tx, conv, actorID, ev, now)
}

// addEvent writes a system line of the feed, by actorID (uuid.Nil: the
// system or the customer).
func addEvent(ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, actorID uuid.UUID, ev Event, now time.Time) error {
	meta, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	id, err := newID()
	if err != nil {
		return err
	}
	var sender *uuid.UUID
	if actorID != uuid.Nil {
		sender = &actorID
	}
	if _, err := q.CreateEventMessage(ctx, store.CreateEventMessageParams{
		ID: id, WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID, SenderID: sender, Meta: meta, CreatedAt: now,
	}); err != nil {
		return err
	}
	return outbox.Add(ctx, tx, conv.WorkspaceID, "message.created", map[string]any{
		"conversation_id": conv.ID, "message_id": id, "kind": KindEvent, "is_private": false,
		"sender_type": SenderSystem, "sender_id": sender,
	})
}

// eventFrom decodes the meta of a system message; nil for other kinds or
// for meta it does not understand (old rows, future types).
func eventFrom(kind string, meta []byte) *Event {
	if kind != KindEvent || len(meta) == 0 {
		return nil
	}
	var ev Event
	if err := json.Unmarshal(meta, &ev); err != nil {
		return nil
	}
	switch ev.Type {
	case EventStatusChanged, EventAssigned, EventTopicChanged, EventLineChanged:
	case EventBotHandoff:
		if ev.Reason == "" {
			return nil
		}
	case EventIssueCreated, EventIssueLinked, EventIssueClosed, EventIssueReopened:
		if ev.Issue == nil {
			return nil
		}
	case EventTaskCreated, EventTaskLinked, EventTaskUnlinked, EventTaskClosed, EventTaskReopened:
		if ev.Task == nil {
			return nil
		}
	default:
		return nil
	}
	return &ev
}
