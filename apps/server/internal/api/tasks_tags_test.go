package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Tags of tasks: any member adds one and tags tasks, admins rename and
// delete them; the list filters by a tag; the feed records each change;
// another workspace's tags work nowhere here.
func TestTasks_Tags(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op

	// Any member adds a tag; names are unique without regard to case.
	rec := op.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": "  Дизайн  ", "color": "violet"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tag: %d %s", rec.Code, rec.Body)
	}
	design := decode[oapi.Tag](t, rec)
	if design.Name != "Дизайн" || design.Color != oapi.Violet {
		t.Fatalf("tag: %+v", design)
	}
	ui := decode[oapi.Tag](t, op.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": "Интерфейс"}))
	if ui.Color != oapi.Gray {
		t.Fatalf("default color: %+v", ui)
	}
	wantProblem(t, op.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": "дизайн"}), http.StatusConflict, "tag_exists")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": "Цвет", "color": "pink"}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": "   "}), http.StatusBadRequest, "invalid_request")
	// The tags of conversations are another list.
	if got := decode[oapi.TagList](t, op.do(http.MethodGet, "/api/v1/tags", nil)); len(got.Items) != 0 {
		t.Fatalf("conversation tags: %+v", got)
	}

	// A task with tags; the list returns them too.
	task := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks",
		map[string]any{"title": "Кнопка уезжает на телефоне", "tag_ids": []uuid.UUID{ui.Id, design.Id}}))
	if fmt.Sprint(task.TagIds) != fmt.Sprint([]uuid.UUID{ui.Id, design.Id}) {
		t.Fatalf("created tags: %v", task.TagIds)
	}
	plain := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Обновить сертификат"}))
	if plain.TagIds == nil || len(plain.TagIds) != 0 {
		t.Fatalf("no tags: %v", plain.TagIds)
	}
	list := func(query string) string {
		page := decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks"+query, nil))
		out := []string{}
		for _, it := range page.Items {
			out = append(out, fmt.Sprintf("%d:%d", it.Number, len(it.TagIds)))
		}
		return strings.Join(out, " ")
	}
	for query, want := range map[string]string{
		"":                           "2:0 1:2",
		"?tag=" + design.Id.String(): "1:2",
		"?tag=" + uuid.NewString():   "",
	} {
		if got := list(query); got != want {
			t.Errorf("list %q: %q, want %q", query, got, want)
		}
	}
	wantProblem(t, op.do(http.MethodGet, "/api/v1/tasks?tag=design", nil), http.StatusBadRequest, "invalid_request")

	// Changing the tags writes one line of the feed; a client that does not
	// know tags keeps them.
	base := "/api/v1/tasks/" + task.Id.String()
	changed := decode[oapi.Task](t, op.do(http.MethodPut, base, taskUpdate(task, func(b map[string]any) { b["tag_ids"] = []uuid.UUID{design.Id} })))
	if fmt.Sprint(changed.TagIds) != fmt.Sprint([]uuid.UUID{design.Id}) || changed.Version != task.Version+1 {
		t.Fatalf("changed: %+v", changed)
	}
	kept := decode[oapi.Task](t, op.do(http.MethodPut, base, taskUpdate(changed, func(b map[string]any) { b["priority"] = "high" })))
	if fmt.Sprint(kept.TagIds) != fmt.Sprint([]uuid.UUID{design.Id}) {
		t.Fatalf("tags lost by an update without them: %v", kept.TagIds)
	}
	feed := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, base+"/activity", nil)).Items
	var kinds []string
	for _, a := range feed {
		kinds = append(kinds, string(a.Kind))
	}
	if got := strings.Join(kinds, ","); got != "created,tags,priority" {
		t.Fatalf("feed: %s", got)
	}
	if l := feed[1]; l.TagsAdded == nil || len(*l.TagsAdded) != 0 || l.TagsRemoved == nil || fmt.Sprint(*l.TagsRemoved) != fmt.Sprint([]uuid.UUID{ui.Id}) {
		t.Fatalf("tags line: %+v", l)
	}

	// At most 10 tags, each once.
	var many []uuid.UUID
	for i := range 11 {
		many = append(many, decode[oapi.Tag](t, op.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": fmt.Sprintf("Тег %d", i)})).Id)
	}
	wantProblem(t, op.do(http.MethodPut, base, taskUpdate(kept, func(b map[string]any) { b["tag_ids"] = many })),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPut, base, taskUpdate(kept, func(b map[string]any) { b["tag_ids"] = []uuid.UUID{ui.Id, ui.Id} })),
		http.StatusBadRequest, "invalid_request")

	// Renaming and deleting are the admins'; a deleted tag leaves its tasks.
	tagURL := "/api/v1/task-tags/" + design.Id.String()
	wantProblem(t, op.do(http.MethodPut, tagURL, map[string]any{"name": "Дизайн и UX"}), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodDelete, tagURL, nil), http.StatusForbidden, "forbidden")
	renamed := decode[oapi.Tag](t, owner.do(http.MethodPut, tagURL, map[string]any{"name": "Дизайн и UX", "color": "teal"}))
	if renamed.Name != "Дизайн и UX" || renamed.Color != oapi.Teal {
		t.Fatalf("renamed: %+v", renamed)
	}
	wantProblem(t, owner.do(http.MethodPut, tagURL, map[string]any{"name": "интерфейс"}), http.StatusConflict, "tag_exists")
	if rec := owner.do(http.MethodDelete, tagURL, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete tag: %d", rec.Code)
	}
	if got := decode[oapi.Task](t, op.do(http.MethodGet, base, nil)); len(got.TagIds) != 0 {
		t.Fatalf("deleted tag kept: %v", got.TagIds)
	}
	wantProblem(t, owner.do(http.MethodDelete, tagURL, nil), http.StatusNotFound, "not_found")

	// Another workspace sees none of the tags, and its tags work nowhere here.
	_, _ = e.register("stranger@example.test", "stranger")
	foreign, _ := e.login("stranger@example.test")
	if got := decode[oapi.TagList](t, foreign.do(http.MethodGet, "/api/v1/task-tags", nil)); len(got.Items) != 0 {
		t.Fatalf("foreign tags: %+v", got)
	}
	theirs := decode[oapi.Tag](t, foreign.do(http.MethodPost, "/api/v1/task-tags", map[string]any{"name": "Интерфейс"}))
	wantProblem(t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "С чужим тегом", "tag_ids": []uuid.UUID{theirs.Id}}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, op.do(http.MethodPut, base, taskUpdate(kept, func(b map[string]any) { b["tag_ids"] = []uuid.UUID{theirs.Id} })),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, foreign.do(http.MethodPut, "/api/v1/task-tags/"+ui.Id.String(), map[string]any{"name": "Взлом"}), http.StatusNotFound, "not_found")
	wantProblem(t, foreign.do(http.MethodDelete, "/api/v1/task-tags/"+ui.Id.String(), nil), http.StatusNotFound, "not_found")
	if got := decode[oapi.TagList](t, op.do(http.MethodGet, "/api/v1/task-tags", nil)); len(got.Items) != 12 || got.Items[0].Name != "Интерфейс" {
		t.Fatalf("tags after a foreign workspace: %d %+v", len(got.Items), got.Items[0])
	}
}
