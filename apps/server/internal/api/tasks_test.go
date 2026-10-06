package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// taskUpdate is the whole state of t for PUT /tasks/{id}, changed by edit.
func taskUpdate(t oapi.Task, edit func(map[string]any)) map[string]any {
	body := map[string]any{
		"version": t.Version, "title": t.Title, "body": t.Body, "status": t.Status, "priority": t.Priority,
		"queue_id": t.QueueId, "assignee_id": t.AssigneeId,
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
}

// A task from a conversation keeps it waiting for the fix like a GitHub
// issue: done brings it back to its operator with a reply to send,
// canceled does not; an open issue still keeps it waiting after the task.
func TestTasks_FromConversation(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectApp("owner@example.test", "zerno")
	c := a.owner
	conv := e.reportConversation(a, "Не синхронизируется корзина\nПосле обновления до 2.4.1 корзина пустая")
	cbase := "/api/v1/conversations/" + conv.String()
	if rec := c.do(http.MethodPut, cbase+"/assignee", map[string]any{"assignee_id": e.ownerID(a)}); rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body)
	}
	conversation := func() oapi.Conversation { return decode[oapi.Conversation](t, c.do(http.MethodGet, cbase, nil)) }
	lines := func(typ string) int {
		return e.ownerCount("SELECT count(*) FROM messages WHERE conversation_id = $1 AND kind = 'event' AND meta->>'type' = $2", conv, typ)
	}

	// Without AI the draft is the subject and the customer's words.
	d := decode[oapi.TaskDraft](t, c.do(http.MethodGet, cbase+"/tasks/draft", nil))
	if d.DraftedBy != oapi.TaskDraftDraftedByTemplate || d.Title != "Не синхронизируется корзина" ||
		!strings.Contains(d.Body, "Сообщение клиента:\nНе синхронизируется корзина") || !strings.Contains(d.Body, "Приложение: Зерно 2.4.1") {
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
		!strings.Contains(ad.Body, "Сообщение клиента:\nНе синхронизируется корзина") {
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
