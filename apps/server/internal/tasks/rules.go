package tasks

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Bounds of the rules of the board (ADR-0019, K2).
const (
	MaxWIPLimit = 999
	MaxAgeDays  = 365
)

// RuleStatuses are the columns a rule may be set for: the open ones.
var RuleStatuses = []string{StatusOpen, StatusInProgress, StatusReview, StatusVerified}

// ColumnRule is a soft limit of tasks in a column and the age after which a
// card stands too long, for the workspace (QueueID nil) or for a queue. Nil
// means none.
type ColumnRule struct {
	QueueID  *uuid.UUID
	Status   string
	WIPLimit *int
	AgeDays  *int
}

// ColumnRules returns the rules of the board, the workspace's first.
func (s *Service) ColumnRules(ctx context.Context, workspaceID uuid.UUID) ([]ColumnRule, error) {
	var out []ColumnRule
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = columnRules(ctx, store.New(tx), workspaceID)
		return err
	})
	if err != nil {
		return nil, dbError(err, "column rules")
	}
	return out, nil
}

func columnRules(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) ([]ColumnRule, error) {
	rows, err := q.ListColumnRules(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]ColumnRule, 0, len(rows))
	for _, r := range rows {
		out = append(out, ColumnRule{QueueID: r.QueueID, Status: r.Status, WIPLimit: intOf(r.WipLimit), AgeDays: intOf(r.AgeDays)})
	}
	return out, nil
}

// SetColumnRules replaces every rule of the board. A rule with neither a
// limit nor an age is left out; a queue of another workspace is ErrInvalid.
func (s *Service) SetColumnRules(ctx context.Context, workspaceID uuid.UUID, rules []ColumnRule) ([]ColumnRule, error) {
	if len(rules) > (MaxQueues+1)*len(RuleStatuses) {
		return nil, fmt.Errorf("%w: too many rules", ErrInvalid)
	}
	type key struct {
		queue  uuid.UUID
		status string
	}
	seen := map[key]bool{}
	for _, r := range rules {
		if !slices.Contains(RuleStatuses, r.Status) {
			return nil, fmt.Errorf("%w: a rule is for an open column, not %q", ErrInvalid, r.Status)
		}
		if r.WIPLimit != nil && (*r.WIPLimit < 1 || *r.WIPLimit > MaxWIPLimit) {
			return nil, fmt.Errorf("%w: a limit must be 1 to %d", ErrInvalid, MaxWIPLimit)
		}
		if r.AgeDays != nil && (*r.AgeDays < 1 || *r.AgeDays > MaxAgeDays) {
			return nil, fmt.Errorf("%w: an age must be 1 to %d days", ErrInvalid, MaxAgeDays)
		}
		k := key{status: r.Status}
		if r.QueueID != nil {
			k.queue = *r.QueueID
		}
		if seen[k] {
			return nil, fmt.Errorf("%w: two rules for one column", ErrInvalid)
		}
		seen[k] = true
	}
	var out []ColumnRule
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.DeleteColumnRules(ctx, workspaceID); err != nil {
			return err
		}
		for _, r := range rules {
			if r.WIPLimit == nil && r.AgeDays == nil {
				continue
			}
			if err := q.InsertColumnRule(ctx, store.InsertColumnRuleParams{
				WorkspaceID: workspaceID, QueueID: r.QueueID, Status: r.Status, WipLimit: int4Of(r.WIPLimit), AgeDays: int4Of(r.AgeDays),
			}); err != nil {
				return err
			}
		}
		var err error
		out, err = columnRules(ctx, q, workspaceID)
		return err
	})
	if err != nil {
		return nil, dbError(err, "set column rules")
	}
	return out, nil
}

// ruleFor is the rule a column of the board follows: the queue's, when the
// board shows one queue and the queue has one, otherwise the workspace's.
func ruleFor(rules []ColumnRule, queueID *uuid.UUID, status string) ColumnRule {
	var own, base ColumnRule
	for _, r := range rules {
		if r.Status != status {
			continue
		}
		switch {
		case r.QueueID == nil:
			base = r
		case queueID != nil && *r.QueueID == *queueID:
			own = r
		}
	}
	if own.Status != "" {
		return own
	}
	return base
}

func intOf(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func int4Of(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true} //nolint:gosec // G115: bounded by MaxWIPLimit and MaxAgeDays
}

func boolOf(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}
