package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// The AI bot from its settings to a task: an admin turns it on for a
// channel, it collects the details of a bug in a conversation and creates
// the task the IT team sees, then hands the conversation to people.
func TestAIBotAPI(t *testing.T) {
	in := setupInbox(t)
	e, ws := in.e, in.so.Workspace.Id
	wantProblem(t, in.op.do(http.MethodGet, "/api/v1/ai/bot", nil), http.StatusForbidden, "forbidden")
	ch := uuid.Must(uuid.NewV7())
	e.ownerExec("INSERT INTO channels (id, workspace_id, type, name, status) VALUES ($1, $2, 'telegram_bot', 'Бот', 'active')", ch, ws)
	if got := decode[oapi.AIBot](t, in.owner.do(http.MethodGet, "/api/v1/ai/bot", nil)); got.MinConfidence != ai.DefaultMinConfidence || len(got.ChannelIds) != 0 {
		t.Fatalf("defaults %+v", got)
	}
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/ai/bot", map[string]any{"channel_ids": []string{ch.String()}}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/ai/bot", map[string]any{
		"channel_ids": []string{uuid.NewString()}, "topics_off": []string{}, "min_confidence": 75, "handoff_message": "",
	}), http.StatusBadRequest, "invalid_request")
	got := decode[oapi.AIBot](t, in.owner.do(http.MethodPut, "/api/v1/ai/bot", map[string]any{
		"channel_ids": []string{ch.String()}, "topics_off": []string{}, "min_confidence": 80, "handoff_message": "Зову коллегу.",
	}))
	if len(got.ChannelIds) != 1 || got.MinConfidence != 80 || got.HandoffMessage != "Зову коллегу." {
		t.Fatalf("saved %+v", got)
	}

	// With AI off the bot takes nothing.
	write := func(thread, id, text string) uuid.UUID {
		t.Helper()
		res, err := e.convs.Receive(context.Background(), ws, conversations.Inbound{
			ChannelID: ch, Thread: thread, ExternalID: id, Body: text,
			Sender: conversations.InboundSender{ExternalUserID: thread, Name: "Иван"},
		})
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		// The customer has been quiet long enough for the bot.
		e.ownerExec("UPDATE messages SET created_at = created_at - interval '10 seconds' WHERE conversation_id = $1", res.ConversationID)
		return res.ConversationID
	}
	status := func(conv uuid.UUID) oapi.ConversationStatus {
		t.Helper()
		return decode[oapi.Conversation](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+conv.String(), nil)).Status
	}
	if conv := write("5001", "1", "Привет"); status(conv) != oapi.ConversationStatusQueued {
		t.Fatalf("AI off: %s", status(conv))
	}
	if rec := in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"api_key": aiTestKey})); rec.Code != http.StatusOK {
		t.Fatalf("ai settings: %d %s", rec.Code, rec.Body)
	}
	if rec := in.owner.do(http.MethodPut, "/api/v1/kb/guide", map[string]any{
		"about": "Зерно — CRM для агентств.", "handoff_rules": "", "task_checklist": "Email аккаунта\nВерсия приложения",
	}); rec.Code != http.StatusOK {
		t.Fatalf("guide: %d %s", rec.Code, rec.Body)
	}

	conv := write("5002", "1", "Приложение вылетает при входе, версия 2.3.1, почта ivan@example.com")
	if st := status(conv); st != oapi.ConversationStatusBot {
		t.Fatalf("bot on: %s", st)
	}
	e.llm.answer(fakeLLMAnswer{status: 200, body: `{"model":"claude-haiku-4-5-20251001","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"bot_reply","input":{
		"answer":"Спасибо, передаю разработчикам.","answer_type":"collect","citations":[],"confidence":0.9,"handoff_reason":"",
		"summary":"Приложение вылетает при входе.","collected":[{"item":"Email аккаунта","value":"⟨EMAIL_1⟩"},{"item":"Версия приложения","value":"2.3.1"}],
		"missing":[],"task_title":"Вылет приложения при входе"}}],"usage":{"input_tokens":3000,"output_tokens":200}}`})
	if wait, err := e.ai.BotTurn(context.Background(), e.convs, e.tasks, 1, ai.ReplyArgs{WorkspaceID: ws, ConversationID: conv}); err != nil || wait != 0 {
		t.Fatalf("turn: %v %v", wait, err)
	}
	if st := status(conv); st != oapi.ConversationStatusQueued {
		t.Fatalf("after the task: %s", st)
	}
	tasks := decode[oapi.TaskPage](t, in.owner.do(http.MethodGet, "/api/v1/tasks", nil))
	if len(tasks.Items) != 1 || tasks.Items[0].Title != "Вылет приложения при входе" || tasks.Items[0].CreatedBy != nil {
		t.Fatalf("tasks %+v", tasks.Items)
	}
	task := decode[oapi.Task](t, in.owner.do(http.MethodGet, "/api/v1/tasks/"+tasks.Items[0].Id.String(), nil))
	if task.Body != "Приложение вылетает при входе.\n\nEmail аккаунта: ivan@example.com\nВерсия приложения: 2.3.1" {
		t.Fatalf("task body %q", task.Body)
	}
	msgs := decode[oapi.MessagePage](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+conv.String()+"/messages", nil))
	var handoff *oapi.MessageEvent
	var reply string
	for _, m := range msgs.Items {
		if m.Event != nil && m.Event.Type == oapi.MessageEventTypeBotHandoff {
			handoff = m.Event
		}
		if m.SenderType == oapi.MessageSenderTypeBot && m.Kind == oapi.MessageKindText {
			reply = m.Body
		}
	}
	if handoff == nil || handoff.Reason == nil || *handoff.Reason != oapi.MessageEventReasonTaskCreated {
		t.Fatalf("handoff %+v", handoff)
	}
	if want := "Спасибо, передаю разработчикам.\n\nЗадача №" + strconv.FormatInt(task.Number, 10) + " передана технической команде. Ответ придёт сюда, в этот чат."; reply != want {
		t.Fatalf("reply %q", reply)
	}

	// A reply of a member in a conversation of the bot takes it from the bot.
	other := write("5003", "1", "Как выгрузить отчёт?")
	rec := in.owner.do(http.MethodPost, "/api/v1/conversations/"+other.String()+"/messages", map[string]any{"body": "Сейчас помогу."})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	c := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+other.String(), nil))
	if c.Status != oapi.ConversationStatusAssigned || c.AssigneeId == nil || *c.AssigneeId != in.so.User.Id {
		t.Fatalf("taken by the reply: %+v", c)
	}
	// An operator takes one from the bot.
	third := write("5004", "1", "Ещё вопрос")
	if rec := in.op.do(http.MethodPut, "/api/v1/conversations/"+third.String()+"/assignee", map[string]any{"assignee_id": in.sop.User.Id}); rec.Code != http.StatusOK {
		t.Fatalf("take from the bot: %d %s", rec.Code, rec.Body)
	}
}
