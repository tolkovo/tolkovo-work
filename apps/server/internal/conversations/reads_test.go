package conversations_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// inbound stores a customer message: channels write them (T-02, W-01),
// which do not exist yet.
func (f *fixture) inbound(ws, conv uuid.UUID, contact uuid.UUID) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	f.ownerExec(`INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, sender_id, kind, body)
		VALUES ($1, $2, $3, 'in', 'contact', $4, 'text', 'hello')`, id, ws, conv, contact)
	return id
}

func TestMarkRead_PerOperatorAndOnlyForward(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", tenancy.RoleOperator)
	c := f.assigned(f.wsA, olga)
	m1 := f.inbound(f.wsA, c.ID, c.ContactID)
	m2 := f.inbound(f.wsA, c.ID, c.ContactID)
	// The operator's own reply is not unread for anyone.
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, operator(olga), c.ID, conversations.MessageInput{Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	m3 := f.inbound(f.wsA, c.ID, c.ContactID)

	unread := func(user uuid.UUID) (int, *uuid.UUID) {
		t.Helper()
		got, err := f.svc.Conversation(f.ctx, f.wsA, user, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.UnreadCount, got.LastReadMessageID
	}
	if n, mark := unread(olga); n != 3 || mark != nil {
		t.Fatalf("before reading: %d %v", n, mark)
	}
	if err := f.svc.MarkRead(f.ctx, f.wsA, olga, c.ID, m2); err != nil {
		t.Fatal(err)
	}
	if n, mark := unread(olga); n != 1 || *mark != m2 {
		t.Fatalf("after m2: %d %v", n, mark)
	}
	// A late request from another tab does not move the mark back.
	if err := f.svc.MarkRead(f.ctx, f.wsA, olga, c.ID, m1); err != nil {
		t.Fatal(err)
	}
	if n, mark := unread(olga); n != 1 || *mark != m2 {
		t.Fatalf("after late m1: %d %v", n, mark)
	}
	// Someone else's reading changes nothing for her.
	if err := f.svc.MarkRead(f.ctx, f.wsA, f.ownerA, c.ID, m3); err != nil {
		t.Fatal(err)
	}
	if n, _ := unread(olga); n != 1 {
		t.Fatalf("owner's read cleared olga's counter: %d", n)
	}
	if n, _ := unread(f.ownerA); n != 0 {
		t.Fatalf("owner after reading: %d", n)
	}
	// Only moves are events: m2 by olga and m3 by the owner.
	reads := 0
	for _, topic := range f.events(f.wsA) {
		if topic == "conversation.read" {
			reads++
		}
	}
	if reads != 2 {
		t.Fatalf("want 2 conversation.read events, got %d", reads)
	}
	// The list returns the caller's counters.
	page, _ := f.svc.Conversations(f.ctx, f.wsA, conversations.ListFilter{ViewerID: olga})
	if len(page.Items) != 1 || page.Items[0].UnreadCount != 1 {
		t.Fatalf("list: %+v", page.Items)
	}
}

func TestMarkRead_RejectsForeignMessage(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	other := f.assigned(f.wsA, f.ownerA)
	m := f.inbound(f.wsA, other.ID, other.ContactID)
	if err := f.svc.MarkRead(f.ctx, f.wsA, f.ownerA, c.ID, m); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("message of another conversation: %v", err)
	}
	if err := f.svc.MarkRead(f.ctx, f.wsA, f.ownerA, uuid.Must(uuid.NewV7()), m); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("missing conversation: %v", err)
	}
}

func TestUnreadCount_Capped(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	f.ownerExec(`INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, kind, body)
		SELECT gen_random_uuid(), $1, $2, 'in', 'contact', 'text', 'x' FROM generate_series(1, 120)`, f.wsA, c.ID)
	got, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID)
	if err != nil || got.UnreadCount != conversations.MaxUnreadCount {
		t.Fatalf("cap: %d %v", got.UnreadCount, err)
	}
}

func TestInboxCounts_MineUnread(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", tenancy.RoleOperator)
	c := f.assigned(f.wsA, olga)
	f.assigned(f.wsA, olga) // nothing from the customer yet
	m := f.inbound(f.wsA, c.ID, c.ContactID)

	count := func(user uuid.UUID) int64 {
		t.Helper()
		n, err := f.svc.InboxCounts(f.ctx, f.wsA, user, false)
		if err != nil {
			t.Fatal(err)
		}
		return n.MineUnread
	}
	if got := count(olga); got != 1 {
		t.Fatalf("mine_unread = %d, want 1", got)
	}
	// Someone else's unread conversations are not in the caller's title.
	if got := count(f.ownerA); got != 0 {
		t.Fatalf("owner's mine_unread = %d, want 0", got)
	}
	if err := f.svc.MarkRead(f.ctx, f.wsA, olga, c.ID, m); err != nil {
		t.Fatal(err)
	}
	if got := count(olga); got != 0 {
		t.Fatalf("after reading mine_unread = %d, want 0", got)
	}
	f.inbound(f.wsA, c.ID, c.ContactID)
	if got := count(olga); got != 1 {
		t.Fatalf("after a new message mine_unread = %d, want 1", got)
	}
}
