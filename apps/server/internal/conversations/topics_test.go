package conversations_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

func (f *fixture) topic(ws uuid.UUID, in conversations.TopicInput) conversations.Topic {
	f.t.Helper()
	t, err := f.svc.CreateTopic(f.ctx, ws, in)
	if err != nil {
		f.t.Fatalf("create topic %q: %v", in.Name, err)
	}
	return t
}

func TestTopics_Manage(t *testing.T) {
	f := setup(t)
	op := f.member(f.wsA, "olga", tenancy.RoleOperator)

	crm := f.topic(f.wsA, conversations.TopicInput{Name: "  CRM  ", Priority: 2, MemberIDs: []uuid.UUID{op}})
	if crm.Name != "CRM" || crm.Color != "gray" || crm.Priority != 2 || crm.Position != 0 || !slices.Equal(crm.MemberIDs, []uuid.UUID{op}) {
		t.Fatalf("created: %+v", crm)
	}
	of := f.topic(f.wsA, conversations.TopicInput{Name: "OnlyFans", Color: "blue"})
	if of.Position != 1 || len(of.MemberIDs) != 0 {
		t.Fatalf("second goes last, everyone takes it: %+v", of)
	}
	for _, bad := range []conversations.TopicInput{
		{Name: "crm"},                   // taken, case-insensitively
		{Name: ""},                      // required
		{Name: "Fansly", Priority: 4},   // 0..3
		{Name: "Fansly", Color: "pink"}, // palette
		{Name: "Fansly", MemberIDs: []uuid.UUID{f.ownerB}}, // not a member here
	} {
		_, err := f.svc.CreateTopic(f.ctx, f.wsA, bad)
		if !errors.Is(err, conversations.ErrTopicExists) && !errors.Is(err, conversations.ErrInvalid) {
			t.Fatalf("%+v: want refusal, got %v", bad, err)
		}
	}
	if _, err := f.svc.CreateTopic(f.ctx, f.wsA, conversations.TopicInput{Name: "crm"}); !errors.Is(err, conversations.ErrTopicExists) {
		t.Fatalf("taken name: %v", err)
	}

	// Changing replaces the members; reordering names every topic once.
	up, err := f.svc.UpdateTopic(f.ctx, f.wsA, crm.ID, conversations.TopicInput{Name: "CRM", Color: "teal", Priority: 3})
	if err != nil || up.Priority != 3 || up.Color != "teal" || len(up.MemberIDs) != 0 {
		t.Fatalf("update: %+v, %v", up, err)
	}
	if _, err := f.svc.UpdateTopic(f.ctx, f.wsA, of.ID, conversations.TopicInput{Name: "crm"}); !errors.Is(err, conversations.ErrTopicExists) {
		t.Fatalf("rename to a taken name: %v", err)
	}
	if _, err := f.svc.ReorderTopics(f.ctx, f.wsA, []uuid.UUID{of.ID}); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("partial order: %v", err)
	}
	list, err := f.svc.ReorderTopics(f.ctx, f.wsA, []uuid.UUID{of.ID, crm.ID})
	if err != nil || len(list) != 2 || list[0].ID != of.ID || list[1].ID != crm.ID {
		t.Fatalf("reorder: %+v, %v", list, err)
	}

	// Another workspace sees none of it.
	if other, err := f.svc.Topics(f.ctx, f.wsB); err != nil || len(other) != 0 {
		t.Fatalf("workspace B: %+v, %v", other, err)
	}
	if err := f.svc.DeleteTopic(f.ctx, f.wsB, crm.ID); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("delete from B: %v", err)
	}
	if _, err := f.svc.UpdateTopic(f.ctx, f.wsB, crm.ID, conversations.TopicInput{Name: "x"}); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("update from B: %v", err)
	}

	// Deleting leaves its conversations without a topic.
	c := f.conversation(f.wsA)
	if _, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, c.ID, &crm.ID, nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := f.svc.DeleteTopic(f.ctx, f.wsA, crm.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID)
	if err != nil || got.TopicID != nil {
		t.Fatalf("after delete: %+v, %v", got.TopicID, err)
	}
}

func TestSetTopic(t *testing.T) {
	f := setup(t)
	urgent := f.topic(f.wsA, conversations.TopicInput{Name: "CRM", Priority: 3})
	c := f.conversation(f.wsA)

	got, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, c.ID, &urgent.ID, &c.Version)
	if err != nil || got.TopicID == nil || *got.TopicID != urgent.ID || got.Priority != 3 || got.Version != c.Version+1 {
		t.Fatalf("set: %+v, %v", got, err)
	}
	// The same topic again changes nothing; a stale version is a conflict.
	again, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, c.ID, &urgent.ID, nil)
	if err != nil || again.Version != got.Version {
		t.Fatalf("same topic: %+v, %v", again, err)
	}
	if _, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, c.ID, nil, &c.Version); !errors.Is(err, conversations.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	// A topic of another workspace does not exist here.
	foreign := f.topic(f.wsB, conversations.TopicInput{Name: "CRM"})
	if _, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, c.ID, &foreign.ID, nil); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("foreign topic: %v", err)
	}
	// Removing it drops the priority.
	none, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, c.ID, nil, nil)
	if err != nil || none.TopicID != nil || none.Priority != 0 {
		t.Fatalf("remove: %+v, %v", none, err)
	}

	var lines []conversations.Event
	for _, m := range f.feed(f.wsA, c.ID) {
		if m.Event != nil && m.Event.Type == conversations.EventTopicChanged {
			lines = append(lines, *m.Event)
		}
	}
	if len(lines) != 2 || *lines[0].TopicID != urgent.ID || lines[0].PreviousTopicID != nil || lines[0].ByCustomer ||
		lines[1].TopicID != nil || *lines[1].PreviousTopicID != urgent.ID {
		t.Fatalf("feed lines: %+v", lines)
	}
	n := 0
	for _, e := range f.events(f.wsA) {
		if e == "conversation.topic_changed" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("want 2 topic_changed events, got %d", n)
	}
}

// dispatch runs the router's assignment in one tenant transaction.
func (f *fixture) dispatch(ws uuid.UUID, assignees ...uuid.UUID) []conversations.Assignment {
	f.t.Helper()
	var out []conversations.Assignment
	err := f.app.InTenantTx(f.ctx, ws, func(tx pgx.Tx) error {
		var err error
		out, err = f.svc.AssignQueued(context.Background(), tx, ws, assignees, 50)
		return err
	})
	if err != nil {
		f.t.Fatalf("assign queued: %v", err)
	}
	return out
}

func TestAssignQueued_TopicMembers(t *testing.T) {
	f := setup(t)
	ivan := f.member(f.wsA, "ivan", tenancy.RoleOperator)
	maria := f.member(f.wsA, "maria", tenancy.RoleOperator)
	crm := f.topic(f.wsA, conversations.TopicInput{Name: "CRM", MemberIDs: []uuid.UUID{maria}})
	free := f.topic(f.wsA, conversations.TopicInput{Name: "Свободная форма"})

	onlyMaria := f.conversation(f.wsA)
	if _, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, onlyMaria.ID, &crm.ID, nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Only Ivan is online: the CRM conversation waits for Maria and does not
	// keep the rest from him.
	anyone := f.conversation(f.wsA)
	if _, err := f.svc.SetTopic(f.ctx, f.wsA, f.ownerA, anyone.ID, &free.ID, nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	got := f.dispatch(f.wsA, ivan)
	if len(got) != 1 || got[0].ConversationID != anyone.ID || got[0].UserID != ivan {
		t.Fatalf("ivan alone: %+v", got)
	}
	var left bool
	_ = f.app.InTenantTx(f.ctx, f.wsA, func(tx pgx.Tx) error {
		var err error
		left, err = f.svc.HasQueued(f.ctx, tx, f.wsA, []uuid.UUID{ivan})
		return err
	})
	if left {
		t.Fatal("the CRM conversation counts as waiting for ivan")
	}

	// Ivan first in turn, but the CRM conversation goes to Maria; the next
	// one to Ivan.
	other := f.conversation(f.wsA)
	got = f.dispatch(f.wsA, ivan, maria)
	byConv := map[uuid.UUID]uuid.UUID{}
	for _, a := range got {
		byConv[a.ConversationID] = a.UserID
	}
	if len(got) != 2 || byConv[onlyMaria.ID] != maria || byConv[other.ID] != ivan {
		t.Fatalf("both online: %+v", got)
	}
}

func TestReceive_AsksTopicOnce(t *testing.T) {
	f := setup(t)
	ch := f.channel(f.wsA)
	in := inbound(ch, "1", "/start")
	in.CanAskTopic = true

	// No topics: nothing to ask.
	if res := f.receive(f.wsA, in); res.AskTopic {
		t.Fatal("asked without topics")
	}
	// A new conversation of the chat with topics: asked once.
	f.ownerExec("UPDATE conversations SET status = 'closed', closed_at = now(), resolved_at = now() WHERE workspace_id = $1", f.wsA)
	crm := f.topic(f.wsA, conversations.TopicInput{Name: "CRM"})
	in.ExternalID = "2"
	first := f.receive(f.wsA, in)
	if !first.AskTopic || !first.CreatedConversation {
		t.Fatalf("first message: %+v", first)
	}
	in.ExternalID, in.Body = "3", "Не грузится список моделей"
	if res := f.receive(f.wsA, in); res.AskTopic {
		t.Fatal("asked twice")
	}

	// The customer picks; the button of a deleted topic finds nothing.
	got, err := f.svc.ChooseThreadTopic(f.ctx, f.wsA, ch, "70001", crm.ID)
	if err != nil || got.Name != "CRM" {
		t.Fatalf("choose: %+v, %v", got, err)
	}
	if _, err := f.svc.ChooseThreadTopic(f.ctx, f.wsA, ch, "70001", uuid.New()); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("unknown topic: %v", err)
	}
	if _, err := f.svc.ChooseThreadTopic(f.ctx, f.wsA, ch, "99999", crm.ID); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("other chat: %v", err)
	}
	conv, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, first.ConversationID)
	if err != nil || conv.TopicID == nil || *conv.TopicID != crm.ID {
		t.Fatalf("conversation: %+v, %v", conv.TopicID, err)
	}
	var line *conversations.Event
	for _, m := range f.feed(f.wsA, conv.ID) {
		if m.Event != nil && m.Event.Type == conversations.EventTopicChanged {
			line = m.Event
		}
	}
	if line == nil || !line.ByCustomer {
		t.Fatalf("feed line: %+v", line)
	}
}

func TestCustomerTopic_ReturnsToQueue(t *testing.T) {
	f := setup(t)
	ch := f.channel(f.wsA)
	ivan := f.member(f.wsA, "ivan", tenancy.RoleOperator)
	maria := f.member(f.wsA, "maria", tenancy.RoleOperator)
	crm := f.topic(f.wsA, conversations.TopicInput{Name: "CRM", MemberIDs: []uuid.UUID{maria}})

	res := f.receive(f.wsA, inbound(ch, "1", "Здравствуйте"))
	got := f.dispatch(f.wsA, ivan)
	if len(got) != 1 {
		t.Fatalf("router: %+v", got)
	}
	// Ivan has not answered: the CRM topic sends it back for Maria.
	if _, err := f.svc.ChooseThreadTopic(f.ctx, f.wsA, ch, "70001", crm.ID); err != nil {
		t.Fatalf("choose: %v", err)
	}
	conv, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, res.ConversationID)
	if err != nil || conv.Status != conversations.StatusQueued || conv.AssigneeID != nil {
		t.Fatalf("after the topic: %s %v, %v", conv.Status, conv.AssigneeID, err)
	}
	if got := f.dispatch(f.wsA, ivan, maria); len(got) != 1 || got[0].UserID != maria {
		t.Fatalf("router again: %+v", got)
	}
}
