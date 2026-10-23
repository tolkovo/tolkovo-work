package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// joinAs adds a new user to the inbox's workspace with role and returns
// their client there.
func (in inbox) joinAs(t *testing.T, email, slug, role string) (*client, uuid.UUID) {
	t.Helper()
	_, s := in.e.register(email, slug)
	in.e.addMember(in.so.Workspace.Id, s.User.Id, role)
	c, _ := in.e.login(email)
	if rec := c.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": in.so.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	return c, s.User.Id
}

// Owners and admins change roles; the member works with the new rights at
// once; the owner and oneself are left alone (ADR-0020).
func TestMembers_ChangeRole(t *testing.T) {
	in := setupInbox(t)
	op, opID, ownerID := in.op, in.sop.User.Id, in.so.User.Id
	admin, adminID := in.joinAs(t, "admin@example.test", "admin-home", tenancy.RoleAdmin)
	path := func(id uuid.UUID) string { return "/api/v1/members/" + id.String() }

	wantProblem(t, op.do(http.MethodPatch, path(adminID), map[string]any{"role": "operator"}), http.StatusForbidden, "forbidden")
	wantProblem(t, in.owner.do(http.MethodPatch, path(ownerID), map[string]any{"role": "admin"}), http.StatusForbidden, "protected_member")
	wantProblem(t, admin.do(http.MethodPatch, path(ownerID), map[string]any{"role": "admin"}), http.StatusForbidden, "protected_member")
	wantProblem(t, admin.do(http.MethodPatch, path(adminID), map[string]any{"role": "operator"}), http.StatusForbidden, "protected_member")
	wantProblem(t, in.owner.do(http.MethodPatch, path(opID), map[string]any{"role": "owner"}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPatch, path(uuid.New()), map[string]any{"role": "admin"}), http.StatusNotFound, "not_found")

	if can := decode[oapi.Session](t, op.do(http.MethodGet, "/api/v1/me", nil)).Permissions; slices.Contains(can, "members.manage") {
		t.Fatalf("operator can manage members: %v", can)
	}
	rec := admin.do(http.MethodPatch, path(opID), map[string]any{"role": "admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body)
	}
	if m := decode[oapi.Member](t, rec); m.Role != oapi.RoleAdmin || m.UserId != opID || !m.Active {
		t.Fatalf("promoted: %+v", m)
	}
	me := decode[oapi.Session](t, op.do(http.MethodGet, "/api/v1/me", nil))
	if me.Role != oapi.RoleAdmin || !slices.Contains(me.Permissions, "members.manage") {
		t.Fatalf("new rights: %+v", me)
	}
	// The same role again changes nothing and tells nobody.
	events := in.e.ownerCount(`SELECT count(*) FROM outbox_events WHERE topic = 'member.changed'`)
	if rec := in.owner.do(http.MethodPatch, path(opID), map[string]any{"role": "admin"}); rec.Code != http.StatusOK {
		t.Fatalf("same role: %d %s", rec.Code, rec.Body)
	}
	if events != 1 || in.e.ownerCount(`SELECT count(*) FROM outbox_events WHERE topic = 'member.changed'`) != 1 {
		t.Fatalf("member.changed events: %d", events)
	}
	// Back to operator: the open conversation stays, an operator answers.
	c := in.conversation(t)
	if rec := op.do(http.MethodPut, "/api/v1/conversations/"+c.Id.String()+"/assignee",
		map[string]any{"assignee_id": opID, "version": c.Version}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	if rec := in.owner.do(http.MethodPatch, path(opID), map[string]any{"role": "operator"}); rec.Code != http.StatusOK {
		t.Fatalf("demote: %d %s", rec.Code, rec.Body)
	}
	got := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+c.Id.String(), nil))
	if got.AssigneeId == nil || *got.AssigneeId != opID {
		t.Fatalf("conversation after demote: %+v", got)
	}
	wantProblem(t, op.do(http.MethodPatch, path(adminID), map[string]any{"role": "operator"}), http.StatusForbidden, "forbidden")
}

// A member removed from the team loses access and their tokens; their
// conversations and tasks go to whom the admin chose (ADR-0020).
func TestMembers_Deactivate(t *testing.T) {
	in := setupInbox(t)
	op, opID := in.op, in.sop.User.Id
	_, adminID := in.joinAs(t, "admin@example.test", "admin-home", tenancy.RoleAdmin)
	path := "/api/v1/members/" + opID.String() + "/deactivate"

	c := in.conversation(t)
	if rec := op.do(http.MethodPut, "/api/v1/conversations/"+c.Id.String()+"/assignee",
		map[string]any{"assignee_id": opID, "version": c.Version}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	task := decode[oapi.Task](t, in.owner.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Сертификат", "assignee_id": opID}))
	queue := decode[oapi.TaskQueue](t, in.owner.do(http.MethodPost, "/api/v1/task-queues",
		map[string]any{"name": "Разработка", "member_ids": []uuid.UUID{opID, adminID}}))
	token := issueToken(t, op, "agent", "tasks:read")

	wantProblem(t, op.do(http.MethodPost, "/api/v1/members/"+adminID.String()+"/deactivate", map[string]any{}), http.StatusForbidden, "forbidden")
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/members/"+in.so.User.Id.String()+"/deactivate", map[string]any{}),
		http.StatusForbidden, "protected_member")
	wantProblem(t, in.owner.do(http.MethodPost, path, map[string]any{"conversations_to": opID}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPost, path, map[string]any{"tasks_to": uuid.New()}), http.StatusBadRequest, "invalid_request")

	if rec := in.owner.do(http.MethodPost, path, map[string]any{"conversations_to": adminID}); rec.Code != http.StatusNoContent {
		t.Fatalf("deactivate: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, in.owner.do(http.MethodPost, path, map[string]any{}), http.StatusNotFound, "not_found")

	// Access ends: the session and the token.
	if rec := op.do(http.MethodGet, "/api/v1/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after removal: %d %s", rec.Code, rec.Body)
	}
	if n := in.e.ownerCount(`SELECT count(*) FROM access_tokens WHERE user_id = $1`, opID); n != 0 {
		t.Fatalf("tokens left: %d", n)
	}
	bearer := in.e.client()
	if rec := bearer.do(http.MethodGet, "/api/v1/tasks", nil, header("Authorization", "Bearer "+token.Secret)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token after removal: %d %s", rec.Code, rec.Body)
	}

	got := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+c.Id.String(), nil))
	if got.Status != "assigned" || got.AssigneeId == nil || *got.AssigneeId != adminID {
		t.Fatalf("conversation handed over: %+v", got)
	}
	gotTask := decode[oapi.Task](t, in.owner.do(http.MethodGet, "/api/v1/tasks/"+task.Id.String(), nil))
	if gotTask.AssigneeId != nil || gotTask.Version != task.Version+1 {
		t.Fatalf("task handed over: %+v", gotTask)
	}
	if n := in.e.ownerCount(`SELECT count(*) FROM task_activity WHERE task_id = $1 AND kind = 'assignee'`, task.Id); n != 1 {
		t.Fatalf("assignee lines: %d", n)
	}
	q := decode[oapi.TaskQueueList](t, in.owner.do(http.MethodGet, "/api/v1/task-queues", nil))
	for _, it := range q.Items {
		if it.Id == queue.Id && (len(it.MemberIds) != 1 || it.MemberIds[0] != adminID) {
			t.Fatalf("queue members: %v", it.MemberIds)
		}
	}
	members := decode[oapi.MemberList](t, in.owner.do(http.MethodGet, "/api/v1/members", nil)).Items
	for _, m := range members {
		if m.UserId == opID && (m.Active || m.Presence != oapi.PresenceOffline) {
			t.Fatalf("removed member listed as %+v", m)
		}
	}

	// An invitation brings them back, with no conversations or tasks.
	if rec := in.owner.do(http.MethodPost, "/api/v1/invitations", map[string]any{"email": "operator@example.test", "role": "operator"}); rec.Code != http.StatusCreated {
		t.Fatalf("invite again: %d %s", rec.Code, rec.Body)
	}
}
