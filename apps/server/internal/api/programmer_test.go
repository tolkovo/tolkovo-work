package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// The programmer reads only the conversations linked to a task and leaves
// notes there; the inbox, replies, customers' contacts and assignments are
// not theirs (ADR-0020).
func TestProgrammer_SeesLinkedConversations(t *testing.T) {
	in := setupInbox(t)
	dev, devID := in.joinAs(t, "dev@example.test", "dev-home", tenancy.RoleProgrammer)

	me := decode[oapi.Session](t, dev.do(http.MethodGet, "/api/v1/me", nil))
	if me.Role != oapi.RoleProgrammer || slices.Contains(me.Permissions, "conversations.view") ||
		!slices.Contains(me.Permissions, "conversations.view_linked") || !slices.Contains(me.Permissions, "tasks.edit") {
		t.Fatalf("programmer: %+v", me)
	}

	linked, other := in.conversation(t), in.conversation(t)
	att := decode[oapi.AttachmentUpload](t, in.owner.do(http.MethodPost, "/api/v1/conversations/"+linked.Id.String()+"/attachments",
		map[string]any{"file_name": "log.txt", "content_type": "text/plain", "size": 2}))
	in.putFile(t, att.Upload, []byte("ok"))
	if rec := in.owner.do(http.MethodPost, "/api/v1/conversations/"+linked.Id.String()+"/messages",
		map[string]any{"body": "лог", "private": true, "attachment_ids": []uuid.UUID{att.Attachment.Id}}); rec.Code != http.StatusCreated {
		t.Fatalf("note with file: %d %s", rec.Code, rec.Body)
	}
	if rec := in.op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Ошибка оплаты", "conversation_id": linked.Id}); rec.Code != http.StatusCreated {
		t.Fatalf("task: %d %s", rec.Code, rec.Body)
	}

	base, otherBase := "/api/v1/conversations/"+linked.Id.String(), "/api/v1/conversations/"+other.Id.String()
	if c := decode[oapi.Conversation](t, dev.do(http.MethodGet, base, nil)); c.Id != linked.Id || c.Contact.Name != "Ivan" {
		t.Fatalf("linked conversation: %+v", c)
	}
	if page := decode[oapi.MessagePage](t, dev.do(http.MethodGet, base+"/messages", nil)); len(page.Items) == 0 {
		t.Fatal("no messages")
	}
	if rec := dev.do(http.MethodGet, "/api/v1/attachments/"+att.Attachment.Id.String()+"/download-link", nil); rec.Code != http.StatusOK {
		t.Fatalf("file of a linked conversation: %d %s", rec.Code, rec.Body)
	}
	if rec := dev.do(http.MethodPost, base+"/messages", map[string]any{"body": "Нашёл причину", "private": true}); rec.Code != http.StatusCreated {
		t.Fatalf("note: %d %s", rec.Code, rec.Body)
	}
	if rec := dev.do(http.MethodGet, base+"/tasks", nil); rec.Code != http.StatusOK {
		t.Fatalf("tasks of a linked conversation: %d %s", rec.Code, rec.Body)
	}

	// Another conversation does not exist for them; the inbox and replies are not theirs.
	wantProblem(t, dev.do(http.MethodGet, otherBase, nil), http.StatusNotFound, "not_found")
	wantProblem(t, dev.do(http.MethodGet, otherBase+"/messages", nil), http.StatusNotFound, "not_found")
	wantProblem(t, dev.do(http.MethodPost, otherBase+"/messages", map[string]any{"body": "x", "private": true}), http.StatusNotFound, "not_found")
	wantProblem(t, dev.do(http.MethodPost, otherBase+"/attachments", map[string]any{"file_name": "a.txt", "content_type": "text/plain", "size": 1}),
		http.StatusNotFound, "not_found")
	wantProblem(t, dev.do(http.MethodGet, "/api/v1/conversations", nil), http.StatusForbidden, "forbidden")
	wantProblem(t, dev.do(http.MethodPost, base+"/messages", map[string]any{"body": "Здравствуйте"}), http.StatusForbidden, "forbidden")
	wantProblem(t, dev.do(http.MethodGet, "/api/v1/contacts/"+linked.Contact.Id.String(), nil), http.StatusForbidden, "forbidden")
	wantProblem(t, dev.do(http.MethodPut, base+"/assignee", map[string]any{"assignee_id": devID}), http.StatusForbidden, "forbidden")

	// Nobody gives them a conversation.
	cur := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, otherBase, nil))
	wantProblem(t, in.owner.do(http.MethodPut, otherBase+"/assignee", map[string]any{"assignee_id": devID, "version": cur.Version}),
		http.StatusBadRequest, "invalid_request")
}

// A task put in review goes to its author for checking and back to the
// programmer in progress; a chosen assignee wins (ADR-0020).
func TestProgrammer_ReviewGoesBackToAuthor(t *testing.T) {
	in := setupInbox(t)
	op, opID := in.op, in.sop.User.Id
	dev, devID := in.joinAs(t, "dev@example.test", "dev-home", tenancy.RoleProgrammer)

	task := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Кнопка оплаты", "assignee_id": devID}))
	base := "/api/v1/tasks/" + task.Id.String()
	put := func(c *client, cur oapi.Task, edit func(map[string]any)) oapi.Task {
		t.Helper()
		rec := c.do(http.MethodPut, base, taskUpdate(cur, edit))
		if rec.Code != http.StatusOK {
			t.Fatalf("update: %d %s", rec.Code, rec.Body)
		}
		return decode[oapi.Task](t, rec)
	}
	task = put(dev, task, func(b map[string]any) { b["status"] = "in_progress" })
	task = put(dev, task, func(b map[string]any) { b["status"] = "review" })
	if task.AssigneeId == nil || *task.AssigneeId != opID {
		t.Fatalf("review goes to the author: %+v", task.AssigneeId)
	}
	// The check failed: back in progress, back to the programmer.
	task = put(op, task, func(b map[string]any) { b["status"] = "in_progress" })
	if task.AssigneeId == nil || *task.AssigneeId != devID {
		t.Fatalf("back in progress: %+v", task.AssigneeId)
	}
	// A chosen assignee wins over the return.
	_, adminID := in.joinAs(t, "admin@example.test", "admin-home", tenancy.RoleAdmin)
	task = put(dev, task, func(b map[string]any) { b["status"] = "review"; b["assignee_id"] = adminID })
	if task.AssigneeId == nil || *task.AssigneeId != adminID {
		t.Fatalf("chosen reviewer: %+v", task.AssigneeId)
	}
	task = put(in.owner, task, func(b map[string]any) { b["status"] = "in_progress" })
	if task.AssigneeId == nil || *task.AssigneeId != adminID {
		t.Fatalf("nobody to hand back to, the reviewer keeps it: %+v", task.AssigneeId)
	}
	// The author left the team: the task stays with the programmer.
	task = put(in.owner, task, func(b map[string]any) { b["assignee_id"] = devID })
	if rec := in.owner.do(http.MethodPost, "/api/v1/members/"+opID.String()+"/deactivate", map[string]any{}); rec.Code != http.StatusNoContent {
		t.Fatalf("remove author: %d %s", rec.Code, rec.Body)
	}
	task = put(dev, task, func(b map[string]any) { b["status"] = "review" })
	if task.AssigneeId == nil || *task.AssigneeId != devID {
		t.Fatalf("author gone, the task stays: %+v", task.AssigneeId)
	}
	// A message goes to the author when the task comes back to them.
	if n := in.e.ownerCount(`SELECT count(*) FROM outbox_events WHERE topic = 'task.changed' AND payload->>'assigned' = 'true' AND payload->>'assignee_id' = $1`, opID.String()); n != 1 {
		t.Fatalf("assigned events for the author: %d", n)
	}
}

// Made a programmer, an operator gives up their conversations; tasks stay.
func TestProgrammer_RoleChangeHandsOverConversations(t *testing.T) {
	in := setupInbox(t)
	op, opID := in.op, in.sop.User.Id
	c := in.conversation(t)
	if rec := op.do(http.MethodPut, "/api/v1/conversations/"+c.Id.String()+"/assignee",
		map[string]any{"assignee_id": opID, "version": c.Version}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	task := decode[oapi.Task](t, in.owner.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Отчёт", "assignee_id": opID}))
	_, progID := in.joinAs(t, "dev@example.test", "dev-home", tenancy.RoleProgrammer)

	path := "/api/v1/members/" + opID.String()
	wantProblem(t, in.owner.do(http.MethodPatch, path, map[string]any{"role": "programmer", "conversations_to": progID}),
		http.StatusBadRequest, "invalid_request")
	rec := in.owner.do(http.MethodPatch, path, map[string]any{"role": "programmer"})
	if rec.Code != http.StatusOK || decode[oapi.Member](t, rec).Role != oapi.RoleProgrammer {
		t.Fatalf("make programmer: %d %s", rec.Code, rec.Body)
	}
	got := decode[oapi.Conversation](t, in.owner.do(http.MethodGet, "/api/v1/conversations/"+c.Id.String(), nil))
	if got.Status != "queued" || got.AssigneeId != nil {
		t.Fatalf("conversation back in the queue: %+v", got)
	}
	if kept := decode[oapi.Task](t, in.owner.do(http.MethodGet, "/api/v1/tasks/"+task.Id.String(), nil)); kept.AssigneeId == nil || *kept.AssigneeId != opID {
		t.Fatalf("task kept: %+v", kept.AssigneeId)
	}
	wantProblem(t, op.do(http.MethodGet, "/api/v1/conversations", nil), http.StatusForbidden, "forbidden")
}
