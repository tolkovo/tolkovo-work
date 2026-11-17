package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Moving a task to another project and changing a project's key (ADR-0022,
// P4): the task takes the next number there, its old ref keeps opening it,
// everything else stays; a changed key stays an alias no other project may
// take.
func TestTaskMove(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	ownerID, opID := in.so.User.Id, in.sop.User.Id
	ws := in.so.Workspace.Id

	task := func(c *client, body map[string]any) oapi.Task {
		t.Helper()
		return decode[oapi.Task](t, c.do(http.MethodPost, "/api/v1/tasks", body))
	}
	project := func(name, key string, members ...uuid.UUID) oapi.TaskProject {
		t.Helper()
		return decode[oapi.TaskProject](t, owner.do(http.MethodPost, "/api/v1/task-projects",
			map[string]any{"name": name, "key": key, "access": "members", "member_ids": members}))
	}
	queue := func(projectID uuid.UUID, members ...uuid.UUID) oapi.TaskQueue {
		t.Helper()
		return decode[oapi.TaskQueue](t, owner.do(http.MethodPost, "/api/v1/task-queues",
			map[string]any{"name": "Очередь", "project_id": projectID, "member_ids": members}))
	}
	move := func(c *client, x oapi.Task, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		full := map[string]any{"version": x.Version, "queue_id": nil, "assignee_id": x.AssigneeId}
		for k, v := range body {
			full[k] = v
		}
		return c.do(http.MethodPost, "/api/v1/tasks/"+x.Id.String()+"/move", full)
	}
	search := func(c *client, q string) []string {
		t.Helper()
		var refs []string
		for _, x := range decode[oapi.TaskPage](t, c.do(http.MethodGet, "/api/v1/tasks?q="+q, nil)).Items {
			refs = append(refs, x.Ref)
		}
		return refs
	}

	conv := in.conversation(t)
	a := task(owner, map[string]any{"title": "Оплата картой", "conversation_id": conv.Id, "priority": "high"}) // TSK-1
	b := task(op, map[string]any{"title": "Экран оплаты"})                                                     // TSK-2
	if rec := op.do(http.MethodPost, "/api/v1/tasks/"+a.Id.String()+"/links", map[string]any{"kind": "blocks", "task": "TSK-2"}); rec.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rec.Code, rec.Body)
	}
	crm := project("CRM", "CRM", ownerID)
	app := project("Приложение", "APP", ownerID, opID)
	crmQueue, appQueue := queue(crm.Id), queue(app.Id, ownerID)

	// Refused: a project the member is not in, a queue of another project, a
	// stale version, the same project.
	wantProblem(t, move(op, a, map[string]any{"project_id": crm.Id}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, move(op, a, map[string]any{"project_id": app.Id, "queue_id": crmQueue.Id}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, move(op, a, map[string]any{"project_id": app.Id, "version": a.Version + 1}), http.StatusConflict, "task_conflict")
	wantProblem(t, move(op, a, map[string]any{"project_id": a.ProjectId}), http.StatusBadRequest, "invalid_request")

	// Moved: APP-1 now; the conversation, the link, the priority stay.
	moved := decode[oapi.Task](t, move(op, a, map[string]any{"project_id": app.Id, "queue_id": appQueue.Id}))
	if moved.Ref != "APP-1" || moved.ProjectId != app.Id || moved.QueueId == nil || *moved.QueueId != appQueue.Id ||
		!slices.Contains(moved.ConversationIds, conv.Id) || moved.Priority != oapi.High || moved.Version != a.Version+1 {
		t.Fatalf("moved: %+v", moved)
	}
	if w := decode[oapi.Task](t, op.do(http.MethodGet, "/api/v1/tasks/"+b.Id.String(), nil)).WaitingOn; len(w) != 1 || *w[0].Ref != "APP-1" {
		t.Fatalf("the blocked one waits on: %+v", w)
	}
	// The old ref still finds it, in the search and as a link's other end.
	if got := search(op, "TSK-1"); !slices.Equal(got, []string{"APP-1"}) {
		t.Fatalf("search by the old ref: %v", got)
	}
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks/"+b.Id.String()+"/links", map[string]any{"kind": "relates", "task": "TSK-1"}),
		http.StatusConflict, "link_exists")
	// The next task of TSK does not take the old number.
	if c := task(op, map[string]any{"title": "Третья"}); c.Ref != "TSK-3" {
		t.Fatalf("next in TSK: %s", c.Ref)
	}
	feed := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, "/api/v1/tasks/"+a.Id.String()+"/activity", nil)).Items
	var lines []string
	for _, l := range feed {
		switch l.Kind {
		case oapi.TaskActivityKindMoved:
			lines = append(lines, "moved "+*l.From+"→"+*l.To)
		case oapi.TaskActivityKindQueue:
			lines = append(lines, "queue")
		}
	}
	if got := strings.Join(lines, ", "); got != "moved TSK-1→APP-1, queue" {
		t.Fatalf("feed: %s", got)
	}
	if n := e.ownerCount(`SELECT count(*) FROM outbox_events WHERE workspace_id = $1 AND topic = 'task.changed' AND payload->>'task_id' = $2`,
		ws, a.Id.String()); n < 2 {
		t.Fatalf("changed events: %d", n)
	}

	// An assignee who is not in the target project: refused, unless an admin
	// adds them to it.
	mine := task(op, map[string]any{"title": "Моя", "assignee_id": opID})
	wantProblem(t, move(owner, mine, map[string]any{"project_id": crm.Id}), http.StatusBadRequest, "invalid_request")
	if x := decode[oapi.Task](t, move(op, b, map[string]any{"project_id": app.Id, "assignee_id": ownerID})); x.Ref != "APP-2" {
		t.Fatalf("moved with a member: %s", x.Ref)
	}
	got := decode[oapi.Task](t, move(owner, mine, map[string]any{"project_id": crm.Id, "add_assignee_to_project": true}))
	if got.Ref != "CRM-1" || got.AssigneeId == nil || *got.AssigneeId != opID {
		t.Fatalf("moved with the assignee: %+v", got)
	}
	if got := search(op, "CRM-1"); !slices.Equal(got, []string{"CRM-1"}) {
		t.Fatal("the added member does not see the task")
	}

	// The key changes: the old refs still open the tasks, the old key is the
	// project's for good.
	crm = decode[oapi.TaskProject](t, owner.do(http.MethodPut, "/api/v1/task-projects/"+crm.Id.String(),
		map[string]any{"name": "Продажи", "key": "sales", "access": "members", "member_ids": []uuid.UUID{ownerID, opID}}))
	if crm.Key != "SALES" || !slices.Equal(crm.FormerKeys, []string{"CRM"}) {
		t.Fatalf("renamed: %+v", crm)
	}
	if got := search(op, "CRM-1"); !slices.Equal(got, []string{"SALES-1"}) {
		t.Fatalf("search by the old key: %v", got)
	}
	if got := search(op, "TSK-2"); !slices.Equal(got, []string{"APP-2"}) {
		t.Fatalf("search by an old ref: %v", got)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/task-projects", map[string]any{"name": "Ещё", "key": "crm", "access": "team", "member_ids": []uuid.UUID{}}),
		http.StatusConflict, "project_exists")
	wantProblem(t, owner.do(http.MethodPut, "/api/v1/task-projects/"+app.Id.String(),
		map[string]any{"name": "Приложение", "key": "CRM", "access": "members", "member_ids": []uuid.UUID{ownerID, opID}}), http.StatusConflict, "project_exists")
	back := decode[oapi.TaskProject](t, owner.do(http.MethodPut, "/api/v1/task-projects/"+crm.Id.String(),
		map[string]any{"name": "CRM", "key": "CRM", "access": "members", "member_ids": []uuid.UUID{ownerID, opID}}))
	if back.Key != "CRM" || !slices.Equal(back.FormerKeys, []string{"SALES"}) {
		t.Fatalf("the key back: %+v", back)
	}
	list := decode[oapi.TaskProjectList](t, op.do(http.MethodGet, "/api/v1/task-projects", nil)).Items
	for _, p := range list {
		if p.Id == crm.Id && !slices.Equal(p.FormerKeys, []string{"SALES"}) {
			t.Fatalf("listed: %+v", p)
		}
	}

	// An agent moves by refs.
	agent := &mcpAgent{t: t, c: e.client(), secret: issueToken(t, op, "Claude Code", "tasks:write").Secret}
	var res struct {
		MovedFrom string `json:"moved_from"`
		Task      struct {
			Ref string `json:"ref"`
		} `json:"task"`
	}
	if msg := agent.call("move_task", map[string]any{"number": "TSK-3", "project": "app"}, &res); msg != "" {
		t.Fatalf("move_task: %s", msg)
	}
	if res.MovedFrom != "TSK-3" || res.Task.Ref != "APP-3" {
		t.Fatalf("move_task: %+v", res)
	}
	if msg := agent.call("move_task", map[string]any{"number": "APP-3", "project": "CRM", "assignee": ownerID.String()}, nil); msg != "" {
		t.Fatalf("move_task with an assignee: %s", msg)
	}
	if msg := agent.call("move_task", map[string]any{"number": "CRM-2", "project": "APP", "assignee": "me", "add_assignee": true}, nil); msg != "" {
		t.Fatalf("move_task, a member of both: %s", msg)
	}
}
