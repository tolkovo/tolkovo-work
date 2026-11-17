package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Projects of tasks (ADR-0022): numbers of their own, the members see the
// tasks of a project, the rest get 404 on every route of such a task.
func TestTaskProjects_AccessAndNumbers(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	ownerID, opID := in.so.User.Id, in.sop.User.Id

	// The default project comes with the first task; its key is TSK.
	first := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Принтер"}))
	if first.Ref != "TSK-1" || first.ProjectKey != "TSK" {
		t.Fatalf("default task: %+v", first)
	}

	// Projects are the admins'; a key is taken once, in any case.
	wantProblem(t, op.do(http.MethodPost, "/api/v1/task-projects", map[string]any{"name": "CRM", "key": "CRM"}),
		http.StatusForbidden, "forbidden")
	crm := decode[oapi.TaskProject](t, owner.do(http.MethodPost, "/api/v1/task-projects",
		map[string]any{"name": "CRM", "key": "crm", "icon": "chart", "access": "members", "member_ids": []uuid.UUID{ownerID}}))
	if crm.Key != "CRM" || crm.IsDefault || len(crm.MemberIds) != 1 {
		t.Fatalf("project: %+v", crm)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/task-projects", map[string]any{"name": "Другой", "key": "CRM"}),
		http.StatusConflict, "project_exists")

	// Numbers go per project.
	secret := decode[oapi.Task](t, owner.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Миграция", "project_id": crm.Id}))
	if secret.Ref != "CRM-1" || secret.Number != 1 || secret.ProjectId != crm.Id {
		t.Fatalf("project task: %+v", secret)
	}

	// The operator, not a member, sees neither the project nor its tasks;
	// an admin sees every project, a member or not.
	projects := decode[oapi.TaskProjectList](t, op.do(http.MethodGet, "/api/v1/task-projects", nil)).Items
	if len(projects) != 1 || !projects[0].IsDefault || !projects[0].Member {
		t.Fatalf("projects of the operator: %+v", projects)
	}
	projects = decode[oapi.TaskProjectList](t, owner.do(http.MethodGet, "/api/v1/task-projects", nil)).Items
	if len(projects) != 2 || projects[1].Id != crm.Id || !projects[1].Member || projects[1].OpenTasks != 1 {
		t.Fatalf("projects of the owner: %+v", projects)
	}
	refs := func(c *client, query string) string {
		var out []string
		for _, it := range decode[oapi.TaskPage](t, c.do(http.MethodGet, "/api/v1/tasks"+query, nil)).Items {
			out = append(out, it.Ref)
		}
		return strings.Join(out, " ")
	}
	if got := refs(op, ""); got != "TSK-1" {
		t.Fatalf("operator's list: %q", got)
	}
	if got := refs(op, "?project="+crm.Id.String()); got != "" {
		t.Fatalf("operator's list of the project: %q", got)
	}
	if got := refs(owner, ""); got != "CRM-1 TSK-1" {
		t.Fatalf("owner's list: %q", got)
	}
	if got := refs(owner, "?project="+crm.Id.String()); got != "CRM-1" {
		t.Fatalf("owner's list of the project: %q", got)
	}

	// Every route of the hidden task answers 404, as for a missing one.
	routes := 0
	err := chi.Walk(e.h.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodOptions || !strings.HasPrefix(route, "/api/v1/tasks/{id}") {
			return nil
		}
		routes++
		path := strings.NewReplacer("{id}", secret.Id.String(), "{conversation_id}", uuid.NewString(), "{file_id}", uuid.NewString(), "{other_id}", uuid.NewString()).Replace(route)
		if rec := op.do(method, path, map[string]any{}); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d %s", method, route, rec.Code, rec.Body)
		}
		return nil
	})
	if err != nil || routes < 8 {
		t.Fatalf("walk: %v, %d routes", err, routes)
	}

	// Nor can the operator create a task there, nor be its assignee.
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "project_id": crm.Id}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, owner.do(http.MethodPut, "/api/v1/tasks/"+secret.Id.String(), taskUpdate(secret, func(b map[string]any) {
		b["assignee_id"] = opID
	})), http.StatusBadRequest, "invalid_request")

	// A queue belongs to one project: a task of another is refused.
	q := decode[oapi.TaskQueue](t, owner.do(http.MethodPost, "/api/v1/task-queues",
		map[string]any{"name": "Разработка", "project_id": crm.Id, "member_ids": []uuid.UUID{}}))
	if q.ProjectId != crm.Id {
		t.Fatalf("queue: %+v", q)
	}
	if qs := decode[oapi.TaskQueueList](t, owner.do(http.MethodGet, "/api/v1/task-queues?project="+first.ProjectId.String(), nil)).Items; len(qs) != 0 {
		t.Fatalf("queues of the default project: %+v", qs)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "x", "queue_id": q.Id}),
		http.StatusBadRequest, "invalid_request")
	// A topic's queue is of the topic's project.
	topic := decode[oapi.Topic](t, owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "Оплата", "task_queue_id": q.Id}))
	if topic.TaskProjectId == nil || *topic.TaskProjectId != crm.Id {
		t.Fatalf("topic: %+v", topic)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "Доставка", "task_queue_id": q.Id, "task_project_id": first.ProjectId}),
		http.StatusBadRequest, "invalid_request")

	// A member of the project sees it all.
	crm = decode[oapi.TaskProject](t, owner.do(http.MethodPut, "/api/v1/task-projects/"+crm.Id.String(),
		map[string]any{"name": "CRM", "icon": "chart", "access": "members", "member_ids": []uuid.UUID{ownerID, opID}}))
	if got := refs(op, ""); got != "CRM-1 TSK-1" {
		t.Fatalf("member's list: %q", got)
	}
	if rec := op.do(http.MethodGet, "/api/v1/tasks/"+secret.Id.String(), nil); rec.Code != http.StatusOK {
		t.Fatalf("member's task: %d %s", rec.Code, rec.Body)
	}

	// The menu counts my open tasks in all and by project, for the project tiles.
	for _, id := range []uuid.UUID{first.Id, secret.Id} {
		task := decode[oapi.Task](t, op.do(http.MethodGet, "/api/v1/tasks/"+id.String(), nil))
		if rec := op.do(http.MethodPut, "/api/v1/tasks/"+id.String(), taskUpdate(task, func(b map[string]any) {
			b["assignee_id"] = opID
		})); rec.Code != http.StatusOK {
			t.Fatalf("assign %s: %d %s", task.Ref, rec.Code, rec.Body)
		}
	}
	summary := decode[oapi.TaskSummary](t, op.do(http.MethodGet, "/api/v1/tasks/summary", nil))
	if summary.AssignedOpen != 2 || len(summary.Projects) != 2 {
		t.Fatalf("summary: %+v", summary)
	}
	for _, p := range summary.Projects {
		if p.AssignedOpen != 1 || (p.ProjectId != crm.Id && p.ProjectId != first.ProjectId) {
			t.Fatalf("summary of a project: %+v", p)
		}
	}

	// A project with tasks is archived, not deleted; the default one is neither.
	wantProblem(t, owner.do(http.MethodDelete, "/api/v1/task-projects/"+crm.Id.String(), nil), http.StatusConflict, "project_not_empty")
	wantProblem(t, owner.do(http.MethodDelete, "/api/v1/task-projects/"+first.ProjectId.String(), nil),
		http.StatusBadRequest, "invalid_request")
	empty := decode[oapi.TaskProject](t, owner.do(http.MethodPost, "/api/v1/task-projects", map[string]any{"name": "Пустой", "key": "EMP"}))
	if rec := owner.do(http.MethodDelete, "/api/v1/task-projects/"+empty.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete empty: %d %s", rec.Code, rec.Body)
	}
}
