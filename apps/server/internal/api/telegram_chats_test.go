package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// linkTelegram links c's Telegram to the workspace's notifications bot as
// chat, the way the member does it with Start.
func linkTelegram(t *testing.T, e *env, c *client, chat int64) {
	t.Helper()
	link := decode[oapi.TelegramLinkStart](t, c.do(http.MethodPost, "/api/v1/me/telegram/link", nil))
	code, _ := strings.CutPrefix(link.Url, "https://t.me/other_bot?start=")
	id := strconv.FormatInt(chat, 10)
	e.bots.Push(`{"message":{"message_id":1,"from":{"id":` + id + `,"first_name":"X"},"chat":{"id":` + id +
		`,"type":"private"},"text":"/start ` + code + `"}}`)
	time.Sleep(notifications.CheckInterval) // one read of the bot per interval
	if me := decode[oapi.MyTelegram](t, c.do(http.MethodPost, "/api/v1/me/telegram/check", nil)); !me.Linked {
		t.Fatalf("link chat %d: %+v", chat, me)
	}
}

// chatJobs are the queued Telegram messages about conversations.
func chatJobs(t *testing.T, e *env, ws uuid.UUID) []notifications.ChatMessageArgs {
	t.Helper()
	var raw []byte
	e.ownerScan(&raw, `SELECT coalesce(json_agg(args ORDER BY id), '[]') FROM river.river_job
		WHERE kind = 'notifications.telegram_chat' AND args->>'workspace_id' = $1`, ws.String())
	var out []notifications.ChatMessageArgs
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("job args: %v %s", err, raw)
	}
	return out
}

// The notifications bot tells members about conversations while their
// panel is closed: a customer's conversation in the queue, one given to
// them, the customer writing in theirs; the name and the channel, never
// the text (docs/06 I-06).
func TestTelegram_Conversations(t *testing.T) {
	in := setupInbox(t)
	e, ws, op, opID := in.e, in.so.Workspace.Id, in.op, in.sop.User.Id
	dev, _ := in.joinAs(t, "dev@example.test", "dev-home", tenancy.RoleProgrammer)
	decode[oapi.TelegramBotState](t, in.owner.do(http.MethodPut, "/api/v1/task-settings/telegram-bot", map[string]any{"token": channelstest.OtherBotToken}))
	linkTelegram(t, e, op, 555)
	linkTelegram(t, e, dev, 777)
	ch := uuid.Must(uuid.NewV7())
	e.ownerExec("INSERT INTO channels (id, workspace_id, type, name, status) VALUES ($1, $2, 'telegram_bot', 'Бот поддержки', 'active')", ch, ws)

	open := map[uuid.UUID]bool{}
	worker := notifications.TelegramWorkerOptions{
		Service: e.notifications, Tasks: e.tasks, Conversations: e.convs, PanelURL: panelURL,
		Panels: func(context.Context, uuid.UUID) (map[uuid.UUID]bool, error) { return open, nil },
	}
	send := func(a notifications.ChatMessageArgs) (sent bool, text string) {
		t.Helper()
		before := len(e.bots.SnapshotSent())
		if err := notifications.SendChatMessage(context.Background(), worker, a); err != nil {
			t.Fatalf("send %+v: %v", a, err)
		}
		all := e.bots.SnapshotSent()
		if len(all) == before {
			return false, ""
		}
		if all[len(all)-1].ChatID != 555 {
			t.Fatalf("sent to chat %d", all[len(all)-1].ChatID)
		}
		return true, all[len(all)-1].Text
	}
	write := func(thread, id, text string) uuid.UUID {
		t.Helper()
		res, err := e.convs.Receive(context.Background(), ws, conversations.Inbound{
			ChannelID: ch, Thread: thread, ExternalID: id, Body: text,
			Sender: conversations.InboundSender{ExternalUserID: thread, Name: "Иван"},
		})
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		return res.ConversationID
	}

	// A customer writes: the queue is news to the operator, not to the
	// programmer, who answers nobody.
	conv := write("9001", "1", "Пароль от кабинета 12345")
	jobs := chatJobs(t, e, ws)
	if len(jobs) != 1 || jobs[0].UserID != opID || jobs[0].About != notifications.ChatQueue || jobs[0].ConversationID != conv {
		t.Fatalf("queue jobs: %+v", jobs)
	}
	// The panel is open: it tells the member itself.
	open[opID] = true
	if sent, _ := send(jobs[0]); sent {
		t.Fatal("sent with the panel open")
	}
	open[opID] = false
	sent, text := send(jobs[0])
	if !sent || !strings.Contains(text, "Новый диалог в очереди") || !strings.Contains(text, "Иван · Бот поддержки") ||
		!strings.Contains(text, panelURL+"/inbox/"+conv.String()) || strings.Contains(text, "12345") {
		t.Fatalf("queue message: %v %q", sent, text)
	}

	// Given to the operator by the owner: news while it stays theirs.
	c := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+conv.String(), nil))
	if rec := in.owner.do(http.MethodPut, "/api/v1/conversations/"+conv.String()+"/assignee",
		map[string]any{"assignee_id": opID, "version": c.Version}); rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body)
	}
	if sent, _ := send(jobs[0]); sent {
		t.Fatal("the queue message went out for a conversation no longer queued")
	}
	jobs = chatJobs(t, e, ws)
	assigned := jobs[len(jobs)-1]
	if assigned.About != notifications.ChatAssigned || assigned.UserID != opID {
		t.Fatalf("assigned job: %+v", assigned)
	}
	if sent, text := send(assigned); !sent || !strings.Contains(text, "Вам передан диалог") {
		t.Fatalf("assigned message: %v %q", sent, text)
	}

	// The customer writes twice: one message within ChatMessageEvery.
	write("9001", "2", "Вы тут?")
	write("9001", "3", "Ау")
	jobs = chatJobs(t, e, ws)
	var news []notifications.ChatMessageArgs
	for _, j := range jobs {
		if j.About == notifications.ChatMessage {
			news = append(news, j)
		}
	}
	if len(news) != 1 {
		t.Fatalf("message jobs: %+v", news)
	}
	if sent, text := send(news[0]); !sent || !strings.Contains(text, "Новое сообщение в диалоге") || strings.Contains(text, "Ау") {
		t.Fatalf("message: %v %q", sent, text)
	}
	// Read in the panel meanwhile: no longer news.
	feed := decode[oapi.MessagePage](t, op.do(http.MethodGet, "/api/v1/conversations/"+conv.String()+"/messages", nil))
	if rec := op.do(http.MethodPost, "/api/v1/conversations/"+conv.String()+"/read",
		map[string]any{"up_to_message_id": feed.Items[0].Id}); rec.Code != http.StatusNoContent {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	if sent, _ := send(news[0]); sent {
		t.Fatal("sent about messages already read")
	}

	// The member turns the queue off: a new customer is not news to them.
	got := decode[oapi.NotificationSettings](t, op.do(http.MethodPatch, "/api/v1/me/notifications", map[string]any{"telegram_queue": false}))
	if got.TelegramQueue || !got.TelegramAssigned || !got.TelegramMessages {
		t.Fatalf("settings: %+v", got)
	}
	before := len(chatJobs(t, e, ws))
	write("9002", "1", "Здравствуйте")
	if after := len(chatJobs(t, e, ws)); after != before {
		t.Fatalf("queue turned off, still queued: %d → %d", before, after)
	}
}
