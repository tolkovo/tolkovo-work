package api

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// numbers lists the task numbers of items in their order.
func numbers(items []oapi.Task) []int64 {
	out := []int64{}
	for _, it := range items {
		out = append(out, it.Number)
	}
	return out
}

// boardColumn finds the column of status on a board.
func boardColumn(t *testing.T, b oapi.TaskBoard, status oapi.TaskStatus) oapi.TaskBoardColumn {
	t.Helper()
	for _, c := range b.Columns {
		if c.Status == status {
			return c
		}
	}
	t.Fatalf("no column %s in %+v", status, b)
	return oapi.TaskBoardColumn{}
}

// The board (ADR-0019): a column by status in the order of the path; an
// open column puts the most urgent first, then the one standing longest;
// done reaches closed_days back; a column continues by sort=flow; moving a
// card is PUT /tasks/{id} and restarts its time in the column.
func TestTasks_Board(t *testing.T) {
	in := setupInbox(t)
	e, op := in.e, in.op
	create := func(title, priority string) oapi.Task {
		t.Helper()
		rec := op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": title, "priority": priority})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", title, rec.Code, rec.Body)
		}
		return decode[oapi.Task](t, rec)
	}
	board := func(query string) oapi.TaskBoard {
		t.Helper()
		rec := op.do(http.MethodGet, "/api/v1/tasks/board"+query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("board %q: %d %s", query, rec.Code, rec.Body)
		}
		return decode[oapi.TaskBoard](t, rec)
	}

	// №1 normal and №2 normal stand since 3 and 5 days, №3 high, №4 urgent.
	one, two := create("Отчёт по сменам", "normal"), create("Подсказка о доставке", "normal")
	create("Чек после оплаты", "high")
	urgent := create("Вылет при открытии журнала", "urgent")
	e.ownerExec(`UPDATE tasks SET status_changed_at = now() - interval '3 days' WHERE id = $1`, one.Id)
	e.ownerExec(`UPDATE tasks SET status_changed_at = now() - interval '5 days' WHERE id = $1`, two.Id)

	b := board("")
	var statuses []oapi.TaskStatus
	for _, c := range b.Columns {
		statuses = append(statuses, c.Status)
	}
	if got := fmt.Sprint(statuses); got != "[open in_progress review verified done]" {
		t.Fatalf("columns: %s", got)
	}
	open := boardColumn(t, b, oapi.TaskStatusOpen)
	if got := fmt.Sprint(numbers(open.Items)); got != "[4 3 2 1]" || open.Count != 4 || open.NextCursor != nil {
		t.Fatalf("open column: %s, count %d", got, open.Count)
	}

	// Moving a card restarts its time in the column; a conversation linked to
	// it shows on the card.
	conv := in.conversation(t)
	if rec := op.do(http.MethodPost, "/api/v1/tasks/"+two.Id.String()+"/conversations", map[string]any{"conversation_id": conv.Id}); rec.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", rec.Code, rec.Body)
	}
	two = decode[oapi.Task](t, op.do(http.MethodGet, "/api/v1/tasks/"+two.Id.String(), nil))
	moved := decode[oapi.Task](t, op.do(http.MethodPut, "/api/v1/tasks/"+two.Id.String(),
		taskUpdate(two, func(m map[string]any) { m["status"] = "in_progress" })))
	if !moved.StatusChangedAt.After(two.StatusChangedAt) || moved.ConversationCount != 1 {
		t.Fatalf("moved: %+v, was %+v", moved, two)
	}
	// Another change keeps the time.
	renamed := decode[oapi.Task](t, op.do(http.MethodPut, "/api/v1/tasks/"+two.Id.String(),
		taskUpdate(moved, func(m map[string]any) { m["title"] = "Подсказка о доставке в выходные" })))
	if !renamed.StatusChangedAt.Equal(moved.StatusChangedAt) {
		t.Fatalf("renamed moved the time: %v, was %v", renamed.StatusChangedAt, moved.StatusChangedAt)
	}
	b = board("")
	work := boardColumn(t, b, oapi.TaskStatusInProgress)
	if len(work.Items) != 1 || work.Items[0].Number != 2 || work.Items[0].ConversationCount != 1 {
		t.Fatalf("in progress column: %+v", work)
	}
	if got := fmt.Sprint(numbers(boardColumn(t, b, oapi.TaskStatusOpen).Items)); got != "[4 3 1]" {
		t.Fatalf("open after the move: %s", got)
	}
	// The filters are those of the list.
	if got := fmt.Sprint(numbers(boardColumn(t, board("?q="+url.QueryEscape("журнал")), oapi.TaskStatusOpen).Items)); got != "[4]" {
		t.Fatalf("search: %s", got)
	}
	if got := boardColumn(t, board("?assignee=me"), oapi.TaskStatusInProgress); len(got.Items) != 1 {
		t.Fatalf("mine: %+v", got)
	}

	// Done reaches closed_days back; canceled only when asked.
	done := decode[oapi.Task](t, op.do(http.MethodPut, "/api/v1/tasks/"+urgent.Id.String(),
		taskUpdate(urgent, func(m map[string]any) { m["status"] = "done" })))
	old := create("Старая задача", "low")
	old = decode[oapi.Task](t, op.do(http.MethodPut, "/api/v1/tasks/"+old.Id.String(),
		taskUpdate(old, func(m map[string]any) { m["status"] = "done" })))
	e.ownerExec(`UPDATE tasks SET status_changed_at = now() - interval '20 days' WHERE id = $1`, old.Id)
	canceled := create("Не будем делать", "normal")
	op.do(http.MethodPut, "/api/v1/tasks/"+canceled.Id.String(), taskUpdate(canceled, func(m map[string]any) { m["status"] = "canceled" }))
	if got := fmt.Sprint(numbers(boardColumn(t, board(""), oapi.TaskStatusDone).Items)); got != fmt.Sprint([]int64{done.Number}) {
		t.Fatalf("done of 14 days: %s", got)
	}
	b = board("?closed_days=30&canceled=true")
	if got := fmt.Sprint(numbers(boardColumn(t, b, oapi.TaskStatusDone).Items)); got != fmt.Sprint([]int64{done.Number, old.Number}) {
		t.Fatalf("done of 30 days: %s", got)
	}
	if got := boardColumn(t, b, oapi.TaskStatusCanceled); len(got.Items) != 1 || got.Items[0].Number != canceled.Number {
		t.Fatalf("canceled: %+v", got)
	}

	// A column shows 50 tasks and continues by sort=flow in the same order.
	e.ownerExec(`INSERT INTO tasks (id, workspace_id, number, title, status, priority, created_at, updated_at, status_changed_at)
		SELECT gen_random_uuid(), workspace_id, 100 + n, 'Задача ' || n, 'review', 'normal', now(), now(), now() - n * interval '1 minute'
		FROM tasks, generate_series(1, 53) n WHERE id = $1`, one.Id)
	review := boardColumn(t, board(""), oapi.TaskStatusReview)
	if review.Count != 53 || len(review.Items) != 50 || review.Items[0].Number != 153 || review.NextCursor == nil {
		t.Fatalf("review column: count %d, %d items, first %d", review.Count, len(review.Items), review.Items[0].Number)
	}
	rest := decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks?status=review&sort=flow&cursor="+url.QueryEscape(*review.NextCursor), nil))
	if got := fmt.Sprint(numbers(rest.Items)); got != "[103 102 101]" || rest.NextCursor != nil {
		t.Fatalf("rest of review: %s", got)
	}
	page := decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks?status=review&sort=flow&limit=2", nil))
	if got := fmt.Sprint(numbers(page.Items)); got != "[153 152]" || page.NextCursor == nil {
		t.Fatalf("first flow page: %s", got)
	}
	page = decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks?status=done&sort=flow&closed_days=14", nil))
	if got := fmt.Sprint(numbers(page.Items)); got != fmt.Sprint([]int64{done.Number}) {
		t.Fatalf("done by flow: %s", got)
	}

	for _, query := range []string{
		"/api/v1/tasks?sort=flow",
		"/api/v1/tasks?sort=flow&status=open&state=open",
		"/api/v1/tasks?sort=flow&status=open&cursor=12",
		"/api/v1/tasks?status=open&cursor=f2.1.1",
		"/api/v1/tasks?sort=flow&status=open&cursor=f9.1.1",
		"/api/v1/tasks/board?closed_days=91",
		"/api/v1/tasks/board?queue=nope",
	} {
		wantProblem(t, op.do(http.MethodGet, query, nil), http.StatusBadRequest, "invalid_request")
	}

	// Another workspace sees an empty board.
	_, _ = e.register("stranger@example.test", "stranger")
	foreign, _ := e.login("stranger@example.test")
	fb := decode[oapi.TaskBoard](t, foreign.do(http.MethodGet, "/api/v1/tasks/board?closed_days=90&canceled=true", nil))
	for _, c := range fb.Columns {
		if c.Count != 0 || len(c.Items) != 0 {
			t.Fatalf("foreign column %s: %+v", c.Status, c)
		}
	}
}
