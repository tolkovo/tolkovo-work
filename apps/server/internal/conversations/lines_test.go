package conversations_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

func (f *fixture) line(ws uuid.UUID, in conversations.LineInput) conversations.Line {
	f.t.Helper()
	l, err := f.svc.CreateLine(f.ctx, ws, in)
	if err != nil {
		f.t.Fatalf("create line %q: %v", in.Name, err)
	}
	return l
}

func TestLines_Manage(t *testing.T) {
	f := setup(t)
	oleg := f.member(f.wsA, "oleg", tenancy.RoleOperator)

	billing := f.line(f.wsA, conversations.LineInput{Name: "  Биллинг ", Level: 2, MemberIDs: []uuid.UUID{oleg}})
	if billing.Name != "Биллинг" || billing.Level != 2 || !slices.Equal(billing.MemberIDs, []uuid.UUID{oleg}) {
		t.Fatalf("created: %+v", billing)
	}
	f.line(f.wsA, conversations.LineInput{Name: "Эксперты", Level: 3})
	for _, bad := range []conversations.LineInput{
		{Name: "биллинг", Level: 2}, // taken, case-insensitively
		{Name: "", Level: 1},
		{Name: "Ночная", Level: 4},
		{Name: "Дубли", Level: 1, MemberIDs: []uuid.UUID{oleg, oleg}},
		{Name: "Чужой", Level: 1, MemberIDs: []uuid.UUID{uuid.Must(uuid.NewV7())}},
	} {
		if _, err := f.svc.CreateLine(f.ctx, f.wsA, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	lines, err := f.svc.Lines(f.ctx, f.wsA)
	if err != nil || len(lines) != 2 || lines[0].ID != billing.ID {
		t.Fatalf("lines by level: %+v %v", lines, err)
	}
	if other, _ := f.svc.Lines(f.ctx, f.wsB); len(other) != 0 {
		t.Fatalf("another workspace sees %d lines", len(other))
	}
	if _, err := f.svc.CreateLine(f.ctx, f.wsB, conversations.LineInput{Name: "Биллинг", Level: 2}); err != nil {
		t.Fatalf("the name is per workspace: %v", err)
	}
}

// Escalation: up with a reason to the line's queue, the reason a note
// after the feed line; down without one. The level follows the line, and a
// deleted line sends its conversations back to the first line.
func TestEscalate(t *testing.T) {
	f := setup(t)
	ivan := f.member(f.wsA, "ivan", tenancy.RoleOperator)
	oleg := f.member(f.wsA, "oleg", tenancy.RoleOperator)
	billing := f.line(f.wsA, conversations.LineInput{Name: "Биллинг", Level: 2, MemberIDs: []uuid.UUID{oleg}})
	support := f.line(f.wsA, conversations.LineInput{Name: "Поддержка", Level: 1})
	c := f.assigned(f.wsA, ivan)
	me := conversations.Actor{UserID: ivan}
	n := &recordingNotifier{}
	f.svc.SetNotifier(n)

	if _, err := f.svc.Escalate(f.ctx, f.wsA, me, c.ID, &billing.ID, "  ", nil); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("up without a reason: %v", err)
	}
	if _, err := f.svc.Escalate(f.ctx, f.wsA, conversations.Actor{UserID: oleg}, c.ID, &billing.ID, "Счёт", nil); !errors.Is(err, conversations.ErrNotAssignee) {
		t.Fatalf("someone else's conversation: %v", err)
	}
	if _, err := f.svc.Escalate(f.ctx, f.wsA, me, c.ID, nil, "", nil); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("to the line it is on: %v", err)
	}
	got, err := f.svc.Escalate(f.ctx, f.wsA, me, c.ID, &billing.ID, "Двойное списание", nil)
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if got.Status != conversations.StatusQueued || got.AssigneeID != nil || got.LineID == nil || *got.LineID != billing.ID || got.LineLevel != 2 {
		t.Fatalf("escalated: %s %v %v %d", got.Status, got.AssigneeID, got.LineID, got.LineLevel)
	}
	page, err := f.svc.Messages(f.ctx, f.wsA, c.ID, "", 20)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	// The feed line, then the note right after it.
	i := slices.IndexFunc(page.Items, func(m conversations.Message) bool {
		return m.Event != nil && m.Event.Type == conversations.EventLineChanged
	})
	j := slices.IndexFunc(page.Items, func(m conversations.Message) bool { return m.IsPrivate })
	if i < 0 || j < 0 || page.Items[j].ID.String() < page.Items[i].ID.String() {
		t.Fatalf("feed line %d, note %d", i, j)
	}
	line, note := page.Items[i], page.Items[j]
	if line.Event == nil || line.Event.Type != conversations.EventLineChanged || line.Event.LineID == nil || *line.Event.LineID != billing.ID ||
		line.Event.PreviousLineID != nil || line.Event.PreviousAssigneeID == nil || *line.Event.PreviousAssigneeID != ivan {
		t.Fatalf("feed line: %+v", line.Event)
	}
	if !note.IsPrivate || note.Body != "Двойное списание" || note.SenderID == nil || *note.SenderID != ivan {
		t.Fatalf("reason note: %+v", note)
	}

	// The line's members hear it waits.
	n.mu.Lock()
	heard := len(n.queued) == 1 && n.queued[0] == c.ID && n.lines[0] != nil && *n.lines[0] == billing.ID
	n.mu.Unlock()
	if !heard {
		t.Fatalf("queue notice: %v %v", n.queued, n.lines)
	}

	// The router gives it to the line's members only.
	assigned := f.dispatch(f.wsA, ivan)
	if len(assigned) != 0 {
		t.Fatalf("an L1 operator got the L2 conversation: %+v", assigned)
	}
	assigned = f.dispatch(f.wsA, ivan, oleg)
	if len(assigned) != 1 || assigned[0].UserID != oleg {
		t.Fatalf("dispatch: %+v", assigned)
	}

	// Down to an L1 line needs no reason; an admin may move anyone's.
	admin := conversations.Actor{UserID: f.ownerA, AnyConversation: true, AssignOthers: true}
	got, err = f.svc.Escalate(f.ctx, f.wsA, admin, c.ID, &support.ID, "", nil)
	if err != nil || got.LineLevel != 1 || *got.LineID != support.ID || got.Status != conversations.StatusQueued {
		t.Fatalf("down: %+v %v", got, err)
	}
	// A queued one moves across without an assignee to drop.
	if got, err = f.svc.Escalate(f.ctx, f.wsA, admin, c.ID, &billing.ID, "Опять счёт", nil); err != nil || got.LineLevel != 2 {
		t.Fatalf("queued up: %+v %v", got, err)
	}

	// The line becomes level 3: its conversation follows; deleted: back to
	// the first line.
	if _, err := f.svc.UpdateLine(f.ctx, f.wsA, billing.ID, conversations.LineInput{Name: "Биллинг", Level: 3, MemberIDs: []uuid.UUID{oleg}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID); got.LineLevel != 3 {
		t.Fatalf("level after update: %d", got.LineLevel)
	}
	if err := f.svc.DeleteLine(f.ctx, f.wsA, billing.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID); got.LineID != nil || got.LineLevel != 1 {
		t.Fatalf("after delete: %v %d", got.LineID, got.LineLevel)
	}
	if err := f.svc.DeleteLine(f.ctx, f.wsA, billing.ID); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	// Resolved conversations keep their line.
	done := f.assigned(f.wsA, ivan)
	if _, err := f.svc.Transition(f.ctx, f.wsA, me, done.ID, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := f.svc.Escalate(f.ctx, f.wsA, me, done.ID, &support.ID, "", nil); !errors.Is(err, conversations.ErrTransition) {
		t.Fatalf("resolved: %v", err)
	}
}

// A member who leaves the team leaves the lines.
func TestLines_HandOverLeaving(t *testing.T) {
	f := setup(t)
	oleg := f.member(f.wsA, "oleg", tenancy.RoleOperator)
	l := f.line(f.wsA, conversations.LineInput{Name: "Биллинг", Level: 2, MemberIDs: []uuid.UUID{oleg}})
	err := f.app.InTenantTx(f.ctx, f.wsA, func(tx pgx.Tx) error {
		return f.svc.HandOver(f.ctx, tx, f.wsA, f.ownerA, oleg, nil, true)
	})
	if err != nil {
		t.Fatalf("hand over: %v", err)
	}
	lines, _ := f.svc.Lines(f.ctx, f.wsA)
	if len(lines) != 1 || lines[0].ID != l.ID || len(lines[0].MemberIDs) != 0 {
		t.Fatalf("lines: %+v", lines)
	}
}
