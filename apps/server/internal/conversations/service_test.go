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

func TestContacts_CreateUpdateAndValidate(t *testing.T) {
	f := setup(t)
	c, err := f.svc.CreateContact(f.ctx, f.wsA, conversations.ContactInput{
		Name: "  Ivan  ", Email: "ivan@example.test", Phone: "+7 (900) 123-45-67", Locale: "ru",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Name != "Ivan" || *c.Email != "ivan@example.test" || *c.Locale != "ru" {
		t.Fatalf("created: %+v", c)
	}

	empty := ""
	newName := "Ivan P."
	u, err := f.svc.UpdateContact(f.ctx, f.wsA, c.ID, conversations.ContactPatch{Name: &newName, Phone: &empty})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if u.Name != newName || u.Phone != nil || u.Email == nil {
		t.Fatalf("patch must change name, clear phone and keep email: %+v", u)
	}
	if got, err := f.svc.Contact(f.ctx, f.wsA, c.ID); err != nil || got.Name != newName {
		t.Fatalf("get: %+v %v", got, err)
	}

	bad := []conversations.ContactInput{
		{Email: "not-an-email"},
		{Email: "Ivan <ivan@example.test>"},
		{Phone: "call me"},
		{Locale: "de"},
		{Name: strings.Repeat("я", 201)},
	}
	for _, in := range bad {
		if _, err := f.svc.CreateContact(f.ctx, f.wsA, in); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", in, err)
		}
	}
	if got := f.events(f.wsA); !slices.Equal(got, []string{"contact.created", "contact.updated"}) {
		t.Fatalf("events: %v", got)
	}
}

func TestContacts_PaginateNewestFirst(t *testing.T) {
	f := setup(t)
	var want []uuid.UUID
	for range 5 {
		want = append([]uuid.UUID{f.contact(f.wsA, "c").ID}, want...)
	}
	var got []uuid.UUID
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := f.svc.Contacts(f.ctx, f.wsA, cursor, 2)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, c := range page.Items {
			got = append(got, c.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pages: got %v, want %v", got, want)
	}
	if _, err := f.svc.Contacts(f.ctx, f.wsA, "garbage!", 2); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestCreateConversation_NumbersPerWorkspaceAndQueues(t *testing.T) {
	f := setup(t)
	a1, a2, b1 := f.conversation(f.wsA), f.conversation(f.wsA), f.conversation(f.wsB)
	if a1.Number != 1 || a2.Number != 2 || b1.Number != 1 {
		t.Fatalf("numbers: %d %d %d", a1.Number, a2.Number, b1.Number)
	}
	if a1.Status != conversations.StatusQueued || a1.AssigneeID != nil || a1.ContactName != "Client" || a1.Version != 1 {
		t.Fatalf("new conversation: %+v", a1)
	}
	// A contact of another workspace does not exist here.
	_, err := f.svc.CreateConversation(f.ctx, f.wsA, conversations.CreateConversationInput{ContactID: f.contact(f.wsB, "x").ID})
	if !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("foreign contact: %v", err)
	}
	if _, err := f.svc.CreateConversation(f.ctx, f.wsA, conversations.CreateConversationInput{
		ContactID: f.contact(f.wsA, "x").ID, Priority: 9,
	}); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("priority: %v", err)
	}
}

func TestTransition_FollowsStateMachineAndRecordsEvents(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)

	steps := []conversations.Status{conversations.StatusPending, conversations.StatusResolved, conversations.StatusAssigned,
		conversations.StatusResolved, conversations.StatusClosed}
	for _, to := range steps {
		next, err := f.svc.Transition(f.ctx, f.wsA, operator(f.ownerA), c.ID, to, &c.Version)
		if err != nil {
			t.Fatalf("→ %s: %v", to, err)
		}
		if next.Status != to || next.Version != c.Version+1 {
			t.Fatalf("→ %s: got %s v%d after v%d", to, next.Status, next.Version, c.Version)
		}
		c = next
	}
	if c.ClosedAt == nil || c.ResolvedAt == nil || c.AssigneeID == nil {
		t.Fatalf("closed conversation keeps history: %+v", c)
	}
	_, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.StatusQueued, nil)
	if !errors.Is(err, conversations.ErrTransition) {
		t.Fatalf("closed is final: %v", err)
	}

	got := f.events(f.wsA)
	// Every change also writes a system line to the feed (message.created).
	want := []string{"contact.created", "conversation.created", "conversation.status_changed", "conversation.assigned", "message.created"}
	for range steps {
		want = append(want, "conversation.status_changed", "message.created")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}

	feed, err := f.svc.Messages(f.ctx, f.wsA, c.ID, "", 10)
	if err != nil || len(feed.Items) != 1+len(steps) {
		t.Fatalf("feed: %d items, %v", len(feed.Items), err)
	}
	last := feed.Items[0]
	if last.Kind != conversations.KindEvent || last.SenderType != conversations.SenderSystem || *last.SenderID != f.ownerA ||
		last.Body != "" || last.IsPrivate || last.Event == nil {
		t.Fatalf("system line: %+v", last)
	}
	if e := *last.Event; e.Type != conversations.EventStatusChanged || e.From != conversations.StatusResolved ||
		e.To != conversations.StatusClosed || *e.AssigneeID != f.ownerA {
		t.Fatalf("closing event: %+v", e)
	}
	if e := feed.Items[len(feed.Items)-1].Event; e == nil || e.Type != conversations.EventAssigned || e.From != conversations.StatusQueued ||
		e.To != conversations.StatusAssigned || e.PreviousAssigneeID != nil || *e.AssigneeID != f.ownerA {
		t.Fatalf("assignment event: %+v", e)
	}
	// System lines are not activity: the inbox order stays.
	if c.LastMessage != nil {
		t.Fatalf("a system line must not be the preview: %+v", c.LastMessage)
	}
}

func TestTransition_RejectsStaleVersionAndForeignConversation(t *testing.T) {
	f := setup(t)
	op := f.member(f.wsA, "olga", tenancy.RoleOperator)
	c := f.assigned(f.wsA, f.ownerA)

	stale := c.Version - 1
	_, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.StatusPending, &stale)
	if !errors.Is(err, conversations.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	// An operator may not change someone else's conversation.
	_, err = f.svc.Transition(f.ctx, f.wsA, operator(op), c.ID, conversations.StatusResolved, nil)
	if !errors.Is(err, conversations.ErrNotAssignee) {
		t.Fatalf("operator on owner's conversation: %v", err)
	}
	// Status "assigned" is reached by assigning someone, not by a status change.
	q := f.conversation(f.wsA)
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), q.ID, conversations.StatusAssigned, nil); !errors.Is(err, conversations.ErrTransition) {
		t.Fatalf("queued → assigned without assignee: %v", err)
	}
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), uuid.Must(uuid.NewV7()), conversations.StatusResolved, nil); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("missing conversation: %v", err)
	}
}

func TestAssign_OperatorTakesReleasesAndTransfersOnlyOwn(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", tenancy.RoleOperator)
	petr := f.member(f.wsA, "petr", tenancy.RoleOperator)
	c := f.conversation(f.wsA)

	// Take from the queue.
	c, err := f.svc.Assign(f.ctx, f.wsA, operator(olga), c.ID, &olga, nil)
	if err != nil || c.Status != conversations.StatusAssigned || *c.AssigneeID != olga {
		t.Fatalf("take: %+v %v", c, err)
	}
	// Another operator can neither take it nor hand it to themselves.
	if _, err := f.svc.Assign(f.ctx, f.wsA, operator(petr), c.ID, &petr, nil); !errors.Is(err, conversations.ErrNotAssignee) {
		t.Fatalf("steal: %v", err)
	}
	if _, err := f.svc.Assign(f.ctx, f.wsA, operator(petr), c.ID, nil, nil); !errors.Is(err, conversations.ErrNotAssignee) {
		t.Fatalf("release foreign: %v", err)
	}
	// The assignee hands their own conversation to a colleague ("transfer (own)", docs/04 §4.3)...
	c, err = f.svc.Assign(f.ctx, f.wsA, operator(olga), c.ID, &petr, nil)
	if err != nil || *c.AssigneeID != petr || c.Status != conversations.StatusAssigned {
		t.Fatalf("operator transfers own: %+v %v", c, err)
	}
	// ...and then it is no longer hers to move.
	if _, err := f.svc.Assign(f.ctx, f.wsA, operator(olga), c.ID, &olga, nil); !errors.Is(err, conversations.ErrNotAssignee) {
		t.Fatalf("take back: %v", err)
	}
	// Release back to the queue.
	c, err = f.svc.Assign(f.ctx, f.wsA, operator(petr), c.ID, nil, nil)
	if err != nil || c.Status != conversations.StatusQueued || c.AssigneeID != nil {
		t.Fatalf("release: %+v %v", c, err)
	}
	// An admin transfers between operators.
	c, _ = f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), c.ID, &olga, nil)
	c, err = f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), c.ID, &petr, nil)
	if err != nil || *c.AssigneeID != petr || c.Status != conversations.StatusAssigned {
		t.Fatalf("transfer: %+v %v", c, err)
	}
}

func TestAssign_OnlyActiveMembersOfTheWorkspace(t *testing.T) {
	f := setup(t)
	c := f.conversation(f.wsA)
	gone := f.member(f.wsA, "gone", tenancy.RoleOperator)
	f.ownerExec("UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2", f.wsA, gone)

	for name, who := range map[string]uuid.UUID{
		"inactive member":   gone,
		"member of B only":  f.ownerB,
		"not a user at all": uuid.Must(uuid.NewV7()),
	} {
		if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), c.ID, &who, nil); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestConversations_FilterAndPaginate(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", tenancy.RoleOperator)
	mine := []uuid.UUID{f.assigned(f.wsA, olga).ID, f.assigned(f.wsA, olga).ID, f.assigned(f.wsA, olga).ID}
	queued := []uuid.UUID{f.conversation(f.wsA).ID, f.conversation(f.wsA).ID}
	other := f.assigned(f.wsA, f.ownerA).ID

	ids := func(fl conversations.ListFilter) []uuid.UUID {
		t.Helper()
		var out []uuid.UUID
		for {
			page, err := f.svc.Conversations(f.ctx, f.wsA, fl)
			if err != nil {
				t.Fatalf("list %+v: %v", fl, err)
			}
			for _, c := range page.Items {
				out = append(out, c.ID)
			}
			if page.NextCursor == "" {
				return out
			}
			fl.Cursor = page.NextCursor
		}
	}
	reversed := func(in []uuid.UUID) []uuid.UUID { out := slices.Clone(in); slices.Reverse(out); return out }

	if got := ids(conversations.ListFilter{ByAssignee: true, AssigneeID: &olga, Limit: 2}); !slices.Equal(got, reversed(mine)) {
		t.Errorf("olga's: got %v, want %v", got, reversed(mine))
	}
	if got := ids(conversations.ListFilter{ByAssignee: true, Limit: 1}); !slices.Equal(got, reversed(queued)) {
		t.Errorf("unassigned: got %v, want %v", got, reversed(queued))
	}
	if got := ids(conversations.ListFilter{Statuses: []conversations.Status{conversations.StatusQueued}}); !slices.Equal(got, reversed(queued)) {
		t.Errorf("queued: got %v", got)
	}
	all := ids(conversations.ListFilter{Limit: 4})
	if len(all) != 6 || all[0] != other {
		t.Errorf("all, newest activity first: %v", all)
	}
	// Activity reorders the inbox: a reply lifts the oldest conversation.
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, operator(olga), mine[0], conversations.MessageInput{Body: "hi"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got := ids(conversations.ListFilter{}); got[0] != mine[0] {
		t.Errorf("after a reply the conversation comes first: %v", got)
	}
	if _, err := f.svc.Conversations(f.ctx, f.wsA, conversations.ListFilter{Statuses: []conversations.Status{"archived"}}); !errors.Is(err, conversations.ErrInvalid) {
		t.Errorf("unknown status filter: %v", err)
	}
}

func TestConversations_ContactHistory(t *testing.T) {
	f := setup(t)
	anna := f.contact(f.wsA, "Anna")
	start := func() uuid.UUID {
		t.Helper()
		c, err := f.svc.CreateConversation(f.ctx, f.wsA, conversations.CreateConversationInput{ContactID: anna.ID})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return c.ID
	}
	first := start()
	if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), first, &f.ownerA, nil); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), first, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	second := start()
	f.conversation(f.wsA) // another contact's

	var got []uuid.UUID
	fl := conversations.ListFilter{ContactID: &anna.ID, Limit: 1}
	for {
		page, err := f.svc.Conversations(f.ctx, f.wsA, fl)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		for _, c := range page.Items {
			if c.ContactID != anna.ID {
				t.Errorf("another contact's conversation in the history: %+v", c)
			}
			got = append(got, c.ID)
		}
		if page.NextCursor == "" {
			break
		}
		fl.Cursor = page.NextCursor
	}
	// Finished ones stay in the history; newest activity first.
	if want := []uuid.UUID{second, first}; !slices.Equal(got, want) {
		t.Errorf("history: got %v, want %v", got, want)
	}
	unknown := uuid.New()
	if page, err := f.svc.Conversations(f.ctx, f.wsA, conversations.ListFilter{ContactID: &unknown}); err != nil || len(page.Items) != 0 {
		t.Errorf("unknown contact: %+v %v", page.Items, err)
	}
}

func TestPostMessage_ReplyNoteAndRules(t *testing.T) {
	f := setup(t)
	olga := f.member(f.wsA, "olga", tenancy.RoleOperator)
	c := f.assigned(f.wsA, olga)

	m, created, err := f.svc.PostMessage(f.ctx, f.wsA, operator(olga), c.ID, conversations.MessageInput{Body: "  Hello  "})
	if err != nil || !created {
		t.Fatalf("reply: %v", err)
	}
	if m.Body != "Hello" || m.Kind != conversations.KindText || m.Direction != conversations.DirectionOut ||
		m.SenderType != conversations.SenderOperator || *m.SenderID != olga || m.IsPrivate || m.DeliveryStatus != nil {
		t.Fatalf("reply: %+v", m)
	}
	after, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID)
	if after.FirstResponseAt == nil || !after.LastMessageAt.Equal(m.CreatedAt) {
		t.Fatalf("reply must set first response and activity: %+v", after)
	}

	// Another operator may leave a note but not reply.
	petr := f.member(f.wsA, "petr", tenancy.RoleOperator)
	note, _, err := f.svc.PostMessage(f.ctx, f.wsA, operator(petr), c.ID, conversations.MessageInput{Body: "check refund", Private: true})
	if err != nil || note.Kind != conversations.KindNote || !note.IsPrivate {
		t.Fatalf("note: %+v %v", note, err)
	}
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, operator(petr), c.ID, conversations.MessageInput{Body: "hi"}); !errors.Is(err, conversations.ErrNotAssignee) {
		t.Fatalf("foreign reply: %v", err)
	}
	// A note does not move the conversation up the inbox.
	if again, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, c.ID); !again.LastMessageAt.Equal(after.LastMessageAt) {
		t.Fatal("a note must not change last_message_at")
	}

	for name, in := range map[string]conversations.MessageInput{
		"empty":     {Body: "   "},
		"too long":  {Body: strings.Repeat("a", conversations.MaxBodyLength+1)},
		"duplicate": {Body: "x", AttachmentIDs: []uuid.UUID{c.ID, c.ID}},
	} {
		if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, operator(olga), c.ID, in); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}

	// Closed conversations take no messages.
	for _, to := range []conversations.Status{conversations.StatusResolved, conversations.StatusClosed} {
		if _, err := f.svc.Transition(f.ctx, f.wsA, operator(olga), c.ID, to, nil); err != nil {
			t.Fatalf("→ %s: %v", to, err)
		}
	}
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{Body: "late", Private: true}); !errors.Is(err, conversations.ErrClosed) {
		t.Fatalf("closed: %v", err)
	}
}

func TestPostMessage_IdempotencyKey(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	in := conversations.MessageInput{Body: "once", IdempotencyKey: "req-1"}

	first, created, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, in)
	if err != nil || !created {
		t.Fatalf("first: %v", err)
	}
	again, created, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, in)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("retry must return the first message: %+v created=%v %v", again, created, err)
	}
	in.Body = "different"
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, in); !errors.Is(err, conversations.ErrIdempotencyMismatch) {
		t.Fatalf("reused key: %v", err)
	}
	page, _ := f.svc.Messages(f.ctx, f.wsA, c.ID, "", 10)
	if n := len(withoutEvents(page.Items)); n != 1 {
		t.Fatalf("want one message, got %d", n)
	}
}

func TestMessages_PaginateNewestFirst(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	var want []uuid.UUID
	for i := range 7 {
		m, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{Body: "m", Private: i%2 == 0})
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		want = append([]uuid.UUID{m.ID}, want...)
	}
	var got []uuid.UUID
	cursor := ""
	for {
		page, err := f.svc.Messages(f.ctx, f.wsA, c.ID, cursor, 3)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		for _, m := range withoutEvents(page.Items) {
			got = append(got, m.ID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := f.svc.Messages(f.ctx, f.wsA, uuid.Must(uuid.NewV7()), "", 3); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("missing conversation: %v", err)
	}
}

func TestAttachments_UploadSendAndDownload(t *testing.T) {
	f := setup(t)
	c := f.assigned(f.wsA, f.ownerA)
	body := []byte("2026-10-04 crash at line 42\n")
	att := f.upload(f.wsA, c.ID, f.ownerA, body)
	if att.Status != conversations.AttachmentUploading || att.MessageID != nil {
		t.Fatalf("upload: %+v", att)
	}
	// Not sent yet: no download link.
	if _, err := f.svc.DownloadLink(f.ctx, f.wsA, att.ID); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("download before send: %v", err)
	}

	m, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{AttachmentIDs: []uuid.UUID{att.ID}})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if m.Kind != conversations.KindMedia || len(m.Attachments) != 1 || m.Attachments[0].Status != conversations.AttachmentReady {
		t.Fatalf("message: %+v", m)
	}
	page, _ := f.svc.Messages(f.ctx, f.wsA, c.ID, "", 10)
	if len(page.Items[0].Attachments) != 1 || page.Items[0].Attachments[0].FileName != "log.txt" {
		t.Fatalf("feed attachments: %+v", page.Items[0])
	}
	link, err := f.svc.DownloadLink(f.ctx, f.wsA, att.ID)
	if err != nil || link.Method != "GET" || !strings.Contains(link.URL, "/ws/"+f.wsA.String()+"/conv/") {
		t.Fatalf("download link: %+v %v", link, err)
	}

	// A file cannot be sent twice.
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{AttachmentIDs: []uuid.UUID{att.ID}}); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("resend: %v", err)
	}
	// Registered but never uploaded.
	up, err := f.svc.CreateUpload(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.UploadInput{FileName: "a.png", ContentType: "image/png", Size: 10})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{AttachmentIDs: []uuid.UUID{up.Attachment.ID}}); !errors.Is(err, conversations.ErrNotUploaded) {
		t.Fatalf("missing file: %v", err)
	}
	// An upload of another conversation does not fit this one.
	other := f.assigned(f.wsA, f.ownerA)
	foreign := f.upload(f.wsA, other.ID, f.ownerA, body)
	if _, _, err := f.svc.PostMessage(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.MessageInput{AttachmentIDs: []uuid.UUID{foreign.ID}}); !errors.Is(err, conversations.ErrInvalid) {
		t.Fatalf("other conversation's file: %v", err)
	}
}

func TestCreateUpload_Validates(t *testing.T) {
	f := setup(t)
	c := f.conversation(f.wsA)
	for name, in := range map[string]conversations.UploadInput{
		"html":       {FileName: "x.html", ContentType: "text/html", Size: 10},
		"svg":        {FileName: "x.svg", ContentType: "image/svg+xml", Size: 10},
		"too large":  {FileName: "x.zip", ContentType: "application/zip", Size: conversations.MaxAttachmentSize + 1},
		"empty":      {FileName: "x.txt", ContentType: "text/plain", Size: 0},
		"no name":    {ContentType: "text/plain", Size: 1},
		"bad digest": {FileName: "x.txt", ContentType: "text/plain", Size: 1, SHA256: []byte{1}},
	} {
		if _, err := f.svc.CreateUpload(f.ctx, f.wsA, admin(f.ownerA), c.ID, in); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	up, err := f.svc.CreateUpload(f.ctx, f.wsA, admin(f.ownerA), c.ID, conversations.UploadInput{
		FileName: "../../etc/passwd", ContentType: "text/plain; charset=utf-8", Size: 1,
	})
	if err != nil || up.Attachment.FileName != "passwd" || up.Attachment.ContentType != "text/plain" {
		t.Fatalf("sanitised upload: %+v %v", up.Attachment, err)
	}
}

// withoutEvents drops system lines (the assignment of the fixture).
func withoutEvents(msgs []conversations.Message) []conversations.Message {
	var out []conversations.Message
	for _, m := range msgs {
		if m.Kind != conversations.KindEvent {
			out = append(out, m)
		}
	}
	return out
}
