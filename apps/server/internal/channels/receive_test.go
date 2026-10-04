package channels

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
)

// A private text message, a photo and a group message as the Bot API sends them.
const (
	updText  = `{"update_id":100,"message":{"message_id":7,"from":{"id":70001,"is_bot":false,"first_name":"Пётр","last_name":"Соколов","username":"petr_s"},"chat":{"id":70001,"type":"private"},"date":1,"text":"Здравствуйте! Где заказ 4412?"}}`
	updPhoto = `{"update_id":101,"message":{"message_id":8,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":2,"caption":"Вот чек","photo":[{"file_id":"small","file_size":10},{"file_id":"big","file_size":1200}]}}`
	updGroup = `{"update_id":102,"message":{"message_id":9,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":-100500,"type":"supergroup"},"date":3,"text":"в группе"}}`
	updEdit  = `{"update_id":103,"edited_message":{"message_id":7,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":1,"edit_date":4,"text":"исправлено"}}`
)

// inboxOf returns the conversations of ws, newest first.
func (f *fixture) inboxOf(ws uuid.UUID) []conversations.Conversation {
	f.t.Helper()
	page, err := f.inbox.Conversations(f.ctx, ws, conversations.ListFilter{ViewerID: f.ownerA})
	if err != nil {
		f.t.Fatalf("conversations: %v", err)
	}
	return page.Items
}

func (f *fixture) feedOf(ws, conv uuid.UUID) []conversations.Message {
	f.t.Helper()
	page, err := f.inbox.Messages(f.ctx, ws, conv, "", 100)
	if err != nil {
		f.t.Fatalf("messages: %v", err)
	}
	return page.Items // newest first
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBotWebhook(t *testing.T) {
	f := setup(t)
	f.bots.Files = map[string][]byte{"photo.jpg": []byte("jpeg bytes of the receipt")}
	ch, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	secret := f.bots.Hooks[0].Secret

	if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, "wrong", []byte(updText)); !errors.Is(err, ErrHookSecret) {
		t.Fatalf("wrong secret: %v", err)
	}
	if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, "", []byte(updText)); !errors.Is(err, ErrHookSecret) {
		t.Fatalf("no secret: %v", err)
	}
	// The workspace in the path must be the channel's.
	if err := f.svc.BotWebhook(f.ctx, f.wsB, ch.ID, secret, []byte(updText)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace: %v", err)
	}
	if err := f.svc.BotWebhook(f.ctx, f.wsA, uuid.Must(uuid.NewV7()), secret, []byte(updText)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown channel: %v", err)
	}
	if len(f.inboxOf(f.wsA)) != 0 {
		t.Fatal("a rejected update was stored")
	}

	for _, u := range []string{updText, updPhoto, updGroup, updEdit, updText, `not json`} {
		if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, secret, []byte(u)); err != nil {
			t.Fatalf("webhook: %v", err)
		}
	}
	convs := f.inboxOf(f.wsA)
	if len(convs) != 1 || convs[0].ContactName != "Пётр Соколов" || convs[0].ChannelID == nil || *convs[0].ChannelID != ch.ID {
		t.Fatalf("conversations %+v", convs)
	}
	feed := f.feedOf(f.wsA, convs[0].ID)
	if len(feed) != 2 || feed[1].Body != "Здравствуйте! Где заказ 4412?" || feed[0].Body != "Вот чек" || len(feed[0].Attachments) != 1 {
		t.Fatalf("feed %+v", feed)
	}
	att := feed[0].Attachments[0]
	if att.Status != conversations.AttachmentReady || att.FileName != "photo.jpg" || att.ContentType != "image/jpeg" || att.Size != 25 {
		t.Fatalf("attachment %+v", att)
	}
	if l := f.list(f.wsA); l[0].LastInboundAt == nil {
		t.Fatal("last_inbound_at not set")
	}

	// A file Telegram does not give stays visible as rejected.
	missing := `{"update_id":104,"message":{"message_id":10,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":5,"document":{"file_id":"x","file_name":"смета.xlsx","mime_type":"application/vnd.ms-excel","file_size":2048}}}`
	if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, secret, []byte(missing)); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	feed = f.feedOf(f.wsA, convs[0].ID)
	if a := feed[0].Attachments; len(a) != 1 || a[0].Status != conversations.AttachmentRejected || a[0].FileName != "смета.xlsx" {
		t.Fatalf("missing file %+v", feed[0])
	}

	// After disconnecting, Telegram's late updates are not taken.
	if err := f.svc.Disconnect(f.ctx, f.wsA, ch.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, secret, []byte(updText)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after disconnect: %v", err)
	}
}

func TestReceivers_PollingBot(t *testing.T) {
	f := setup(t)
	rdb, _ := rdbtest.New(t)
	f.svc.opts.HookBaseURL = "http://localhost:8080"
	ch, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil || ch.Details.Delivery != DeliveryPolling {
		t.Fatalf("connect: %+v %v", ch, err)
	}
	// A webhook channel is not polled.
	if err := f.svc.BotWebhook(f.ctx, f.wsA, ch.ID, "", []byte(updText)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("webhook to a polling bot: %v", err)
	}
	f.bots.Push(withoutID(updText), withoutID(updGroup))

	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	opts := ReceiverOptions{System: f.sys.Pool(), Redis: rdb, Refresh: 50 * time.Millisecond, PollTimeout: time.Second} //nolint:forbidigo // the system role, as in cmd
	go func() { done <- f.svc.RunReceivers(ctx, opts) }()
	// A second worker competes for the same channel: the lease keeps one.
	go func() { _ = f.svc.RunReceivers(ctx, opts) }()

	eventually(t, "the polled message", func() bool { return len(f.inboxOf(f.wsA)) == 1 })
	f.bots.Push(`{"message":{"message_id":11,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":6,"text":"Ау"}}`)
	conv := f.inboxOf(f.wsA)[0].ID
	eventually(t, "the second message", func() bool { return len(f.feedOf(f.wsA, conv)) == 2 })

	// The token is revoked in @BotFather: the channel needs the admin.
	f.bots.Revoke()
	eventually(t, "the error status", func() bool {
		l := f.list(f.wsA)
		return l[0].Status == StatusError && l[0].ErrorCode == ErrorTokenRevoked
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("receivers: %v", err)
	}
	if n := len(f.feedOf(f.wsA, conv)); n != 2 {
		t.Fatalf("messages after two workers: %d", n)
	}
}

func TestReceivers_PersonalAccount(t *testing.T) {
	f := setup(t)
	rdb, _ := rdbtest.New(t)
	f.users.Feed, f.users.Stop, f.users.Ready = make(chan telegram.Message), make(chan error), make(chan struct{}, 1)
	f.users.Files = map[string][]byte{"договор.pdf": []byte("%PDF-1.7")}
	login, err := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, channelstest.AccountPhone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitCode(f.ctx, f.wsA, login.Channel.ID, channelstest.CodeOK); err != nil {
		t.Fatal(err)
	}
	id := login.Channel.ID

	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		opts := ReceiverOptions{System: f.sys.Pool(), Redis: rdb, Refresh: 50 * time.Millisecond} //nolint:forbidigo // the system role, as in cmd
		done <- f.svc.RunReceivers(ctx, opts)
	}()
	<-f.users.Ready
	if got := f.users.Sessions[len(f.users.Sessions)-1]; got != "s:"+channelstest.AccountPhone+"|code" {
		t.Fatalf("listened with session %q", got)
	}

	person := telegram.Person{ID: 80001, FirstName: "Ольга", Username: "olga_k"}
	// The owner wrote to a friend: no conversation, nothing stored.
	f.users.Feed <- telegram.Message{ID: 500, ChatID: 80002, From: telegram.Person{ID: 80002, FirstName: "Друг"}, Out: true, Text: "Привет!"}
	f.users.Feed <- telegram.Message{ID: 501, ChatID: 80001, From: person, Text: "Пришлите договор",
		Files: []telegram.File{{Name: "договор.pdf", MIME: "application/pdf", Size: 8}}}
	f.users.Feed <- telegram.Message{ID: 502, ChatID: 80001, From: person, Out: true, Text: "Отправил на почту"}
	f.users.Feed <- telegram.Message{ID: 503, ChatID: 80001, From: person, Unsupported: telegram.UnsupportedSticker}

	eventually(t, "the account's messages", func() bool {
		c := f.inboxOf(f.wsA)
		return len(c) == 1 && len(f.feedOf(f.wsA, c[0].ID)) == 3
	})
	conv := f.inboxOf(f.wsA)[0]
	feed := f.feedOf(f.wsA, conv.ID)
	if feed[2].Body != "Пришлите договор" || len(feed[2].Attachments) != 1 || feed[2].Attachments[0].Status != conversations.AttachmentReady {
		t.Fatalf("customer message %+v", feed[2])
	}
	if !feed[1].SentOutsidePanel || feed[1].SenderType != conversations.SenderOperator {
		t.Fatalf("owner message %+v", feed[1])
	}
	if feed[0].Unsupported != conversations.UnsupportedSticker {
		t.Fatalf("sticker %+v", feed[0])
	}
	r, _, err := f.files.Open(f.ctx, f.storageKey(feed[2].Attachments[0].ID))
	if err != nil {
		t.Fatalf("open stored file: %v", err)
	}
	stored, _ := io.ReadAll(r)
	_ = r.Close()
	if !bytes.Equal(stored, []byte("%PDF-1.7")) {
		t.Fatalf("stored %q", stored)
	}
	if pts := f.updatePts(id); pts != 5 {
		t.Fatalf("saved pts %d, want 5", pts)
	}

	// The session is ended in Telegram's Devices.
	f.users.Stop <- telegram.ErrSessionRevoked
	eventually(t, "the error status", func() bool {
		l := f.list(f.wsA)
		return l[0].Status == StatusError && l[0].ErrorCode == ErrorSessionRevoked
	})
	eventually(t, "the connection to close", func() bool { return f.users.Active() == 0 })
	if err := f.svc.Disconnect(f.ctx, f.wsA, id); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if pts := f.updatePts(id); pts != -1 {
		t.Fatalf("update state kept after disconnect: %d", pts)
	}
	cancel()
	<-done
}

// withoutID drops update_id: Bots.Push numbers the updates itself.
func withoutID(u string) string {
	return regexp.MustCompile(`"update_id":\d+,`).ReplaceAllString(u, "")
}

func (f *fixture) storageKey(attachmentID uuid.UUID) string {
	f.t.Helper()
	var key string
	f.ownerQuery("SELECT storage_key FROM attachments WHERE id = $1", []any{attachmentID}, &key)
	return key
}

// updatePts is the saved pts of a channel, -1 without a row.
func (f *fixture) updatePts(id uuid.UUID) int {
	f.t.Helper()
	pts := -1
	f.ownerQuery("SELECT pts FROM channel_update_state WHERE channel_id = $1", []any{id}, &pts)
	return pts
}

func (f *fixture) ownerQuery(sql string, args []any, dest ...any) {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	if err := conn.QueryRow(f.ctx, sql, args...).Scan(dest...); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		f.t.Fatalf("query: %v", err)
	}
}
