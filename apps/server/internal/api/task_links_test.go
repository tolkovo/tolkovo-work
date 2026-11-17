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

// Links between tasks (ADR-0022, P3): a task waits while a task that
// blocks it is open, loops and second links are refused, a hidden task
// shows by its status only, and a duplicate hands its conversations to the
// original.
func TestTaskLinks(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	ownerID, opID := in.so.User.Id, in.sop.User.Id
	ws := in.so.Workspace.Id

	task := func(c *client, body map[string]any) oapi.Task {
		t.Helper()
		return decode[oapi.Task](t, c.do(http.MethodPost, "/api/v1/tasks", body))
	}
	get := func(c *client, id uuid.UUID) oapi.Task {
		t.Helper()
		return decode[oapi.Task](t, c.do(http.MethodGet, "/api/v1/tasks/"+id.String(), nil))
	}
	links := func(c *client, id uuid.UUID) []oapi.TaskLink {
		t.Helper()
		return decode[oapi.TaskLinkList](t, c.do(http.MethodGet, "/api/v1/tasks/"+id.String()+"/links", nil)).Items
	}
	link := func(c *client, id uuid.UUID, kind, other string) *httptest.ResponseRecorder {
		t.Helper()
		return c.do(http.MethodPost, "/api/v1/tasks/"+id.String()+"/links", map[string]any{"kind": kind, "task": other})
	}

	a := task(op, map[string]any{"title": "API оплаты"})                        // TSK-1
	b := task(op, map[string]any{"title": "Экран оплаты", "assignee_id": opID}) // TSK-2
	d := task(op, map[string]any{"title": "Релиз"})                             // TSK-3
	crm := decode[oapi.TaskProject](t, owner.do(http.MethodPost, "/api/v1/task-projects",
		map[string]any{"name": "CRM", "key": "CRM", "access": "members", "member_ids": []uuid.UUID{ownerID}}))
	secret := task(owner, map[string]any{"title": "Секретная", "project_id": crm.Id}) // CRM-1

	// TSK-1 blocks TSK-2: each side sees the link its way, TSK-2 waits.
	added := decode[oapi.TaskLinkList](t, link(op, a.Id, "blocks", "TSK-2"))
	if len(added.Items) != 1 || added.Items[0].Kind != oapi.Blocks || added.Items[0].Ref == nil || *added.Items[0].Ref != "TSK-2" {
		t.Fatalf("added: %+v", added)
	}
	if ls := links(op, b.Id); len(ls) != 1 || ls[0].Kind != oapi.WaitsFor || *ls[0].Ref != "TSK-1" || ls[0].Status != oapi.TaskStatusOpen {
		t.Fatalf("links of the blocked: %+v", ls)
	}
	if w := get(op, b.Id).WaitingOn; len(w) != 1 || w[0].Id != a.Id || *w[0].Ref != "TSK-1" {
		t.Fatalf("waiting on: %+v", w)
	}
	if w := get(op, a.Id).WaitingOn; len(w) != 0 {
		t.Fatalf("the blocker waits: %+v", w)
	}
	blocked := decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks?blocked=true", nil)).Items
	if len(blocked) != 1 || blocked[0].Ref != "TSK-2" || len(blocked[0].WaitingOn) != 1 {
		t.Fatalf("blocked list: %+v", blocked)
	}
	summary := decode[oapi.TaskSummary](t, op.do(http.MethodGet, "/api/v1/tasks/summary", nil))
	if len(summary.Projects) != 1 || summary.Projects[0].Waiting != 1 {
		t.Fatalf("summary: %+v", summary)
	}

	// One link a pair, no loops, not to itself.
	wantProblem(t, link(op, b.Id, "blocks", "TSK-1"), http.StatusConflict, "link_exists")
	if rec := link(op, b.Id, "blocks", d.Id.String()); rec.Code != http.StatusCreated {
		t.Fatalf("by id: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, link(op, d.Id, "blocks", "TSK-1"), http.StatusConflict, "link_loop")
	wantProblem(t, link(op, a.Id, "waits_for", "TSK-3"), http.StatusConflict, "link_loop")
	wantProblem(t, link(op, a.Id, "relates", "TSK-1"), http.StatusBadRequest, "invalid_request")

	// A hidden task cannot be linked by who does not see it; linked by an
	// admin, it shows by its status only, and may be unlinked.
	wantProblem(t, link(op, a.Id, "relates", "CRM-1"), http.StatusNotFound, "not_found")
	wantProblem(t, link(op, a.Id, "relates", secret.Id.String()), http.StatusNotFound, "not_found")
	if rec := link(owner, secret.Id, "relates", "TSK-1"); rec.Code != http.StatusCreated {
		t.Fatalf("admin links: %d %s", rec.Code, rec.Body)
	}
	ls := links(op, a.Id)
	if len(ls) != 2 || ls[1].TaskId != secret.Id || ls[1].Visible || ls[1].Ref != nil || ls[1].Title != nil || ls[1].Kind != oapi.Relates {
		t.Fatalf("hidden link: %+v", ls)
	}
	if rec := op.do(http.MethodDelete, "/api/v1/tasks/"+a.Id.String()+"/links/"+secret.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink hidden: %d %s", rec.Code, rec.Body)
	}
	if rec := op.do(http.MethodDelete, "/api/v1/tasks/"+a.Id.String()+"/links/"+secret.Id.String(), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unlink twice: %d %s", rec.Code, rec.Body)
	}

	// The feed of both tasks says so; the hidden task's ref is not told.
	feed := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, "/api/v1/tasks/"+a.Id.String()+"/activity", nil)).Items
	var lines []string
	for _, l := range feed {
		if l.Kind != oapi.TaskActivityKindLinkAdded && l.Kind != oapi.TaskActivityKindLinkRemoved {
			continue
		}
		ref := "?"
		if l.LinkRef != nil {
			ref = *l.LinkRef
		}
		lines = append(lines, string(l.Kind)+" "+string(*l.Link)+" "+ref)
	}
	if got := strings.Join(lines, ", "); got != "link_added blocks TSK-2, link_added relates ?, link_removed relates ?" {
		t.Fatalf("feed: %s", got)
	}
	ownerFeed := decode[oapi.TaskActivityList](t, owner.do(http.MethodGet, "/api/v1/tasks/"+a.Id.String()+"/activity", nil)).Items
	if l := ownerFeed[len(ownerFeed)-1]; l.LinkRef == nil || *l.LinkRef != "CRM-1" {
		t.Fatalf("owner's feed: %+v", l)
	}

	// The blocker closes: TSK-2 waits for nothing, and its assignee's panel
	// is told.
	a = get(owner, a.Id)
	if rec := owner.do(http.MethodPut, "/api/v1/tasks/"+a.Id.String(), taskUpdate(a, func(m map[string]any) { m["status"] = "done" })); rec.Code != http.StatusOK {
		t.Fatalf("close: %d %s", rec.Code, rec.Body)
	}
	if w := get(op, b.Id).WaitingOn; len(w) != 0 {
		t.Fatalf("still waiting: %+v", w)
	}
	if n := e.ownerCount(`SELECT count(*) FROM outbox_events WHERE workspace_id = $1 AND topic = 'task.changed'
		AND payload->>'task_id' = $2 AND (payload->>'unblocked')::boolean`, ws, b.Id.String()); n != 1 {
		t.Fatalf("unblocked events: %d", n)
	}
	if s := decode[oapi.TaskSummary](t, op.do(http.MethodGet, "/api/v1/tasks/summary", nil)); s.Projects[0].Waiting != 0 {
		t.Fatalf("summary after: %+v", s)
	}

	// A duplicate is canceled; its conversation goes to the original.
	conv := in.conversation(t)
	dup := task(owner, map[string]any{"title": "Опять оплата", "conversation_id": conv.Id})
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks/"+dup.Id.String()+"/duplicate", map[string]any{"original": "CRM-1", "version": dup.Version}),
		http.StatusNotFound, "not_found")
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/tasks/"+dup.Id.String()+"/duplicate", map[string]any{"original": "TSK-3", "version": dup.Version + 1}),
		http.StatusConflict, "task_conflict")
	closed := decode[oapi.Task](t, owner.do(http.MethodPost, "/api/v1/tasks/"+dup.Id.String()+"/duplicate",
		map[string]any{"original": "TSK-3", "version": dup.Version}))
	if closed.Status != oapi.TaskStatusCanceled || len(closed.ConversationIds) != 0 {
		t.Fatalf("duplicate: %+v", closed)
	}
	if orig := get(owner, d.Id); !slices.Contains(orig.ConversationIds, conv.Id) {
		t.Fatalf("original: %+v", orig)
	}
	if ls := links(owner, d.Id); ls[len(ls)-1].Kind != oapi.DuplicatedBy || ls[len(ls)-1].TaskId != dup.Id {
		t.Fatalf("links of the original: %+v", ls)
	}
	wantProblem(t, owner.do(http.MethodPost, "/api/v1/tasks/"+dup.Id.String()+"/duplicate", map[string]any{"original": "TSK-3", "version": closed.Version}),
		http.StatusBadRequest, "invalid_request")

	// An agent links by refs, as people do.
	agent := &mcpAgent{t: t, c: e.client(), secret: issueToken(t, op, "Claude Code", "tasks:write").Secret}
	var res struct {
		Task  string `json:"task"`
		Links []struct {
			Kind string `json:"kind"`
			Task string `json:"task"`
		} `json:"links"`
	}
	if msg := agent.call("link_tasks", map[string]any{"number": "TSK-2", "other": "TSK-4", "kind": "relates"}, &res); msg != "" {
		t.Fatalf("link_tasks: %s", msg)
	}
	if res.Task != "TSK-2" || len(res.Links) != 3 || res.Links[2].Kind != "relates" || res.Links[2].Task != "TSK-4" {
		t.Fatalf("link_tasks: %+v", res)
	}
	if msg := agent.call("link_tasks", map[string]any{"number": "TSK-2", "other": "TSK-4", "kind": "blocks"}, nil); !strings.Contains(msg, "linked already") {
		t.Fatalf("second link: %q", msg)
	}
	if msg := agent.call("link_tasks", map[string]any{"number": "TSK-2", "other": 4, "remove": true}, &res); msg != "" || len(res.Links) != 2 {
		t.Fatalf("remove: %q %+v", msg, res)
	}
}
