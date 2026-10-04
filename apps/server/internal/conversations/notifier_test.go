package conversations_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// recordingNotifier remembers whom NeedsAttention was called for.
type recordingNotifier struct {
	mu    sync.Mutex
	calls []uuid.UUID // user ids
	err   error
}

func (n *recordingNotifier) NeedsAttention(_ context.Context, tx pgx.Tx, _, _, userID uuid.UUID) error {
	if tx == nil {
		return errors.New("no transaction")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, userID)
	return n.err
}

func (n *recordingNotifier) users() []uuid.UUID {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.calls)
}

func TestConversations_NotifierOnAssign(t *testing.T) {
	f := setup(t)
	n := &recordingNotifier{}
	f.svc.SetNotifier(n)
	op := f.member(f.wsA, "olga", tenancy.RoleOperator)

	// Taking a conversation for oneself needs no reminder.
	c := f.conversation(f.wsA)
	if _, err := f.svc.Assign(f.ctx, f.wsA, operator(op), c.ID, &op, nil); err != nil {
		t.Fatalf("take: %v", err)
	}
	if got := n.users(); len(got) != 0 {
		t.Fatalf("taking for oneself notified %v", got)
	}

	// Given by someone else: the assignee is told.
	c2 := f.conversation(f.wsA)
	if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), c2.ID, &op, nil); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := n.users(); !slices.Equal(got, []uuid.UUID{op}) {
		t.Fatalf("notified %v, want [%s]", got, op)
	}

	// Releasing to the queue tells nobody.
	if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), c2.ID, nil, nil); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := n.users(); len(got) != 1 {
		t.Fatalf("release notified: %v", got)
	}

	// A notifier error rolls the change back: no assignment without its reminder.
	n.err = errors.New("queue down")
	c3 := f.conversation(f.wsA)
	if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), c3.ID, &op, nil); err == nil {
		t.Fatal("assign succeeded although the notifier failed")
	}
	got, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c3.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AssigneeID != nil {
		t.Fatalf("assignee %v kept after the failed notifier", got.AssigneeID)
	}
}
