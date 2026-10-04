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
)

// Event is what a system message records. It holds ids and statuses only:
// the panel renders the line in the reader's language with member names.
type Event struct {
	Type               string     `json:"type"`
	From               Status     `json:"from"`
	To                 Status     `json:"to"`
	AssigneeID         *uuid.UUID `json:"assignee_id"`
	PreviousAssigneeID *uuid.UUID `json:"previous_assignee_id"`
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
	if err := json.Unmarshal(meta, &ev); err != nil || (ev.Type != EventStatusChanged && ev.Type != EventAssigned) {
		return nil
	}
	return &ev
}
