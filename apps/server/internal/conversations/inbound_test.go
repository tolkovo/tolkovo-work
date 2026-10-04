package conversations_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// channel inserts a bot channel of ws behind the channels module's back.
func (f *fixture) channel(ws uuid.UUID) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	f.ownerExec("INSERT INTO channels (id, workspace_id, type, name, status) VALUES ($1, $2, 'telegram_bot', 'Бот', 'active')", id, ws)
	return id
}

func inbound(ch uuid.UUID, msgID, body string) conversations.Inbound {
	return conversations.Inbound{
		ChannelID: ch, Thread: "70001", ExternalID: msgID, Body: body,
		Sender: conversations.InboundSender{ExternalUserID: "70001", Name: "Пётр Соколов", Username: "petr_s"},
	}
}

func (f *fixture) receive(ws uuid.UUID, in conversations.Inbound) conversations.Received {
	f.t.Helper()
	res, err := f.svc.Receive(f.ctx, ws, in)
	if err != nil {
		f.t.Fatalf("receive %s: %v", in.ExternalID, err)
	}
	return res
}

func (f *fixture) feed(ws, conv uuid.UUID) []conversations.Message {
	f.t.Helper()
	page, err := f.svc.Messages(f.ctx, ws, conv, "", 100)
	if err != nil {
		f.t.Fatalf("messages: %v", err)
	}
	slices.Reverse(page.Items)
	return page.Items
}

func TestReceive_ThreadAndDeduplication(t *testing.T) {
	f := setup(t)
	ch := f.channel(f.wsA)

	first := f.receive(f.wsA, inbound(ch, "1", "  Здравствуйте, заказ 4412 не пришёл  "))
	if !first.Stored || !first.CreatedConversation {
		t.Fatalf("first message: %+v", first)
	}
	conv, err := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, first.ConversationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if conv.Status != conversations.StatusQueued || conv.ChannelID == nil || *conv.ChannelID != ch ||
		conv.ContactName != "Пётр Соколов" || conv.UnreadCount != 1 {
		t.Fatalf("conversation %+v", conv)
	}
	if got := f.events(f.wsA); !slices.Equal(got, []string{"contact.created", "conversation.created", "message.created"}) {
		t.Fatalf("events %v", got)
	}

	second := f.receive(f.wsA, inbound(ch, "2", "Номер телефона +7 900 000-00-00"))
	if !second.Stored || second.CreatedConversation || second.ConversationID != first.ConversationID {
		t.Fatalf("second message: %+v", second)
	}
	// Telegram repeats an update: nothing new.
	if again := f.receive(f.wsA, inbound(ch, "2", "Номер телефона +7 900 000-00-00")); again.Stored {
		t.Fatalf("duplicate stored: %+v", again)
	}
	seen, open, err := f.svc.InboundState(f.ctx, f.wsA, ch, "70001", "2")
	if err != nil || !seen || !open {
		t.Fatalf("state seen=%v open=%v err=%v", seen, open, err)
	}
	msgs := f.feed(f.wsA, first.ConversationID)
	if len(msgs) != 2 || msgs[0].Body != "Здравствуйте, заказ 4412 не пришёл" ||
		msgs[0].Direction != conversations.DirectionIn || msgs[0].SenderType != conversations.SenderContact ||
		msgs[0].SenderID == nil || *msgs[0].SenderID != conv.ContactID || msgs[0].DeliveryStatus != nil {
		t.Fatalf("feed %+v", msgs)
	}

	// Another person is another contact and conversation.
	other := inbound(ch, "1", "Добрый день")
	other.Thread, other.Sender = "70002", conversations.InboundSender{ExternalUserID: "70002", Name: "Ира"}
	if res := f.receive(f.wsA, other); !res.CreatedConversation || res.ConversationID == first.ConversationID {
		t.Fatalf("other person: %+v", res)
	}

	// A channel of another workspace cannot receive here.
	if _, err := f.svc.Receive(f.ctx, f.wsB, inbound(ch, "9", "чужое")); err == nil {
		t.Fatal("received into another workspace's channel")
	}
}

func TestReceive_WakesTheConversationUp(t *testing.T) {
	f := setup(t)
	n := &recordingNotifier{}
	f.svc.SetNotifier(n)
	ch := f.channel(f.wsA)
	op := f.member(f.wsA, "olga", tenancy.RoleOperator)

	res := f.receive(f.wsA, inbound(ch, "1", "Привет"))
	if got := n.users(); len(got) != 0 {
		t.Fatalf("queued conversation notified %v", got)
	}
	id := res.ConversationID
	if _, err := f.svc.Assign(f.ctx, f.wsA, operator(op), id, &op, nil); err != nil {
		t.Fatalf("take: %v", err)
	}
	if _, err := f.svc.Transition(f.ctx, f.wsA, operator(op), id, conversations.StatusPending, nil); err != nil {
		t.Fatalf("pending: %v", err)
	}

	// The customer answers: waiting is over, the operator is reminded.
	f.receive(f.wsA, inbound(ch, "2", "Вот номер заказа"))
	c, _ := f.svc.Conversation(f.ctx, f.wsA, op, id)
	if c.Status != conversations.StatusAssigned || c.AssigneeID == nil || *c.AssigneeID != op {
		t.Fatalf("after pending: %s %v", c.Status, c.AssigneeID)
	}
	if got := n.users(); !slices.Equal(got, []uuid.UUID{op}) {
		t.Fatalf("notified %v", got)
	}

	// Resolved, then the customer writes: back to the same operator.
	if _, err := f.svc.Transition(f.ctx, f.wsA, operator(op), id, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	f.receive(f.wsA, inbound(ch, "3", "Ещё вопрос"))
	c, _ = f.svc.Conversation(f.ctx, f.wsA, op, id)
	if c.Status != conversations.StatusAssigned || *c.AssigneeID != op || c.ResolvedAt != nil {
		t.Fatalf("reopened: %s %v %v", c.Status, c.AssigneeID, c.ResolvedAt)
	}
	feed := f.feed(f.wsA, id)
	var lines []string
	for _, m := range feed {
		if m.Event != nil && m.SenderID == nil {
			lines = append(lines, string(m.Event.From)+"→"+string(m.Event.To))
		}
	}
	if !slices.Equal(lines, []string{"pending→assigned", "resolved→assigned"}) {
		t.Fatalf("system lines by the system: %v", lines)
	}

	// The operator left the workspace: a reopened conversation goes to the queue.
	if _, err := f.svc.Transition(f.ctx, f.wsA, operator(op), id, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	f.ownerExec("UPDATE memberships SET is_active = false WHERE workspace_id = $1 AND user_id = $2", f.wsA, op)
	f.receive(f.wsA, inbound(ch, "4", "Алло?"))
	c, _ = f.svc.Conversation(f.ctx, f.wsA, f.ownerA, id)
	if c.Status != conversations.StatusQueued || c.AssigneeID != nil {
		t.Fatalf("reopened without the operator: %s %v", c.Status, c.AssigneeID)
	}

	// Closed is final: the next message starts a new conversation with the
	// same contact, and old message ids still count as seen.
	if _, err := f.svc.Assign(f.ctx, f.wsA, admin(f.ownerA), id, &f.ownerA, nil); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), id, conversations.StatusResolved, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := f.svc.Transition(f.ctx, f.wsA, admin(f.ownerA), id, conversations.StatusClosed, nil); err != nil {
		t.Fatalf("close: %v", err)
	}
	if again := f.receive(f.wsA, inbound(ch, "4", "Алло?")); again.Stored {
		t.Fatal("a message of the closed conversation was stored again")
	}
	next := f.receive(f.wsA, inbound(ch, "5", "Новый вопрос"))
	if !next.CreatedConversation || next.ConversationID == id {
		t.Fatalf("after close: %+v", next)
	}
	nc, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, next.ConversationID)
	if nc.ContactID != c.ContactID {
		t.Fatal("the same person became another contact")
	}
}

func TestReceive_OwnerReplies(t *testing.T) {
	f := setup(t)
	ch := f.channel(f.wsA)
	owner := inbound(ch, "10", "Сейчас проверю")
	owner.FromOwner = true

	// No open conversation: a private chat of the owner stays private.
	if res := f.receive(f.wsA, owner); res.Stored {
		t.Fatalf("owner message without a conversation stored: %+v", res)
	}
	res := f.receive(f.wsA, inbound(ch, "11", "Где мой заказ?"))
	owner.ExternalID = "12"
	reply := f.receive(f.wsA, owner)
	if !reply.Stored || reply.ConversationID != res.ConversationID {
		t.Fatalf("owner reply: %+v", reply)
	}
	msgs := f.feed(f.wsA, res.ConversationID)
	last := msgs[len(msgs)-1]
	if last.SenderType != conversations.SenderOperator || last.SenderID != nil || last.Direction != conversations.DirectionOut ||
		!last.SentOutsidePanel || last.DeliveryStatus == nil || *last.DeliveryStatus != "sent" {
		t.Fatalf("owner reply message %+v", last)
	}
	c, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, res.ConversationID)
	if c.FirstResponseAt == nil || c.Status != conversations.StatusQueued {
		t.Fatalf("conversation after the owner reply: %+v", c)
	}
}

func TestReceive_FilesNamesAndUnsupported(t *testing.T) {
	f := setup(t)
	ch := f.channel(f.wsA)

	key, err := storage.NewKey(f.wsA, storage.KindConversation)
	if err != nil {
		t.Fatal(err)
	}
	photo := []byte("jpeg bytes")
	if _, err := f.files.Put(f.ctx, key, bytes.NewReader(photo), storage.PutOptions{MaxSize: 100}); err != nil {
		t.Fatal(err)
	}
	in := inbound(ch, "1", "")
	in.Files = []conversations.ReceivedFile{
		{StorageKey: key, FileName: "photo.jpg", ContentType: "image/jpeg", Size: int64(len(photo))},
		{FileName: "video.mp4", ContentType: "video/mp4", Size: 30 << 20, Rejected: true},
	}
	res := f.receive(f.wsA, in)
	msgs := f.feed(f.wsA, res.ConversationID)
	if len(msgs) != 1 || msgs[0].Kind != conversations.KindMedia || len(msgs[0].Attachments) != 2 {
		t.Fatalf("media message %+v", msgs)
	}
	if a := msgs[0].Attachments; a[0].Status != conversations.AttachmentReady || a[0].FileName != "photo.jpg" ||
		a[1].Status != conversations.AttachmentRejected || a[1].Size != 30<<20 {
		t.Fatalf("attachments %+v", a)
	}

	sticker := inbound(ch, "2", "")
	sticker.Unsupported = conversations.UnsupportedSticker
	f.receive(f.wsA, sticker)
	msgs = f.feed(f.wsA, res.ConversationID)
	if got := msgs[len(msgs)-1]; got.Unsupported != conversations.UnsupportedSticker || got.Body != "" {
		t.Fatalf("sticker %+v", got)
	}

	// The contact keeps the name an operator gave it; the username follows Telegram.
	c, _ := f.svc.Conversation(f.ctx, f.wsA, f.ownerA, res.ConversationID)
	name := "Пётр (опт)"
	if _, err := f.svc.UpdateContact(f.ctx, f.wsA, c.ContactID, conversations.ContactPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	renamed := inbound(ch, "3", "ок")
	renamed.Sender.Name, renamed.Sender.Username = "Петя", "petya"
	f.receive(f.wsA, renamed)
	got, _ := f.svc.Contact(f.ctx, f.wsA, c.ContactID)
	if got.Name != name {
		t.Fatalf("contact name %q", got.Name)
	}

	empty := inbound(ch, "4", "  ")
	if _, err := f.svc.Receive(f.ctx, f.wsA, empty); err == nil {
		t.Fatal("empty message stored")
	}
}
