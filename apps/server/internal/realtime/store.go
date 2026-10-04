package realtime

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// TenantDB is the part of *db.DB the gateway needs: every read runs in a
// transaction scoped to the connection's workspace, so RLS applies.
type TenantDB interface {
	InTenantTx(ctx context.Context, workspaceID uuid.UUID, fn func(pgx.Tx) error) error
}

// catchUp is one replay read.
type catchUp struct {
	events []outbox.Event
	pruned int64 // retention deleted every seq up to here
	latest int64 // newest published seq of the workspace (or pruned)
}

// eventStore reads published events back from Postgres (ADR-0005: Postgres
// is the source of truth, Redis only carries live updates).
type eventStore struct {
	db TenantDB
}

func (s eventStore) latestSeq(ctx context.Context, workspaceID uuid.UUID) (int64, error) {
	var seq int64
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		seq, err = outbox.LatestSeq(ctx, tx, workspaceID)
		return err
	})
	return seq, err
}

// since returns at most limit events after afterSeq that role may see, and
// the watermarks to decide between replay and resync.
func (s eventStore) since(ctx context.Context, workspaceID uuid.UUID, role string, afterSeq int64, limit int) (catchUp, error) {
	var out catchUp
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		if out.pruned, err = outbox.PrunedSeq(ctx, tx); err != nil {
			return err
		}
		if out.latest, err = outbox.LatestSeq(ctx, tx, workspaceID); err != nil {
			return err
		}
		out.events, err = outbox.Since(ctx, tx, workspaceID, afterSeq, topicPatterns(role), limit)
		return err
	})
	return out, err
}
