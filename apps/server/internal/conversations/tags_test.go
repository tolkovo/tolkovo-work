package conversations_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

func TestTags_CreateListAndValidate(t *testing.T) {
	f := setup(t)
	vip, err := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "  VIP   client ", Color: "amber"})
	if err != nil || vip.Name != "VIP client" || vip.Color != "amber" {
		t.Fatalf("create: %+v %v", vip, err)
	}
	bug, err := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "bug"})
	if err != nil || bug.Color != "gray" {
		t.Fatalf("default color: %+v %v", bug, err)
	}
	// Names are unique without regard to case.
	if _, err := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "vip CLIENT"}); !errors.Is(err, conversations.ErrTagExists) {
		t.Fatalf("duplicate: %v", err)
	}
	// The same name is free in another workspace.
	if _, err := f.svc.CreateTag(f.ctx, f.wsB, f.ownerB, conversations.TagInput{Name: "bug"}); err != nil {
		t.Fatalf("other workspace: %v", err)
	}
	for name, in := range map[string]conversations.TagInput{
		"empty":     {Name: "   "},
		"too long":  {Name: strings.Repeat("я", conversations.MaxTagNameLength+1)},
		"bad color": {Name: "x", Color: "#ff0000"},
	} {
		if _, err := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, in); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tags, err := f.svc.Tags(f.ctx, f.wsA)
	if err != nil || len(tags) != 2 || tags[0].ID != bug.ID || tags[1].ID != vip.ID {
		t.Fatalf("list by name: %+v %v", tags, err)
	}
}

func TestSetTags_ReplacesBumpsVersionAndRecordsEvent(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", tenancy.RoleOperator)
	c := f.assigned(f.wsA, f.ownerA)
	a, _ := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "a"})
	b, _ := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "b"})

	// Any member who may tag works on someone else's conversation.
	got, err := f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, []uuid.UUID{b.ID}, &c.Version)
	if err != nil || !slices.Equal(got.TagIDs, []uuid.UUID{b.ID}) || got.Version != c.Version+1 {
		t.Fatalf("set: %+v %v", got, err)
	}
	// Tags keep the order they were added in, not the order of ids.
	got, err = f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, []uuid.UUID{a.ID, b.ID}, nil)
	if err != nil || !slices.Equal(got.TagIDs, []uuid.UUID{b.ID, a.ID}) {
		t.Fatalf("add: %+v %v", got, err)
	}
	got, err = f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, []uuid.UUID{a.ID}, nil)
	if err != nil || !slices.Equal(got.TagIDs, []uuid.UUID{a.ID}) {
		t.Fatalf("replace: %+v %v", got, err)
	}
	// The same set changes nothing: no version bump, no event.
	same, err := f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, []uuid.UUID{a.ID}, nil)
	if err != nil || same.Version != got.Version {
		t.Fatalf("no-op: %+v %v", same, err)
	}
	if _, err := f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, []uuid.UUID{}, &c.Version); !errors.Is(err, conversations.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	cleared, err := f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, nil, nil)
	if err != nil || len(cleared.TagIDs) != 0 {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
	n := 0
	for _, topic := range f.events(f.wsA) {
		if topic == "conversation.tags_changed" {
			n++
		}
	}
	if n != 4 {
		t.Fatalf("want 4 tags_changed events, got %d", n)
	}

	// The list carries tags too.
	if _, err := f.svc.SetTags(f.ctx, f.wsA, olga, c.ID, []uuid.UUID{b.ID}, nil); err != nil {
		t.Fatal(err)
	}
	page, _ := f.svc.Conversations(f.ctx, f.wsA, conversations.ListFilter{})
	if len(page.Items) != 1 || !slices.Equal(page.Items[0].TagIDs, []uuid.UUID{b.ID}) {
		t.Fatalf("list tags: %+v", page.Items)
	}
}

func TestSetTags_Validates(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	a, _ := f.svc.CreateTag(f.ctx, f.wsA, f.ownerA, conversations.TagInput{Name: "a"})
	many := make([]uuid.UUID, conversations.MaxConversationTags+1)
	for i := range many {
		many[i] = uuid.Must(uuid.NewV7())
	}
	for name, ids := range map[string][]uuid.UUID{
		"duplicate": {a.ID, a.ID},
		"unknown":   {a.ID, uuid.Must(uuid.NewV7())},
		"too many":  many,
	} {
		if _, err := f.svc.SetTags(f.ctx, f.wsA, f.ownerA, c.ID, ids, nil); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.SetTags(f.ctx, f.wsA, f.ownerA, uuid.Must(uuid.NewV7()), nil, nil); !errors.Is(err, conversations.ErrNotFound) {
		t.Errorf("missing conversation: %v", err)
	}
}
