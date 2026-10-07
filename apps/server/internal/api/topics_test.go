package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Topics: admins manage them, operators set them on any conversation and
// filter the inbox, tasks take them from their conversation with the
// topic's queue, and an agent's token reads them.
func TestTopicsAPI(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	opID := in.sop.User.Id

	queue := decode[oapi.TaskQueue](t, owner.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "CRM", "member_ids": []string{}}))

	// routing.manage: the owner creates, the operator does not.
	wantProblem(t, op.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "CRM"}), http.StatusForbidden, "forbidden")
	rec := owner.do(http.MethodPost, "/api/v1/topics", map[string]any{
		"name": "CRM", "color": "blue", "priority": 2, "task_queue_id": queue.Id, "member_ids": []uuid.UUID{opID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	crm := decode[oapi.Topic](t, rec)
	if crm.Name != "CRM" || crm.Priority != 2 || crm.TaskQueueId == nil || *crm.TaskQueueId != queue.Id || len(crm.MemberIds) != 1 {
		t.Fatalf("created: %+v", crm)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "crm"}), http.StatusConflict, "topic_exists")
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "X", "task_queue_id": uuid.New()}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/topics", map[string]any{}), http.StatusBadRequest, "invalid_request")
	free := decode[oapi.Topic](t, owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "Свободная форма"}))
	if free.Position != 1 || free.Priority != 0 || free.Color != "gray" {
		t.Fatalf("second: %+v", free)
	}
	list := decode[oapi.TopicList](t, owner.do(http.MethodPut, "/api/v1/topics/order", map[string]any{"ids": []uuid.UUID{free.Id, crm.Id}}))
	if len(list.Items) != 2 || list.Items[0].Id != free.Id {
		t.Fatalf("order: %+v", list.Items)
	}
	if got := decode[oapi.TopicList](t, op.do(http.MethodGet, "/api/v1/topics", nil)); len(got.Items) != 2 {
		t.Fatalf("operator's list: %+v", got.Items)
	}

	// conversations.tag: the operator sets a topic on a conversation that is
	// not theirs; it takes the priority, the inbox filters by it.
	c := in.conversation(t)
	base := "/api/v1/conversations/" + c.Id.String()
	if c.TopicId != nil {
		t.Fatalf("new conversation has a topic: %+v", c.TopicId)
	}
	wantProblem(t, op.do(http.MethodPut, base+"/topic", map[string]any{}), http.StatusBadRequest, "invalid_request")
	rec = op.do(http.MethodPut, base+"/topic", map[string]any{"topic_id": crm.Id, "version": c.Version})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.Conversation](t, rec); got.TopicId == nil || *got.TopicId != crm.Id || got.Priority != 2 {
		t.Fatalf("set: %+v", got)
	}
	other := in.conversation(t)
	ids := func(query string) []uuid.UUID {
		page := decode[oapi.ConversationPage](t, op.do(http.MethodGet, "/api/v1/conversations"+query, nil))
		out := []uuid.UUID{}
		for _, it := range page.Items {
			out = append(out, it.Id)
		}
		return out
	}
	if got := ids("?topic=" + crm.Id.String()); len(got) != 1 || got[0] != c.Id {
		t.Fatalf("by topic: %v", got)
	}
	if got := ids("?topic=none"); len(got) != 1 || got[0] != other.Id {
		t.Fatalf("without a topic: %v", got)
	}
	wantProblem(t, op.do(http.MethodGet, "/api/v1/conversations?topic=crm", nil), http.StatusBadRequest, "invalid_request")
	feed := decode[oapi.MessagePage](t, op.do(http.MethodGet, base+"/messages", nil))
	var line *oapi.MessageEvent
	for _, m := range feed.Items {
		if m.Event != nil && m.Event.Type == oapi.MessageEventTypeTopicChanged {
			line = m.Event
		}
	}
	if line == nil || line.TopicId == nil || *line.TopicId != crm.Id || line.ByCustomer == nil || *line.ByCustomer {
		t.Fatalf("feed line: %+v", line)
	}

	// A task from the conversation takes its topic and the topic's queue;
	// the list filters by topic; a change of topic is a line of the feed.
	rec = op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Не грузится список моделей", "conversation_id": c.Id})
	if rec.Code != http.StatusCreated {
		t.Fatalf("task: %d %s", rec.Code, rec.Body)
	}
	task := decode[oapi.Task](t, rec)
	if task.TopicId == nil || *task.TopicId != crm.Id || task.QueueId == nil || *task.QueueId != queue.Id {
		t.Fatalf("task from the conversation: %+v", task)
	}
	byHand := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Принтер"}))
	if byHand.TopicId != nil || byHand.QueueId != nil {
		t.Fatalf("task by hand: %+v", byHand)
	}
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "topic_id": uuid.New()}),
		http.StatusBadRequest, "invalid_request")
	page := decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks?topic="+crm.Id.String(), nil))
	if len(page.Items) != 1 || page.Items[0].Id != task.Id {
		t.Fatalf("tasks by topic: %+v", page.Items)
	}
	if page = decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks?topic=none", nil)); len(page.Items) != 1 || page.Items[0].Id != byHand.Id {
		t.Fatalf("tasks without a topic: %+v", page.Items)
	}
	// An update without topic_id is refused, not taken as "no topic".
	noTopic := taskUpdate(task, nil)
	delete(noTopic, "topic_id")
	wantProblem(t, op.do(http.MethodPut, "/api/v1/tasks/"+task.Id.String(), noTopic), http.StatusBadRequest, "invalid_request")
	moved := decode[oapi.Task](t, op.do(http.MethodPut, "/api/v1/tasks/"+task.Id.String(), taskUpdate(task, func(b map[string]any) {
		b["topic_id"] = free.Id
	})))
	if moved.TopicId == nil || *moved.TopicId != free.Id {
		t.Fatalf("moved: %+v", moved)
	}
	acts := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, "/api/v1/tasks/"+task.Id.String()+"/activity", nil)).Items
	if last := acts[len(acts)-1]; last.Kind != oapi.TaskActivityKindTopic || *last.From != crm.Id.String() || *last.To != free.Id.String() {
		t.Fatalf("topic line: %+v", last)
	}

	// An agent's token reads the topics, and nothing more of them.
	agent := e.client()
	tok := issueToken(t, op, "agent", "tasks:read")
	if got := decode[oapi.TopicList](t, agent.do(http.MethodGet, "/api/v1/topics", nil, bearer(tok.Secret))); len(got.Items) != 2 {
		t.Fatalf("token's list: %+v", got.Items)
	}
	wantProblem(t, agent.do(http.MethodPut, base+"/topic", map[string]any{"topic_id": nil}, bearer(tok.Secret)),
		http.StatusForbidden, "token_not_allowed")

	// Another workspace sees none of it; deleting leaves no topic behind.
	stranger, _ := e.register("stranger@example.test", "stranger")
	if l := decode[oapi.TopicList](t, stranger.do(http.MethodGet, "/api/v1/topics", nil)); len(l.Items) != 0 {
		t.Fatalf("stranger's list: %+v", l.Items)
	}
	wantProblem(t, stranger.do(http.MethodDelete, "/api/v1/topics/"+crm.Id.String(), nil), http.StatusNotFound, "not_found")
	wantProblem(t, op.do(http.MethodDelete, "/api/v1/topics/"+crm.Id.String(), nil), http.StatusForbidden, "forbidden")
	if rec := owner.do(http.MethodDelete, "/api/v1/topics/"+crm.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.Conversation](t, op.do(http.MethodGet, base, nil)); got.TopicId != nil {
		t.Fatalf("after delete: %+v", got.TopicId)
	}
}

// The widget offers the topics; a topic picked before writing goes with
// the first message, a later pick goes to the open conversation.
func TestTopicsAPI_Widget(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.7", shopOrigin)
	if sess := v.start(w.key, nil); sess.Topics == nil || len(sess.Topics) != 0 {
		t.Fatalf("no topics yet: %+v", sess.Topics)
	}
	crm := decode[oapi.Topic](t, w.owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "CRM", "priority": 1}))
	fansly := decode[oapi.Topic](t, w.owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "Fansly"}))
	sess := v.start(w.key, nil)
	if len(sess.Topics) != 2 || sess.Topics[0] != (oapi.WidgetTopic{Id: crm.Id, Name: "CRM"}) {
		t.Fatalf("session topics: %+v", sess.Topics)
	}

	rec := v.do(http.MethodPost, "/api/v1/widget/messages", map[string]any{"message": "Не открывается CRM", "topic_id": crm.Id})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	conv := v.messages("").Conversation
	if conv == nil || conv.TopicId == nil || *conv.TopicId != crm.Id {
		t.Fatalf("conversation: %+v", conv)
	}
	// A topic with a later message does not replace the one picked.
	if rec := v.do(http.MethodPost, "/api/v1/widget/messages", map[string]any{"message": "ещё", "topic_id": fansly.Id}); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if conv = v.messages("").Conversation; *conv.TopicId != crm.Id {
		t.Fatalf("replaced by a message: %+v", conv.TopicId)
	}
	// Picking again does.
	if rec := v.do(http.MethodPost, "/api/v1/widget/topic", map[string]any{"conversation_id": conv.Id, "topic_id": fansly.Id}); rec.Code != http.StatusNoContent {
		t.Fatalf("pick: %d %s", rec.Code, rec.Body)
	}
	if conv = v.messages("").Conversation; *conv.TopicId != fansly.Id {
		t.Fatalf("after the pick: %+v", conv.TopicId)
	}
	wantProblem(t, v.do(http.MethodPost, "/api/v1/widget/topic", map[string]any{"conversation_id": conv.Id, "topic_id": uuid.New()}),
		http.StatusNotFound, "not_found")
	// Someone else's conversation is not found.
	other := e.visitor("198.51.100.8", shopOrigin)
	other.start(w.key, nil)
	wantProblem(t, other.do(http.MethodPost, "/api/v1/widget/topic", map[string]any{"conversation_id": conv.Id, "topic_id": crm.Id}),
		http.StatusNotFound, "not_found")
}
