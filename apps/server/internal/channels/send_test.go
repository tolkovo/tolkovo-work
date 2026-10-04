package channels

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// sendSetup is setup with Redis and a job queue: replies are queued in the
// transaction that stores them, and tests run the job by hand.
func sendSetup(t *testing.T) *fixture {
	t.Helper()
	f := setup(t)
	rdb, _ := rdbtest.New(t)
	inserter, err := jobs.New(f.app.Pool(), jobs.Options{Log: f.svc.log}) //nolint:forbidigo // the job queue sits on the pool, as in cmd
	if err != nil {
		t.Fatal(err)
	}
	f.svc = New(f.app, Options{
		Sealer: f.sealer, Bots: f.bots, Users: f.users, HookBaseURL: "https://support.example.com",
		Inbox: f.inbox, Files: f.files, Log: f.svc.log, Jobs: inserter, Redis: rdb,
	})
	// Tests send pieces back to back; the per-chat limit is tested on its own.
	f.svc.limits.chat = ratelimit.Rule{Limit: 100, Window: time.Second}
	f.inbox.SetDeliverer(f.svc)
	f.rdb = rdb
	return f
}

var operator = func(f *fixture) conversations.Actor {
	return conversations.Actor{UserID: f.ownerA, AnyConversation: true}
}

// botConversation connects a bot and lets a customer write to it.
func (f *fixture) botConversation() (Channel, conversations.Conversation) {
	f.t.Helper()
	ch, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil {
		f.t.Fatalf("connect: %v", err)
	}
	if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, f.bots.Hooks[0].Secret, []byte(updText)); err != nil {
		f.t.Fatalf("webhook: %v", err)
	}
	c := f.inboxOf(f.wsA)
	if len(c) != 1 {
		f.t.Fatalf("conversations: %d", len(c))
	}
	return ch, c[0]
}

// reply posts an operator reply, with files given as name → bytes.
func (f *fixture) reply(conv uuid.UUID, body string, files ...[2]string) conversations.Message {
	f.t.Helper()
	var ids []uuid.UUID
	for _, file := range files {
		up, err := f.inbox.CreateUpload(f.ctx, f.wsA, operator(f), conv, conversations.UploadInput{
			FileName: file[0], ContentType: contentType(file[0]), Size: int64(len(file[1])),
		})
		if err != nil {
			f.t.Fatalf("upload: %v", err)
		}
		if _, err := f.files.Put(f.ctx, f.storageKey(up.Attachment.ID), strings.NewReader(file[1]), storage.PutOptions{
			ContentType: contentType(file[0]), MaxSize: int64(len(file[1])),
		}); err != nil {
			f.t.Fatalf("put: %v", err)
		}
		ids = append(ids, up.Attachment.ID)
	}
	m, _, err := f.inbox.PostMessage(f.ctx, f.wsA, operator(f), conv, conversations.MessageInput{Body: body, AttachmentIDs: ids})
	if err != nil {
		f.t.Fatalf("reply: %v", err)
	}
	return m
}

func contentType(name string) string {
	if strings.HasSuffix(name, ".jpg") {
		return "image/jpeg"
	}
	return "application/pdf"
}

func (f *fixture) deliver(msg uuid.UUID, last bool) error {
	return f.svc.send(f.ctx, SendArgs{WorkspaceID: f.wsA, MessageID: msg}, last)
}

func (f *fixture) message(conv, id uuid.UUID) conversations.Message {
	f.t.Helper()
	for _, m := range f.feedOf(f.wsA, conv) {
		if m.ID == id {
			return m
		}
	}
	f.t.Fatalf("message %s not in the feed", id)
	return conversations.Message{}
}

func status(m conversations.Message) string {
	if m.DeliveryStatus == nil {
		return ""
	}
	return *m.DeliveryStatus
}

func (f *fixture) ownerExec(sql string, args ...any) {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	if _, err := conn.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("exec: %v", err)
	}
}

func (f *fixture) count(sql string, args ...any) int {
	f.t.Helper()
	var n int
	f.ownerQuery(sql, args, &n)
	return n
}

func TestSend_Bot(t *testing.T) {
	f := sendSetup(t)
	_, conv := f.botConversation()
	long := strings.Repeat("Ваш заказ 4412 передан в доставку. ", 150) // > 4096 characters
	m := f.reply(conv.ID, long, [2]string{"чек.jpg", "jpeg bytes"}, [2]string{"договор.pdf", "%PDF-1.7"})
	if status(m) != conversations.DeliveryPending {
		t.Fatalf("a reply waits for its channel: %q", status(m))
	}
	if n := f.count("SELECT count(*) FROM river.river_job WHERE kind = 'channels.send' AND args->>'message_id' = $1", m.ID.String()); n != 1 {
		t.Fatalf("the reply must be queued with it: %d jobs", n)
	}
	// A note is not delivered.
	note, _, err := f.inbox.PostMessage(f.ctx, f.wsA, operator(f), conv.ID, conversations.MessageInput{Body: "внутреннее", Private: true})
	if err != nil || note.DeliveryStatus != nil {
		t.Fatalf("note: %+v %v", note.DeliveryStatus, err)
	}

	if err := f.deliver(m.ID, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := f.bots.SnapshotSent()
	if len(sent) != 4 {
		t.Fatalf("two pieces of text and two files, got %d", len(sent))
	}
	if sent[0].ChatID != 70001 || len([]rune(sent[0].Text)) > telegram.MaxTextLen || sent[0].Text+" "+sent[1].Text != strings.TrimSpace(long) {
		t.Fatalf("text split: %d + %d characters", len([]rune(sent[0].Text)), len([]rune(sent[1].Text)))
	}
	if sent[2].File != "чек.jpg" || !sent[2].Photo || string(sent[2].Data) != "jpeg bytes" || sent[3].File != "договор.pdf" || sent[3].Photo {
		t.Fatalf("files: %+v %+v", sent[2], sent[3])
	}
	got := f.message(conv.ID, m.ID)
	if status(got) != conversations.DeliverySent || got.DeliveryError != "" {
		t.Fatalf("delivered: %q %q", status(got), got.DeliveryError)
	}
	if n := f.count("SELECT count(*) FROM outbox_events WHERE topic = 'message.delivery' AND payload->>'message_id' = $1 AND payload->>'status' = 'sent'", m.ID.String()); n != 1 {
		t.Fatalf("message.delivery: %d", n)
	}
	// The job again (River redelivers after a crash): nothing is sent twice.
	if err := f.deliver(m.ID, false); err != nil || len(f.bots.SnapshotSent()) != 4 {
		t.Fatalf("a delivered reply is left alone: %v, %d sent", err, len(f.bots.SnapshotSent()))
	}
}

// A reply in a report conversation is out once stored: the app fetches it
// (AP-06); nothing goes to Telegram.
func TestSend_App(t *testing.T) {
	f := sendSetup(t)
	app, err := f.svc.ConnectApp(f.ctx, f.wsA, f.ownerA, "Зерно")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	got, err := f.inbox.ReceiveReport(f.ctx, f.wsA, conversations.AppReport{
		ChannelID: app.Channel.ID, ReportID: uuid.Must(uuid.NewV7()), Body: "Не синхронизируется корзина",
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	m := f.reply(got.ConversationID, "Исправили в 2.4.2")
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	if status(f.message(got.ConversationID, m.ID)) != conversations.DeliverySent || len(f.bots.SnapshotSent()) != 0 {
		t.Fatalf("delivered: %q, %d sent to Telegram", status(f.message(got.ConversationID, m.ID)), len(f.bots.SnapshotSent()))
	}
}

func TestSend_BotFailures(t *testing.T) {
	f := sendSetup(t)
	ch, conv := f.botConversation()

	// Blocked: failed for good, the operator sees why.
	f.bots.FailNext(&telegram.UndeliverableError{Reason: telegram.ReasonBlocked, Detail: "Forbidden: bot was blocked by the user"})
	m := f.reply(conv.ID, "Здравствуйте!")
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatalf("a refusal ends the job: %v", err)
	}
	if got := f.message(conv.ID, m.ID); status(got) != conversations.DeliveryFailed || got.DeliveryError != conversations.DeliveryBlocked {
		t.Fatalf("blocked: %q %q", status(got), got.DeliveryError)
	}
	// The customer unblocked: the operator sends it again.
	if _, err := f.inbox.RetryDelivery(f.ctx, f.wsA, operator(f), conv.ID, m.ID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := f.inbox.RetryDelivery(f.ctx, f.wsA, operator(f), conv.ID, m.ID); !errors.Is(err, conversations.ErrNotFailed) {
		t.Fatalf("a pending reply is not retried: %v", err)
	}
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := f.message(conv.ID, m.ID); status(got) != conversations.DeliverySent || got.DeliveryError != "" {
		t.Fatalf("after retry: %q %q", status(got), got.DeliveryError)
	}

	// 429: the job sleeps as long as Telegram asks, without spending an attempt.
	f.bots.FailNext(&telegram.APIError{Method: "sendMessage", Code: 429, RetryAfter: 7 * time.Second})
	m2 := f.reply(conv.ID, "Минуту")
	var snooze *river.JobSnoozeError
	if err := f.deliver(m2.ID, false); !errors.As(err, &snooze) || snooze.Duration != 7*time.Second {
		t.Fatalf("429: %v", err)
	}
	// A reply after it waits for its turn.
	m3 := f.reply(conv.ID, "Проверяю")
	if err := f.deliver(m3.ID, false); !errors.As(err, &snooze) || snooze.Duration != waitTurn {
		t.Fatalf("a later reply must not overtake: %v", err)
	}
	// Network trouble: retried, and failed after the last attempt.
	f.bots.FailNext(errors.New("connection reset"), errors.New("connection reset"))
	if err := f.deliver(m2.ID, false); err == nil || errors.As(err, &snooze) {
		t.Fatalf("a network error is retried: %v", err)
	}
	if err := f.deliver(m2.ID, true); err != nil {
		t.Fatalf("the last attempt fails the reply: %v", err)
	}
	if got := f.message(conv.ID, m2.ID); got.DeliveryError != conversations.DeliveryGaveUp {
		t.Fatalf("gave up: %q %q", status(got), got.DeliveryError)
	}
	if err := f.deliver(m3.ID, false); err != nil || status(f.message(conv.ID, m3.ID)) != conversations.DeliverySent {
		t.Fatalf("the next reply goes once the earlier one failed: %v", err)
	}

	// The token was revoked in @BotFather: the channel needs the admin.
	f.bots.Revoke()
	m4 := f.reply(conv.ID, "Ещё вопрос?")
	if err := f.deliver(m4.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := f.message(conv.ID, m4.ID); got.DeliveryError != conversations.DeliveryChannelUnavailable {
		t.Fatalf("revoked: %q %q", status(got), got.DeliveryError)
	}
	if l := f.list(f.wsA); l[0].ID != ch.ID || l[0].Status != StatusError || l[0].ErrorCode != ErrorTokenRevoked {
		t.Fatalf("channel: %+v", l[0])
	}
}

func TestSend_ChatLimit(t *testing.T) {
	f := sendSetup(t)
	f.svc.limits.chat = ratelimit.Rule{Limit: 1, Window: time.Minute}
	_, conv := f.botConversation()
	m := f.reply(conv.ID, "Первое", [2]string{"договор.pdf", "%PDF"})
	var snooze *river.JobSnoozeError
	if err := f.deliver(m.ID, false); !errors.As(err, &snooze) || snooze.Duration <= 0 {
		t.Fatalf("the second piece waits for the chat's limit: %v", err)
	}
	if sent := f.bots.SnapshotSent(); len(sent) != 1 {
		t.Fatalf("one piece went: %d", len(sent))
	}
	f.svc.limits.chat = ratelimit.Rule{Limit: 100, Window: time.Second}
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if sent := f.bots.SnapshotSent(); len(sent) != 2 || sent[1].File != "договор.pdf" {
		t.Fatalf("the rest goes on, the first piece is not repeated: %+v", sent)
	}
}

func TestSend_PersonalAccount(t *testing.T) {
	f := sendSetup(t)
	conn := &channelstest.UserConn{}
	f.users.Feed, f.users.Stop, f.users.Ready = make(chan telegram.Message), make(chan error), make(chan struct{}, 1)
	f.users.Reads, f.users.Conn = make(chan [2]int64), conn
	login, err := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, channelstest.AccountPhone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitCode(f.ctx, f.wsA, login.Channel.ID, channelstest.CodeOK); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() {
		opts := ReceiverOptions{System: f.sys.Pool(), Redis: f.rdb, Refresh: 50 * time.Millisecond} //nolint:forbidigo // the system role, as in cmd
		done <- f.svc.RunReceivers(ctx, opts)
	}()
	<-f.users.Ready
	person := telegram.Person{ID: 80001, FirstName: "Ольга"}
	f.users.Feed <- telegram.Message{ID: 41, ChatID: 80001, From: person, Text: "Когда доставка?"}
	eventually(t, "the conversation", func() bool { return len(f.inboxOf(f.wsA)) == 1 })
	conv := f.inboxOf(f.wsA)[0]

	m := f.reply(conv.ID, "Завтра до 18:00", [2]string{"чек.jpg", "jpeg"})
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent := conn.SnapshotSent()
	if len(sent) != 2 || sent[0].ChatID != 80001 || sent[0].Text != "Завтра до 18:00" || sent[1].File != "чек.jpg" || !sent[1].Photo {
		t.Fatalf("sent through the live connection: %+v", sent)
	}
	if sent[0].RandomID == sent[1].RandomID || sent[0].RandomID != randomID(m.ID, 0) {
		t.Fatalf("random ids: %d %d", sent[0].RandomID, sent[1].RandomID)
	}
	if f.users.OneShots() != 0 {
		t.Fatal("the live connection is used when this worker holds it")
	}
	got := f.message(conv.ID, m.ID)
	if status(got) != conversations.DeliverySent {
		t.Fatalf("status %q", status(got))
	}
	// Telegram brought both messages back as the owner's: they are not stored again.
	f.users.Feed <- telegram.Message{ID: 42, ChatID: 80001, From: person, Text: "Спасибо"}
	eventually(t, "the next customer message", func() bool { return len(f.feedOf(f.wsA, conv.ID)) == 3 })
	for _, msg := range f.feedOf(f.wsA, conv.ID) {
		if msg.SentOutsidePanel {
			t.Fatalf("an echo of our reply was stored as the owner's: %+v", msg)
		}
	}

	// The customer read the chat.
	f.users.Reads <- [2]int64{80001, 9002}
	eventually(t, "read", func() bool { return status(f.message(conv.ID, m.ID)) == conversations.DeliveryRead })

	// The operator typed and read: Telegram shows both.
	f.svc.ReplyTyping(f.ctx, f.wsA, conv.ID)
	f.svc.ReplyTyping(f.ctx, f.wsA, conv.ID) // within typingEvery: once
	f.svc.ConversationRead(f.ctx, f.wsA, conv.ID)
	eventually(t, "typing and read", func() bool {
		return len(conn.Typed()) == 1 && slices.Contains(conn.SnapshotReads(), [2]int64{80001, 42})
	})

	// A chat the account never saw a person in cannot be written to.
	m2 := f.reply(conv.ID, "Ещё")
	f.ownerExec("UPDATE channel_peers SET access_hash = 1 WHERE channel_id = $1", login.Channel.ID)
	if err := f.deliver(m2.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := f.message(conv.ID, m2.ID); got.DeliveryError != conversations.DeliveryChatNotFound {
		t.Fatalf("stale peer: %q %q", status(got), got.DeliveryError)
	}
	f.ownerExec("DELETE FROM channel_peers WHERE channel_id = $1", login.Channel.ID)
	m3 := f.reply(conv.ID, "И ещё")
	if err := f.deliver(m3.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := f.message(conv.ID, m3.ID); got.DeliveryError != conversations.DeliveryChatNotFound {
		t.Fatalf("unknown peer: %q %q", status(got), got.DeliveryError)
	}

	// Another worker holds the account: the reply goes through a connection of its own.
	cancel()
	<-done
	f.ownerExec("INSERT INTO channel_peers (workspace_id, channel_id, user_id, access_hash) VALUES ($1, $2, 80001, $3)",
		f.wsA, login.Channel.ID, channelstest.PeerHash(80001))
	m4 := f.reply(conv.ID, "Курьер выехал")
	if err := f.deliver(m4.ID, false); err != nil {
		t.Fatal(err)
	}
	if f.users.OneShots() != 1 || status(f.message(conv.ID, m4.ID)) != conversations.DeliverySent {
		t.Fatalf("one-shot connection: %d, %q", f.users.OneShots(), status(f.message(conv.ID, m4.ID)))
	}
	// The session was ended in Telegram meanwhile.
	conn.FailNext(telegram.ErrSessionRevoked)
	m5 := f.reply(conv.ID, "Алло?")
	if err := f.deliver(m5.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := f.message(conv.ID, m5.ID); got.DeliveryError != conversations.DeliveryChannelUnavailable {
		t.Fatalf("session revoked: %q", got.DeliveryError)
	}
	if l := f.list(f.wsA); l[0].ErrorCode != ErrorSessionRevoked {
		t.Fatalf("channel: %+v", l[0])
	}
	// A disconnected channel takes its peers along.
	if err := f.svc.Disconnect(f.ctx, f.wsA, login.Channel.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.count("SELECT count(*) FROM channel_peers WHERE channel_id = $1", login.Channel.ID); n != 0 {
		t.Fatalf("peers left: %d", n)
	}
}

func TestSend_BotTyping(t *testing.T) {
	f := sendSetup(t)
	_, conv := f.botConversation()
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	go func() { f.svc.runActions(ctx) }()
	// Subscribing takes a moment: publish until the first one is heard.
	eventually(t, "typing", func() bool {
		f.svc.ReplyTyping(f.ctx, f.wsA, conv.ID)
		return len(f.bots.Typed()) > 0
	})
	f.svc.ReplyTyping(f.ctx, f.wsA, conv.ID)
	f.svc.ConversationRead(f.ctx, f.wsA, conv.ID) // a bot cannot mark read
	time.Sleep(200 * time.Millisecond)
	if typed := f.bots.Typed(); len(typed) != 1 || typed[0] != 70001 {
		t.Fatalf("typing at most every %s: %v", typingEvery, typed)
	}
}

func TestSplitText(t *testing.T) {
	long := strings.Repeat("слово ", 1000) + "\n" + strings.Repeat("я", 5000)
	parts := telegram.SplitText(long)
	var total int
	for _, p := range parts {
		if n := len([]rune(p)); n == 0 || n > telegram.MaxTextLen {
			t.Fatalf("piece of %d characters", n)
		}
		total += len([]rune(p))
	}
	if len(parts) < 3 || !strings.HasPrefix(parts[0], "слово") || strings.HasSuffix(parts[0], " ") {
		t.Fatalf("pieces: %d", len(parts))
	}
	if got := telegram.SplitText("коротко"); len(got) != 1 || got[0] != "коротко" {
		t.Fatalf("short: %v", got)
	}
}
