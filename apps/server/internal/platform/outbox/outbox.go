// Package outbox implements the transactional outbox: a domain event is
// written in the same transaction as the change it describes, and a relay
// publishes it afterwards (docs/02 §4, docs/adr/0004-river-background-jobs.md).
//
// Delivery is at least once: a crash between publishing and marking the
// batch publishes it again. Consumers deduplicate by Event.ID.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox/store"
)

// Event is a published domain event.
type Event struct {
	ID          int64 // insertion order; the deduplication key
	Seq         int64 // publication order; consumers resume from it
	WorkspaceID uuid.UUID
	Topic       string // "<module>.<entity>.<fact>" in snake_case, e.g. "conversation.created"
	Payload     json.RawMessage
	CreatedAt   time.Time
}

// Publisher delivers a batch of events in Seq order. It is called outside any
// database transaction, so it may do network I/O.
type Publisher interface {
	Publish(ctx context.Context, events []Event) error
}

// Add appends an event inside tx, which must come from db.InTenantTx for the
// same workspace: RLS rejects an event for any other workspace. The payload
// is stored as JSON and must not contain secrets; personal data only by ID.
func Add(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, topic string, payload any) error {
	if workspaceID == uuid.Nil {
		return errors.New("outbox: workspace id is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox: encode %s payload: %w", topic, err)
	}
	err = store.New(tx).InsertEvent(ctx, store.InsertEventParams{
		WorkspaceID: workspaceID,
		Topic:       topic,
		Payload:     body,
	})
	if err != nil {
		return fmt.Errorf("outbox: add %s: %w", topic, err)
	}
	return nil
}
