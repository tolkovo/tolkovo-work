package outbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox/store"
)

// Catch-up reads published events back for realtime clients that missed
// them (ADR-0005, backlog R-03). They run as the application role inside
// db.InTenantTx, so RLS limits them to the transaction's workspace on top of
// the explicit workspace_id filter.

// Since returns up to limit published events of the workspace with seq
// greater than afterSeq, in seq order, whose topics match one of the LIKE
// patterns (for example "conversation.%"). No patterns, no events.
func Since(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, afterSeq int64, patterns []string, limit int) ([]Event, error) {
	if workspaceID == uuid.Nil {
		return nil, errors.New("outbox: workspace id is required")
	}
	if len(patterns) == 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := store.New(tx).EventsAfter(ctx, store.EventsAfterParams{
		WorkspaceID:   workspaceID,
		AfterSeq:      afterSeq,
		TopicPatterns: patterns,
		MaxRows:       int32(min(limit, 10_000)), //nolint:gosec // G115: capped above
	})
	if err != nil {
		return nil, fmt.Errorf("outbox: events after %d: %w", afterSeq, err)
	}
	events := make([]Event, len(rows))
	for i, r := range rows {
		events[i] = Event{
			ID: r.ID, Seq: r.Seq.Int64, WorkspaceID: workspaceID,
			Topic: r.Topic, Payload: r.Payload, CreatedAt: r.CreatedAt,
		}
	}
	return events, nil
}

// LatestSeq is where a client without history starts: the newest published
// seq of the workspace or the prune watermark, whichever is higher.
func LatestSeq(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID) (int64, error) {
	seq, err := store.New(tx).LatestSeq(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("outbox: latest seq: %w", err)
	}
	return seq, nil
}

// PrunedSeq is the highest seq retention has deleted. Catch-up from a lower
// seq may have lost events: the client must refetch over REST instead.
func PrunedSeq(ctx context.Context, tx pgx.Tx) (int64, error) {
	seq, err := store.New(tx).PrunedSeq(ctx)
	if err != nil {
		return 0, fmt.Errorf("outbox: pruned seq: %w", err)
	}
	return seq, nil
}
