package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// One customer in every project and the articles of the knowledge base
// linked to tasks (ADR-0022, P5): a task the caller does not see is only
// counted, never named.
func TestContactTasksAndArticles(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	ownerID := in.so.User.Id
	ws := in.so.Workspace.Id
	dev, _ := in.joinAs(t, "dev@example.test", "dev-home", tenancy.RoleProgrammer)

	task := func(body map[string]any) oapi.Task {
		t.Helper()
		return decode[oapi.Task](t, owner.do(http.MethodPost, "/api/v1/tasks", body))
	}
	refs := func(l oapi.TaskBriefList) []string {
		var out []string
		for _, x := range l.Items {
			out = append(out, x.Ref)
		}
		return out
	}

	first := in.conversation(t)
	contact := first.Contact.Id
	second := decode[oapi.Conversation](t, owner.do(http.MethodPost, "/api/v1/conversations", map[string]any{"contact_id": contact, "subject": "Ещё"}))
	other := in.conversation(t) // another customer
	crm := decode[oapi.TaskProject](t, owner.do(http.MethodPost, "/api/v1/task-projects",
		map[string]any{"name": "CRM", "key": "CRM", "access": "members", "member_ids": []uuid.UUID{ownerID}}))
	a := task(map[string]any{"title": "Не проходит оплата", "conversation_id": first.Id})                      // TSK-1
	b := task(map[string]any{"title": "Импорт контактов", "project_id": crm.Id, "conversation_id": second.Id}) // CRM-1
	task(map[string]any{"title": "Чужой клиент", "conversation_id": other.Id})                                 // TSK-2

	// The customer's tasks: all of them for an admin; the operator sees TSK
	// and only counts the task of a project they are not in.
	path := "/api/v1/contacts/" + contact.String() + "/tasks"
	if l := decode[oapi.TaskBriefList](t, owner.do(http.MethodGet, path, nil)); !slices.Equal(refs(l), []string{"CRM-1", "TSK-1"}) || l.Hidden != 0 {
		t.Fatalf("admin: %+v", l)
	}
	l := decode[oapi.TaskBriefList](t, op.do(http.MethodGet, path, nil))
	if !slices.Equal(refs(l), []string{"TSK-1"}) || l.Hidden != 1 || l.Items[0].Title != "Не проходит оплата" || l.Items[0].Status != oapi.TaskStatusOpen {
		t.Fatalf("operator: %+v", l)
	}
	// The programmer does not see contacts.
	wantProblem(t, dev.do(http.MethodGet, path, nil), http.StatusForbidden, "forbidden")
	if l := decode[oapi.TaskBriefList](t, op.do(http.MethodGet, "/api/v1/contacts/"+uuid.NewString()+"/tasks", nil)); len(l.Items) != 0 || l.Hidden != 0 {
		t.Fatalf("unknown contact: %+v", l)
	}

	// An article linked to tasks of two projects.
	article := decode[oapi.KBArticle](t, owner.do(http.MethodPost, "/api/v1/kb/articles",
		map[string]any{"kind": "article", "title": "Оплата и возвраты", "body": "Как вернуть деньги."}))
	articles := func(c *client, id uuid.UUID) []oapi.TaskArticle {
		t.Helper()
		return decode[oapi.TaskArticleList](t, c.do(http.MethodGet, "/api/v1/tasks/"+id.String()+"/articles", nil)).Items
	}
	add := func(c *client, id uuid.UUID, articleID uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		return c.do(http.MethodPost, "/api/v1/tasks/"+id.String()+"/articles", map[string]any{"article_id": articleID})
	}
	before := e.ownerCount(`SELECT count(*) FROM outbox_events WHERE workspace_id = $1 AND topic = 'task.changed'`, ws)
	added := decode[oapi.TaskArticleList](t, add(owner, a.Id, article.Id))
	if len(added.Items) != 1 || added.Items[0].Title != "Оплата и возвраты" || added.Items[0].Kind != oapi.Article || !added.Items[0].Published {
		t.Fatalf("added: %+v", added)
	}
	if again := decode[oapi.TaskArticleList](t, add(owner, a.Id, article.Id)); len(again.Items) != 1 {
		t.Fatalf("twice: %+v", again)
	}
	if n := e.ownerCount(`SELECT count(*) FROM outbox_events WHERE workspace_id = $1 AND topic = 'task.changed'`, ws); n != before+1 {
		t.Fatalf("changed events: %d → %d", before, n)
	}
	decode[oapi.TaskArticleList](t, add(owner, b.Id, article.Id))
	wantProblem(t, add(owner, a.Id, uuid.New()), http.StatusBadRequest, "invalid_request")
	// Who sees the task reads its articles; linking takes the knowledge base.
	if as := articles(op, a.Id); len(as) != 1 || as[0].Id != article.Id {
		t.Fatalf("operator reads: %+v", as)
	}
	wantProblem(t, add(op, a.Id, article.Id), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodGet, "/api/v1/tasks/"+b.Id.String()+"/articles", nil), http.StatusNotFound, "not_found")

	// The tasks of the article, for who keeps the knowledge base.
	atPath := "/api/v1/kb/articles/" + article.Id.String() + "/tasks"
	if l := decode[oapi.TaskBriefList](t, owner.do(http.MethodGet, atPath, nil)); !slices.Equal(refs(l), []string{"CRM-1", "TSK-1"}) {
		t.Fatalf("article tasks: %+v", l)
	}
	wantProblem(t, op.do(http.MethodGet, atPath, nil), http.StatusForbidden, "forbidden")

	// Unlinked once; a deleted article leaves no links.
	del := "/api/v1/tasks/" + a.Id.String() + "/articles/" + article.Id.String()
	if rec := owner.do(http.MethodDelete, del, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, owner.do(http.MethodDelete, del, nil), http.StatusNotFound, "not_found")
	if rec := owner.do(http.MethodDelete, "/api/v1/kb/articles/"+article.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete article: %d %s", rec.Code, rec.Body)
	}
	if as := articles(owner, b.Id); len(as) != 0 {
		t.Fatalf("after the article is gone: %+v", as)
	}
}
