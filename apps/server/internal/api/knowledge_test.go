package api

import (
	"net/http"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Owners and admins fill the knowledge base; operators see none of it.
func TestKnowledgeAPI(t *testing.T) {
	in := setupInbox(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/kb/articles"}, {http.MethodGet, "/api/v1/kb/search?q=x"}, {http.MethodGet, "/api/v1/kb/guide"},
	} {
		wantProblem(t, in.op.do(c.method, c.path, nil), http.StatusForbidden, "forbidden")
	}
	wantProblem(t, in.op.do(http.MethodPost, "/api/v1/kb/articles", map[string]any{"kind": "qa", "title": "a", "body": "b"}), http.StatusForbidden, "forbidden")

	topic := decode[oapi.Topic](t, in.owner.do(http.MethodPost, "/api/v1/topics", map[string]any{"name": "CRM"}))
	rec := in.owner.do(http.MethodPost, "/api/v1/kb/articles", map[string]any{
		"kind": "article", "title": "Как подключить аккаунт", "body": "Откройте «Настройки → Аккаунты».",
		"url": "https://help.example.test/connect", "topic_ids": []string{topic.Id.String()},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	a := decode[oapi.KBArticle](t, rec)
	if !a.Published || len(a.TopicIds) != 1 || a.Url == nil {
		t.Fatalf("created %+v", a)
	}
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/kb/articles", map[string]any{"kind": "faq", "title": "a", "body": "b"}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/kb/articles", map[string]any{"kind": "qa", "title": "a", "body": "b", "workspace_id": in.sop.Workspace.Id}), http.StatusBadRequest, "invalid_request")

	found := decode[oapi.KBSearchResult](t, in.owner.do(http.MethodGet, "/api/v1/kb/search?q=не+могу+подключить+аккаунт&topic="+topic.Id.String(), nil))
	if len(found.Items) != 1 || found.Items[0].Id != a.Id || found.Items[0].Excerpt == "" {
		t.Fatalf("search %+v", found)
	}

	upd := decode[oapi.KBArticle](t, in.owner.do(http.MethodPut, "/api/v1/kb/articles/"+a.Id.String(), map[string]any{
		"kind": "article", "title": "Подключение аккаунта", "body": "Шаги.", "published": false,
	}))
	if upd.Published || upd.Url != nil || len(upd.TopicIds) != 0 {
		t.Fatalf("updated %+v", upd)
	}
	if got := decode[oapi.KBArticle](t, in.owner.do(http.MethodGet, "/api/v1/kb/articles/"+a.Id.String(), nil)); got.Title != "Подключение аккаунта" {
		t.Fatalf("read %+v", got)
	}

	guide := decode[oapi.KBGuide](t, in.owner.do(http.MethodPut, "/api/v1/kb/guide", map[string]any{"about": "CRM для агентств.", "handoff_rules": "Оплата — к человеку.", "task_checklist": "Email аккаунта"}))
	if guide.About != "CRM для агентств." || guide.TaskChecklist != "Email аккаунта" || guide.UpdatedAt == nil {
		t.Fatalf("guide %+v", guide)
	}
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/kb/guide", map[string]any{"about": "x"}), http.StatusBadRequest, "invalid_request")

	// The file of an agent: topics by name, notes it added are let through.
	res := decode[oapi.KBImportResult](t, in.owner.do(http.MethodPost, "/api/v1/kb/import", map[string]any{
		"format": "tolkovo-knowledge/1", "generated_by": "agent notes",
		"about": "Зерно — CRM для агентств OnlyFans и Fansly.", "task_checklist": "Email аккаунта\nВерсия приложения",
		"articles": []map[string]any{
			{"kind": "article", "title": "подключение аккаунта", "body": "Шаг 1. Шаг 2.", "topics": []string{"crm", "Fansly"}, "note": "from the code"},
			{"kind": "qa", "title": "Есть ли экспорт в Excel?", "body": "Пока нет.", "topics": []string{"Fansly"}},
		},
	}))
	if res.Created != 1 || res.Updated != 1 || len(res.UnknownTopics) != 1 || res.UnknownTopics[0] != "Fansly" {
		t.Fatalf("import %+v", res)
	}
	list := decode[oapi.KBArticleList](t, in.owner.do(http.MethodGet, "/api/v1/kb/articles", nil))
	if len(list.Items) != 2 {
		t.Fatalf("list %+v", list)
	}
	for _, item := range list.Items {
		if item.Id == a.Id && (item.Title != "подключение аккаунта" || len(item.TopicIds) != 1 || !item.Published) {
			t.Fatalf("replaced by import %+v", item)
		}
	}
	if g := decode[oapi.KBGuide](t, in.owner.do(http.MethodGet, "/api/v1/kb/guide", nil)); g.HandoffRules != "Оплата — к человеку." || g.TaskChecklist != "Email аккаунта\nВерсия приложения" || g.About != "Зерно — CRM для агентств OnlyFans и Fansly." {
		t.Fatalf("guide after import %+v", g)
	}
	wantProblem(t, in.owner.do(http.MethodPost, "/api/v1/kb/import", map[string]any{"format": "other/2", "articles": []any{}}), http.StatusBadRequest, "invalid_request")

	if rec := in.owner.do(http.MethodDelete, "/api/v1/kb/articles/"+a.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, in.owner.do(http.MethodGet, "/api/v1/kb/articles/"+a.Id.String(), nil), http.StatusNotFound, "not_found")
}
