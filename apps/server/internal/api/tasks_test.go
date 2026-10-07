package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// taskUpdate is the whole state of t for PUT /tasks/{id}, changed by edit.
func taskUpdate(t oapi.Task, edit func(map[string]any)) map[string]any {
	body := map[string]any{
		"version": t.Version, "title": t.Title, "body": t.Body, "status": t.Status, "priority": t.Priority,
		"queue_id": t.QueueId, "topic_id": t.TopicId, "assignee_id": t.AssigneeId,
	}
	if edit != nil {
		edit(body)
	}
	return body
}

// A task by hand: every member creates, changes and comments it; the
// version guards against lost edits; the feed records each change; queues
// and deleting are for admins.
func TestTasks_Lifecycle(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	opID := in.sop.User.Id

	// Queues are the admins'.
	wantProblem(t, op.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "Разработка", "member_ids": []string{}}),
		http.StatusForbidden, "forbidden")
	dev := decode[oapi.TaskQueue](t, owner.do(http.MethodPost, "/api/v1/task-queues",
		map[string]any{"name": "Разработка", "member_ids": []uuid.UUID{opID}}))
	if dev.Name != "Разработка" || len(dev.MemberIds) != 1 || dev.MemberIds[0] != opID {
		t.Fatalf("queue: %+v", dev)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "разработка", "member_ids": []string{}}),
		http.StatusConflict, "queue_exists")

	// An operator creates a task in the queue for nobody.
	rec := op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "  Обновить сертификат на сервере  ", "queue_id": dev.Id})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	task := decode[oapi.Task](t, rec)
	if task.Number != 1 || task.Title != "Обновить сертификат на сервере" || task.Status != oapi.TaskStatusOpen ||
		task.Priority != oapi.Normal || task.AssigneeId != nil || task.CreatedBy == nil || *task.CreatedBy != opID {
		t.Fatalf("created: %+v", task)
	}
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "a\nb"}), http.StatusBadRequest, "invalid_request")
	// An assignee must be a member of this workspace.
	_, stranger := e.register("stranger@example.test", "stranger")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Чужому", "assignee_id": stranger.User.Id}),
		http.StatusBadRequest, "invalid_request")

	// In progress without an assignee: the task goes to whoever took it.
	base := "/api/v1/tasks/" + task.Id.String()
	rec = owner.do(http.MethodPut, base, taskUpdate(task, func(b map[string]any) { b["status"] = "in_progress"; b["priority"] = "high" }))
	if rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	taken := decode[oapi.Task](t, rec)
	if taken.Status != oapi.TaskStatusInProgress || taken.AssigneeId == nil || *taken.AssigneeId != in.so.User.Id ||
		taken.Version != task.Version+1 {
		t.Fatalf("taken: %+v", taken)
	}
	// A stale version loses nobody's edit.
	wantProblem(t, op.do(http.MethodPut, base, taskUpdate(task, func(b map[string]any) { b["title"] = "Старое" })),
		http.StatusConflict, "task_conflict")

	// Comment and done; the feed has every change in order.
	if rec := op.do(http.MethodPost, base+"/activity", map[string]any{"body": "Сертификат истекает в пятницу"}); rec.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", rec.Code, rec.Body)
	}
	done := decode[oapi.Task](t, op.do(http.MethodPut, base, taskUpdate(taken, func(b map[string]any) { b["status"] = "done" })))
	if done.Status != oapi.TaskStatusDone || done.ClosedAt == nil {
		t.Fatalf("done: %+v", done)
	}
	feed := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, base+"/activity", nil)).Items
	var kinds []string
	for _, a := range feed {
		kinds = append(kinds, string(a.Kind))
	}
	if got := strings.Join(kinds, ","); got != "created,priority,assignee,status,comment,status" {
		t.Fatalf("feed: %s", got)
	}
	if st := feed[3]; st.From == nil || *st.From != "open" || st.To == nil || *st.To != "in_progress" {
		t.Fatalf("status line: %+v", st)
	}
	if c := feed[4]; c.Body == nil || *c.Body != "Сертификат истекает в пятницу" || c.ActorId == nil || *c.ActorId != opID {
		t.Fatalf("comment line: %+v", c)
	}
	// A long feed shows its latest 500 lines: 500 older comments go before
	// the six lines above, and the last of those is still there.
	e.ownerExec(`INSERT INTO task_activity (id, workspace_id, task_id, kind, body, created_at)
		SELECT ('00000000-0000-7000-8000-' || lpad(n::text, 12, '0'))::uuid, workspace_id, id, 'comment', 'старый', created_at
		FROM tasks, generate_series(1, 500) n WHERE id = $1`, task.Id)
	long := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, base+"/activity", nil)).Items
	if len(long) != 500 || long[len(long)-1].Kind != oapi.TaskActivityKindStatus || long[0].Body == nil || *long[0].Body != "старый" {
		t.Fatalf("long feed: %d lines, last %+v", len(long), long[len(long)-1])
	}

	// Filters: open, mine, the queue, the search.
	other := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Принтер на втором этаже", "assignee_id": opID}))
	list := func(query string) []int64 {
		page := decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks"+query, nil))
		out := []int64{}
		for _, it := range page.Items {
			out = append(out, it.Number)
		}
		return out
	}
	for query, want := range map[string]string{
		"":                               "[2 1]",
		"?state=open":                    "[2]",
		"?state=closed":                  "[1]",
		"?assignee=me":                   "[2]",
		"?assignee=none":                 "[]",
		"?queue=" + dev.Id.String():      "[1]",
		"?queue=none":                    "[2]",
		"?q=принтер":                     "[2]",
		"?q=1":                           "[1]",
		"?q=%25":                         "[]",
		"?limit=1":                       "[2]",
		"?limit=1&cursor=2":              "[1]",
		"?status=done":                   "[1]",
		"?assignee=" + opID.String():     "[2]",
		"?state=open&assignee=none&q=xx": "[]",
	} {
		if got := fmt.Sprint(list(query)); got != want {
			t.Errorf("list %q: %s, want %s", query, got, want)
		}
	}
	if s := decode[oapi.TaskSummary](t, op.do(http.MethodGet, "/api/v1/tasks/summary", nil)); s.AssignedOpen != 1 {
		t.Fatalf("summary: %+v", s)
	}

	// Deleting is the admins'; a deleted queue leaves its tasks without one.
	wantProblem(t, op.do(http.MethodDelete, base, nil), http.StatusForbidden, "forbidden")
	if rec := owner.do(http.MethodDelete, "/api/v1/task-queues/"+dev.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete queue: %d", rec.Code)
	}
	if got := decode[oapi.Task](t, op.do(http.MethodGet, base, nil)); got.QueueId != nil {
		t.Fatalf("queue kept: %+v", got)
	}
	if rec := owner.do(http.MethodDelete, base, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	wantProblem(t, op.do(http.MethodGet, base, nil), http.StatusNotFound, "not_found")

	// Another workspace sees none of it.
	foreign, _ := e.login("stranger@example.test")
	wantProblem(t, foreign.do(http.MethodGet, "/api/v1/tasks/"+other.Id.String(), nil), http.StatusNotFound, "not_found")
	if page := decode[oapi.TaskPage](t, foreign.do(http.MethodGet, "/api/v1/tasks", nil)); len(page.Items) != 0 {
		t.Fatalf("foreign list: %+v", page)
	}

	// Ids of another workspace's queue, member or conversation work nowhere
	// here, and that workspace changes nothing of ours.
	fq := decode[oapi.TaskQueue](t, foreign.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "Чужая", "member_ids": []string{}}))
	fct := decode[oapi.Contact](t, foreign.do(http.MethodPost, "/api/v1/contacts", map[string]any{"name": "Чужой клиент"}))
	fconv := decode[oapi.Conversation](t, foreign.do(http.MethodPost, "/api/v1/conversations", map[string]any{"contact_id": fct.Id}))
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "В чужую очередь", "queue_id": fq.Id}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Из чужого диалога", "conversation_id": fconv.Id}),
		http.StatusNotFound, "not_found")
	obase := "/api/v1/tasks/" + other.Id.String()
	wantProblem(t, op.do(http.MethodPut, obase, taskUpdate(other, func(b map[string]any) { b["queue_id"] = fq.Id })),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPut, obase, taskUpdate(other, func(b map[string]any) { b["assignee_id"] = stranger.User.Id })),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPost, obase+"/conversations", map[string]any{"conversation_id": fconv.Id}),
		http.StatusNotFound, "not_found")
	wantProblem(t, foreign.do(http.MethodPut, obase, taskUpdate(other, func(b map[string]any) { b["title"], b["assignee_id"] = "Взлом", nil })),
		http.StatusNotFound, "not_found")
	wantProblem(t, foreign.do(http.MethodPost, obase+"/activity", map[string]any{"body": "Взлом"}), http.StatusNotFound, "not_found")
	wantProblem(t, foreign.do(http.MethodPost, obase+"/conversations", map[string]any{"conversation_id": fconv.Id}),
		http.StatusNotFound, "not_found")
	wantProblem(t, foreign.do(http.MethodDelete, obase, nil), http.StatusNotFound, "not_found")
	if got := decode[oapi.Task](t, op.do(http.MethodGet, obase, nil)); got.Title != other.Title || got.Version != other.Version {
		t.Fatalf("changed from another workspace: %+v", got)
	}

	// A NUL in a text is a bad request, not a server error.
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "a\x00b"}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPost, obase+"/activity", map[string]any{"body": "a\x00b"}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodGet, "/api/v1/tasks?q=a%00b", nil), http.StatusBadRequest, "invalid_request")
}

// A task from a conversation keeps it waiting for the fix like a GitHub
// issue: done brings it back to its operator with a reply to send,
// canceled does not; an open issue still keeps it waiting after the task.
func TestTasks_FromConversation(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	c := a.owner
	conv := e.reportConversation(a, "Не синхронизируется корзина\nПосле обновления до 2.4.1 корзина пустая, ответьте на anna@example.com")
	cbase := "/api/v1/conversations/" + conv.String()
	if rec := c.do(http.MethodPut, cbase+"/assignee", map[string]any{"assignee_id": e.ownerID(a)}); rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body)
	}
	conversation := func() oapi.Conversation { return decode[oapi.Conversation](t, c.do(http.MethodGet, cbase, nil)) }
	lines := func(typ string) int {
		return e.ownerCount("SELECT count(*) FROM messages WHERE conversation_id = $1 AND kind = 'event' AND meta->>'type' = $2", conv, typ)
	}

	// Without AI the draft is the subject and the customer's words, their
	// personal data masked: a task outlives the contact's data.
	d := decode[oapi.TaskDraft](t, c.do(http.MethodGet, cbase+"/tasks/draft", nil))
	if d.DraftedBy != oapi.TaskDraftDraftedByTemplate || d.Title != "Не синхронизируется корзина" ||
		!strings.Contains(d.Body, "Сообщение клиента:\nНе синхронизируется корзина") || !strings.Contains(d.Body, "Приложение: Зерно 2.4.1") ||
		!strings.Contains(d.Body, "ответьте на ⟨EMAIL_1⟩") || strings.Contains(d.Body, "anna@example.com") {
		t.Fatalf("draft: %+v", d)
	}

	// With AI on the model writes the title and the parts of the description;
	// the customer's words stay below them.
	if rec := c.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"api_key": aiTestKey})); rec.Code != http.StatusOK {
		t.Fatalf("ai settings: %d %s", rec.Code, rec.Body)
	}
	e.llm.answer(fakeLLMAnswer{status: 200, body: `{"model":"claude-haiku-4-5-20251001","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"issue_draft","input":{
		"title":"Корзина пустеет после обновления","summary":"После обновления корзина пустая.",
		"steps":["Обновить приложение","Открыть корзину"],"expected":"Товары на месте","actual":"Корзина пустая"}}],"usage":{"input_tokens":900,"output_tokens":120}}`})
	ad := decode[oapi.TaskDraft](t, c.do(http.MethodGet, cbase+"/tasks/draft", nil))
	if ad.DraftedBy != oapi.TaskDraftDraftedByAi || ad.Title != "Корзина пустеет после обновления" ||
		!strings.HasPrefix(ad.Body, "После обновления корзина пустая.\n\nШаги воспроизведения:\n1. Обновить приложение\n2. Открыть корзину\n\nОжидалось:\nТовары на месте\n\nФактически:\nКорзина пустая") ||
		!strings.Contains(ad.Body, "Сообщение клиента:\nНе синхронизируется корзина") || strings.Contains(ad.Body, "anna@example.com") {
		t.Fatalf("ai draft: %+v", ad)
	}
	if n := e.ownerCount(`SELECT count(*) FROM ai_calls WHERE conversation_id = $1 AND purpose = 'issue_draft' AND status = 'ok'`, conv); n != 1 {
		t.Fatalf("logged calls: %d", n)
	}

	// Created from the conversation: a feed line, waiting, an assigned
	// conversation goes to pending.
	task := decode[oapi.Task](t, c.do(http.MethodPost, "/api/v1/tasks", map[string]any{
		"title": d.Title, "body": d.Body, "conversation_id": conv,
	}))
	if len(task.ConversationIds) != 1 || task.ConversationIds[0] != conv {
		t.Fatalf("task: %+v", task)
	}
	if cv := conversation(); cv.Status != oapi.ConversationStatusPending || !cv.AwaitingFix || lines("task_created") != 1 {
		t.Fatalf("after create: %+v, lines %d", cv, lines("task_created"))
	}
	wantProblem(t, c.do(http.MethodPost, "/api/v1/tasks/"+task.Id.String()+"/conversations", map[string]any{"conversation_id": conv}),
		http.StatusConflict, "task_linked")
	ct := decode[oapi.ConversationTasks](t, c.do(http.MethodGet, cbase+"/tasks", nil))
	if len(ct.Items) != 1 || ct.ReplyDraft != nil || !ct.Settings.Enabled || ct.Settings.ConversationButton != oapi.Both {
		t.Fatalf("conversation tasks: %+v", ct)
	}

	// An open GitHub issue as well: the task done does not end waiting.
	e.connectGitHub(c, true)
	link := decode[oapi.IssueLink](t, c.do(http.MethodPost, cbase+"/issues", map[string]any{"title": "Корзина", "body": ""}))
	if err := e.createIssueJob(e.workspaceOf(a), link.Id, false); err != nil {
		t.Fatalf("job: %v", err)
	}
	base := "/api/v1/tasks/" + task.Id.String()
	done := decode[oapi.Task](t, c.do(http.MethodPut, base, taskUpdate(task, func(b map[string]any) { b["status"] = "done" })))
	if cv := conversation(); cv.Status != oapi.ConversationStatusPending || !cv.AwaitingFix || lines("task_closed") != 1 {
		t.Fatalf("done with an open issue: %+v", cv)
	}

	// Reopened, then the issue unlinked by closing it as not planned is
	// GitHub's business; here: drop the issue's waiting by hand and close
	// the task again — now the conversation comes back with the reply.
	reopened := decode[oapi.Task](t, c.do(http.MethodPut, base, taskUpdate(done, func(b map[string]any) { b["status"] = "open" })))
	if lines("task_reopened") != 1 {
		t.Fatal("no reopened line")
	}
	e.ownerExec("UPDATE issue_links SET status = 'closed', close_reason = 'not_planned', closed_at = now() WHERE id = $1", link.Id)
	decode[oapi.Task](t, c.do(http.MethodPut, base, taskUpdate(reopened, func(b map[string]any) { b["status"] = "done" })))
	cv := conversation()
	if cv.Status != oapi.ConversationStatusAssigned || cv.AwaitingFix || lines("task_closed") != 2 {
		t.Fatalf("done: %+v", cv)
	}
	ct = decode[oapi.ConversationTasks](t, c.do(http.MethodGet, cbase+"/tasks", nil))
	if ct.ReplyDraft == nil || ct.ReplyDraft.Number != task.Number || !strings.Contains(ct.ReplyDraft.Text, "Задачу по вашему обращению выполнили") {
		t.Fatalf("reply draft: %+v", ct.ReplyDraft)
	}

	// The customer's words reach a draft with personal data masked: every
	// member sees tasks, and a task outlives the contact's data (ADR-0015).
	if rec := c.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"enabled": false})); rec.Code != http.StatusOK {
		t.Fatalf("ai off: %d %s", rec.Code, rec.Body)
	}
	pd := e.reportConversation(a, "Пишите на anna.k@example.test или +7 912 345-67-89")
	md := decode[oapi.TaskDraft](t, c.do(http.MethodGet, "/api/v1/conversations/"+pd.String()+"/tasks/draft", nil))
	if strings.Contains(md.Title+md.Body, "anna.k@example.test") || strings.Contains(md.Title+md.Body, "345-67-89") ||
		!strings.Contains(md.Body, "⟨EMAIL_1⟩") {
		t.Fatalf("unmasked draft: %+v", md)
	}

	// A second conversation linked and then unlinked stops waiting.
	conv2 := e.reportConversation(a, "Корзина снова пустая")
	other := decode[oapi.Task](t, c.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Вход", "conversation_id": conv2}))
	if rec := c.do(http.MethodDelete, "/api/v1/tasks/"+other.Id.String()+"/conversations/"+conv2.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body)
	}
	if cv := decode[oapi.Conversation](t, c.do(http.MethodGet, "/api/v1/conversations/"+conv2.String(), nil)); cv.AwaitingFix {
		t.Fatalf("still waiting after unlink: %+v", cv)
	}

	// The section turned off: no new tasks, the setting is the admins'.
	if rec := c.do(http.MethodPut, "/api/v1/task-settings", map[string]any{
		"enabled": false, "conversation_button": "github", "reply_ru": "{name}, готово: задача {task_number}.", "reply_en": "",
	}); rec.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, c.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Ещё"}), http.StatusConflict, "tasks_disabled")
	wantProblem(t, c.do(http.MethodPut, "/api/v1/task-settings", map[string]any{
		"enabled": true, "conversation_button": "both", "reply_ru": "{customer}", "reply_en": "",
	}), http.StatusBadRequest, "invalid_request")
	st := decode[oapi.TaskSettingsView](t, c.do(http.MethodGet, "/api/v1/task-settings", nil))
	if st.Settings.Enabled || st.Settings.ConversationButton != oapi.Github || st.Settings.ReplyRu != "{name}, готово: задача {task_number}." ||
		st.Settings.ReplyEn != st.Defaults.ReplyEn {
		t.Fatalf("settings: %+v", st)
	}
}

// Telegram (TK-02): the admin adds a bot of its own, a member links their
// Telegram with /start <code>, messages about tasks go there through a job
// with the number, the title and the link only.
func TestTasks_TelegramNotifications(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	ws, opID := in.so.Workspace.Id, in.sop.User.Id

	// No bot yet: nothing to link to.
	if me := decode[oapi.MyTelegram](t, op.do(http.MethodGet, "/api/v1/me/telegram", nil)); me.BotUsername != nil || me.Linked {
		t.Fatalf("before a bot: %+v", me)
	}
	wantProblem(t, op.do(http.MethodPost, "/api/v1/me/telegram/link", nil), http.StatusConflict, "no_notify_bot")

	// The bot is the admins'; a token Telegram does not know is refused.
	wantProblem(t, op.do(http.MethodPut, "/api/v1/task-settings/telegram-bot", map[string]any{"token": channelstest.OtherBotToken}),
		http.StatusForbidden, "forbidden")
	wantProblem(t, owner.do(http.MethodPut, "/api/v1/task-settings/telegram-bot", map[string]any{"token": "111111111:AAH-unknown-token-000000000000000000000"}),
		http.StatusUnprocessableEntity, "telegram_token_invalid")
	// A support channel's bot is refused: reading its updates would take the
	// customers' messages.
	decode[oapi.Channel](t, owner.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": channelstest.BotToken}))
	wantProblem(t, owner.do(http.MethodPut, "/api/v1/task-settings/telegram-bot", map[string]any{"token": channelstest.BotToken}),
		http.StatusConflict, "notify_bot_busy")
	st := decode[oapi.TelegramBotState](t, owner.do(http.MethodPut, "/api/v1/task-settings/telegram-bot", map[string]any{"token": channelstest.OtherBotToken}))
	if st.Bot == nil || st.Bot.Username != "other_bot" || !st.EncryptionAvailable {
		t.Fatalf("bot: %+v", st)
	}
	if n := e.ownerCount("SELECT count(*) FROM notify_bots WHERE workspace_id = $1 AND position($2 in encode(sealed, 'escape')) = 0", ws, channelstest.OtherBotToken); n != 1 {
		t.Fatal("the token is not sealed")
	}

	// The member opens the link and presses Start; the check links the chat
	// and greets it. Another code in the bot's updates links nobody.
	link := decode[oapi.TelegramLinkStart](t, op.do(http.MethodPost, "/api/v1/me/telegram/link", nil))
	code, ok := strings.CutPrefix(link.Url, "https://t.me/other_bot?start=")
	if !ok || len(code) < 20 {
		t.Fatalf("link: %s", link.Url)
	}
	if me := decode[oapi.MyTelegram](t, op.do(http.MethodGet, "/api/v1/me/telegram", nil)); !me.Pending || me.Linked {
		t.Fatalf("pending: %+v", me)
	}
	e.bots.Push(
		`{"message":{"message_id":1,"from":{"id":777,"first_name":"Мошенник"},"chat":{"id":777,"type":"private"},"text":"/start not-a-code"}}`,
		`{"message":{"message_id":2,"from":{"id":555,"first_name":"Пётр","username":"petr"},"chat":{"id":555,"type":"private"},"text":"/start `+code+`"}}`,
	)
	me := decode[oapi.MyTelegram](t, op.do(http.MethodPost, "/api/v1/me/telegram/check", nil))
	if !me.Linked || me.Pending || me.Name == nil || *me.Name != "Пётр @petr" {
		t.Fatalf("linked: %+v", me)
	}
	sent := e.bots.SnapshotSent()
	if len(sent) != 1 || sent[0].ChatID != 555 {
		t.Fatalf("greeting: %+v", sent)
	}
	// The same code a second time does nothing.
	if n := e.ownerCount("SELECT count(*) FROM telegram_links WHERE workspace_id = $1 AND chat_id IS NOT NULL", ws); n != 1 {
		t.Fatalf("links: %d", n)
	}

	// The owner gives a task to the member: a job with ids only, then the
	// message with the number, the title and the link.
	task := decode[oapi.Task](t, owner.do(http.MethodPost, "/api/v1/tasks", map[string]any{
		"title": "Обновить сертификат", "body": "секретная подробность", "assignee_id": opID,
	}))
	var args []byte
	e.ownerScan(&args, "SELECT args FROM river.river_job WHERE kind = 'notifications.telegram_task' AND args->>'workspace_id' = $1", ws.String())
	var job notifications.TaskMessageArgs
	if err := json.Unmarshal(args, &job); err != nil || job.TaskID != task.Id || job.UserID != opID || job.About != tasks.NoticeAssigned {
		t.Fatalf("job: %s %v", args, err)
	}
	worker := notifications.TelegramWorkerOptions{Service: e.notifications, Tasks: e.tasks, PanelURL: panelURL}
	if err := notifications.SendTaskMessage(context.Background(), worker, job); err != nil {
		t.Fatalf("send: %v", err)
	}
	sent = e.bots.SnapshotSent()
	msg := sent[len(sent)-1]
	if msg.ChatID != 555 || !strings.Contains(msg.Text, "З-1") || !strings.Contains(msg.Text, "Обновить сертификат") ||
		!strings.Contains(msg.Text, panelURL+"/tasks/"+task.Id.String()) || strings.Contains(msg.Text, "секретная") || !msg.NoPreview {
		t.Fatalf("message: %+v", msg)
	}
	// Too many requests: the job waits as long as Telegram asks.
	e.bots.FailNext(&telegram.APIError{Method: "sendMessage", Code: http.StatusTooManyRequests, RetryAfter: 7 * time.Second})
	var snooze *rivertype.JobSnoozeError
	if err := notifications.SendTaskMessage(context.Background(), worker, job); !errors.As(err, &snooze) || snooze.Duration != 7*time.Second {
		t.Fatalf("send after 429: %v", err)
	}

	// The Telegram linked to the member is not linked to the owner too: the
	// chat is told so and the owner's code keeps waiting.
	time.Sleep(notifications.CheckInterval)
	ownerLink := decode[oapi.TelegramLinkStart](t, owner.do(http.MethodPost, "/api/v1/me/telegram/link", nil))
	ownerCode, _ := strings.CutPrefix(ownerLink.Url, "https://t.me/other_bot?start=")
	e.bots.Push(`{"message":{"message_id":3,"from":{"id":555,"first_name":"Пётр"},"chat":{"id":555,"type":"private"},"text":"/start ` + ownerCode + `"}}`)
	if me := decode[oapi.MyTelegram](t, owner.do(http.MethodPost, "/api/v1/me/telegram/check", nil)); me.Linked || !me.Pending {
		t.Fatalf("a chat of another member linked: %+v", me)
	}
	if sent = e.bots.SnapshotSent(); !strings.Contains(sent[len(sent)-1].Text, "другому сотруднику") || sent[len(sent)-1].ChatID != 555 {
		t.Fatalf("refusal: %+v", sent[len(sent)-1])
	}
	// A member without a code waiting does not make the server read the bot.
	reads := e.bots.Reads()
	time.Sleep(notifications.CheckInterval)
	decode[oapi.MyTelegram](t, op.do(http.MethodPost, "/api/v1/me/telegram/check", nil))
	if e.bots.Reads() != reads {
		t.Fatal("a check without a code read the bot")
	}
	// Assigning yourself tells nobody.
	before := e.ownerCount("SELECT count(*) FROM river.river_job WHERE kind = 'notifications.telegram_task'")
	decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Себе", "assignee_id": opID}))
	if n := e.ownerCount("SELECT count(*) FROM river.river_job WHERE kind = 'notifications.telegram_task'"); n != before {
		t.Fatalf("own task queued a message: %d → %d", before, n)
	}

	// The member blocked the bot: the next message unlinks them.
	e.bots.FailNext(&telegram.UndeliverableError{Reason: telegram.ReasonBlocked, Detail: "Forbidden: bot was blocked by the user"})
	if err := notifications.SendTaskMessage(context.Background(), worker, job); err != nil {
		t.Fatalf("send to a blocked chat: %v", err)
	}
	if me := decode[oapi.MyTelegram](t, op.do(http.MethodGet, "/api/v1/me/telegram", nil)); me.Linked {
		t.Fatalf("still linked after a block: %+v", me)
	}

	// A revoked token is the admin's to replace; the member is told so.
	time.Sleep(notifications.CheckInterval)
	decode[oapi.TelegramLinkStart](t, op.do(http.MethodPost, "/api/v1/me/telegram/link", nil))
	e.bots.Revoke()
	wantProblem(t, op.do(http.MethodPost, "/api/v1/me/telegram/check", nil), http.StatusConflict, "notify_bot_revoked")

	// Removing the bot forgets every link.
	if rec := owner.do(http.MethodDelete, "/api/v1/task-settings/telegram-bot", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete bot: %d", rec.Code)
	}
	if n := e.ownerCount("SELECT count(*) FROM telegram_links WHERE workspace_id = $1", ws); n != 0 {
		t.Fatalf("links kept: %d", n)
	}
}
