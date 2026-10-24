package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// A blocked task (ADR-0019, K2): the mark is set and taken off by PUT, kept
// by a client that does not send it, and taken off by closing; the feed
// records both, the reason as the body.
func TestTasks_Blocked(t *testing.T) {
	in := setupInbox(t)
	op := in.op
	task := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Выгрузка в 1С"}))
	base := "/api/v1/tasks/" + task.Id.String()
	put := func(cur oapi.Task, edit func(map[string]any)) oapi.Task {
		t.Helper()
		rec := op.do(http.MethodPut, base, taskUpdate(cur, edit))
		if rec.Code != http.StatusOK {
			t.Fatalf("put: %d %s", rec.Code, rec.Body)
		}
		return decode[oapi.Task](t, rec)
	}
	blocked := put(task, func(m map[string]any) { m["blocked_reason"] = "  Ждём ответа бухгалтерии  " })
	if blocked.BlockedReason == nil || *blocked.BlockedReason != "Ждём ответа бухгалтерии" || blocked.Status != oapi.TaskStatusOpen {
		t.Fatalf("blocked: %+v", blocked)
	}
	// A client that does not know the mark keeps it.
	kept := put(blocked, func(m map[string]any) { m["status"] = "in_progress" })
	if kept.BlockedReason == nil {
		t.Fatalf("an update without the field took the mark off: %+v", kept)
	}
	only := func(query string) []int64 {
		t.Helper()
		return numbers(decode[oapi.TaskPage](t, op.do(http.MethodGet, "/api/v1/tasks"+query, nil)).Items)
	}
	other := decode[oapi.Task](t, op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": "Не заблокирована"}))
	if got := fmt.Sprint(only("?blocked=true")); got != fmt.Sprint([]int64{task.Number}) {
		t.Fatalf("blocked filter: %s", got)
	}
	if got := fmt.Sprint(only("?blocked=false")); got != fmt.Sprint([]int64{other.Number}) {
		t.Fatalf("not blocked filter: %s", got)
	}
	b := decode[oapi.TaskBoard](t, op.do(http.MethodGet, "/api/v1/tasks/board?blocked=true", nil))
	if c := boardColumn(t, b, oapi.TaskStatusInProgress); len(c.Items) != 1 || c.Items[0].BlockedReason == nil {
		t.Fatalf("blocked on the board: %+v", c)
	}
	wantProblem(t, op.do(http.MethodPut, base, taskUpdate(kept, func(m map[string]any) { m["blocked_reason"] = strings.Repeat("я", 501) })),
		http.StatusBadRequest, "invalid_request")

	unblocked := put(kept, func(m map[string]any) { m["blocked_reason"] = "" })
	if unblocked.BlockedReason != nil {
		t.Fatalf("unblocked: %+v", unblocked)
	}
	again := put(unblocked, func(m map[string]any) { m["blocked_reason"] = "Нет доступа к серверу" })
	done := put(again, func(m map[string]any) { m["status"] = "done" })
	if done.BlockedReason != nil || done.ClosedAt == nil {
		t.Fatalf("closing kept the mark: %+v", done)
	}
	// A closed task cannot be blocked: the mark is dropped.
	if stays := put(done, func(m map[string]any) { m["blocked_reason"] = "Поздно" }); stays.BlockedReason != nil {
		t.Fatalf("a closed task got blocked: %+v", stays)
	}

	feed := decode[oapi.TaskActivityList](t, op.do(http.MethodGet, base+"/activity", nil)).Items
	var lines []string
	for _, a := range feed {
		switch a.Kind {
		case oapi.TaskActivityKindBlocked:
			lines = append(lines, "blocked:"+deref(a.Body))
		case oapi.TaskActivityKindUnblocked, oapi.TaskActivityKindStatus:
			lines = append(lines, string(a.Kind))
		}
	}
	want := "blocked:Ждём ответа бухгалтерии status unblocked blocked:Нет доступа к серверу unblocked status"
	if got := strings.Join(lines, " "); got != want {
		t.Fatalf("feed:\n got %s\nwant %s", got, want)
	}
}

// The rules of the columns (ADR-0019, K2): admins set soft limits and ages
// for the workspace and for a queue; the board shows the queue's when it
// shows one queue that has it.
func TestTasks_BoardRules(t *testing.T) {
	in := setupInbox(t)
	e, owner, op := in.e, in.owner, in.op
	dev := decode[oapi.TaskQueue](t, owner.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "Разработка", "member_ids": []string{}}))
	ops := decode[oapi.TaskQueue](t, owner.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "Сисадмины", "member_ids": []string{}}))
	rules := map[string]any{"rules": []map[string]any{
		{"queue_id": nil, "status": "in_progress", "wip_limit": 2, "age_days": 3},
		{"queue_id": nil, "status": "review", "wip_limit": nil, "age_days": 2},
		{"queue_id": dev.Id, "status": "in_progress", "wip_limit": 5, "age_days": nil},
		{"queue_id": ops.Id, "status": "open", "wip_limit": nil, "age_days": nil}, // dropped: says nothing
	}}
	wantProblem(t, op.do(http.MethodPut, "/api/v1/task-board-settings", rules), http.StatusForbidden, "forbidden")
	set := decode[oapi.TaskBoardSettings](t, owner.do(http.MethodPut, "/api/v1/task-board-settings", rules))
	if len(set.Rules) != 3 {
		t.Fatalf("stored rules: %+v", set.Rules)
	}
	if got := decode[oapi.TaskBoardSettings](t, op.do(http.MethodGet, "/api/v1/task-board-settings", nil)); len(got.Rules) != 3 ||
		got.Rules[0].QueueId != nil {
		t.Fatalf("read rules, the workspace's first: %+v", got.Rules)
	}

	rule := func(query string, status oapi.TaskStatus) string {
		t.Helper()
		c := boardColumn(t, decode[oapi.TaskBoard](t, op.do(http.MethodGet, "/api/v1/tasks/board"+query, nil)), status)
		return fmt.Sprintf("%v/%v", deref(c.WipLimit), deref(c.AgeDays))
	}
	for _, tc := range []struct {
		query  string
		status oapi.TaskStatus
		want   string
	}{
		{"", oapi.TaskStatusInProgress, "2/3"},
		{"", oapi.TaskStatusReview, "0/2"},
		{"", oapi.TaskStatusOpen, "0/0"},
		{"?queue=" + dev.Id.String(), oapi.TaskStatusInProgress, "5/0"}, // the queue's rule, whole
		{"?queue=" + dev.Id.String(), oapi.TaskStatusReview, "0/2"},     // none of its own: the workspace's
		{"?queue=" + ops.Id.String(), oapi.TaskStatusInProgress, "2/3"},
		{"?queue=none", oapi.TaskStatusInProgress, "2/3"},
	} {
		if got := rule(tc.query, tc.status); got != tc.want {
			t.Errorf("rule of %s %q: %s, want %s", tc.status, tc.query, got, tc.want)
		}
	}

	for name, bad := range map[string][]map[string]any{
		"done has no rule": {{"queue_id": nil, "status": "done", "wip_limit": 3, "age_days": nil}},
		"limit too big":    {{"queue_id": nil, "status": "open", "wip_limit": 1000, "age_days": nil}},
		"two for a column": {
			{"queue_id": dev.Id, "status": "open", "wip_limit": 3, "age_days": nil},
			{"queue_id": dev.Id, "status": "open", "wip_limit": 4, "age_days": nil},
		},
	} {
		if rec := owner.do(http.MethodPut, "/api/v1/task-board-settings", map[string]any{"rules": bad}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}

	// A queue of another workspace is no queue here; that workspace sees none of ours.
	_, _ = e.register("stranger@example.test", "stranger")
	foreign, _ := e.login("stranger@example.test")
	fq := decode[oapi.TaskQueue](t, foreign.do(http.MethodPost, "/api/v1/task-queues", map[string]any{"name": "Чужая", "member_ids": []string{}}))
	wantProblem(t, owner.do(http.MethodPut, "/api/v1/task-board-settings", map[string]any{"rules": []map[string]any{
		{"queue_id": fq.Id, "status": "open", "wip_limit": 3, "age_days": nil},
	}}), http.StatusBadRequest, "invalid_request")
	if got := decode[oapi.TaskBoardSettings](t, foreign.do(http.MethodGet, "/api/v1/task-board-settings", nil)); len(got.Rules) != 0 {
		t.Fatalf("foreign rules: %+v", got.Rules)
	}

	// Deleting a queue deletes its rules.
	if rec := owner.do(http.MethodDelete, "/api/v1/task-queues/"+dev.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete queue: %d", rec.Code)
	}
	if got := decode[oapi.TaskBoardSettings](t, op.do(http.MethodGet, "/api/v1/task-board-settings", nil)); len(got.Rules) != 2 {
		t.Fatalf("rules after the queue went: %+v", got.Rules)
	}
}

// The flow metrics (ADR-0019, K3): the cycle time of a task done, the week
// it counts in, the statuses at the end of each day and the oldest open
// tasks, under the filters of the board.
func TestTasks_Flow(t *testing.T) {
	in := setupInbox(t)
	e, op := in.e, in.op
	create := func(title string) oapi.Task {
		t.Helper()
		rec := op.do(http.MethodPost, "/api/v1/tasks", map[string]any{"title": title, "priority": "normal"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", title, rec.Code, rec.Body)
		}
		return decode[oapi.Task](t, rec)
	}
	move := func(task oapi.Task, status string) oapi.Task {
		t.Helper()
		rec := op.do(http.MethodPut, "/api/v1/tasks/"+task.Id.String(), taskUpdate(task, func(m map[string]any) { m["status"] = status }))
		if rec.Code != http.StatusOK {
			t.Fatalf("move to %s: %d %s", status, rec.Code, rec.Body)
		}
		return decode[oapi.Task](t, rec)
	}
	flow := func(query string) oapi.TaskFlow {
		t.Helper()
		rec := op.do(http.MethodGet, "/api/v1/tasks/flow"+query, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("flow %q: %d %s", query, rec.Code, rec.Body)
		}
		return decode[oapi.TaskFlow](t, rec)
	}

	fixed := move(create("Вылет при открытии журнала"), "in_progress")
	// It went to work three days ago.
	e.ownerExec(`UPDATE task_activity SET created_at = now() - interval '3 days' WHERE task_id = $1 AND kind = 'status'`, fixed.Id)
	move(fixed, "done")
	waiting := create("Отчёт по сменам")
	e.ownerExec(`UPDATE tasks SET status_changed_at = now() - interval '5 days' WHERE id = $1`, waiting.Id)

	f := flow("")
	if len(f.Cfd) != 30 || len(f.Cycle) != 1 || f.Cycle[0].Number != fixed.Number || f.Cycle[0].Days < 2.9 || f.Cycle[0].Days > 3.1 ||
		f.CycleP50 == nil || *f.CycleP50 != f.Cycle[0].Days || f.Truncated {
		t.Fatalf("flow: %d days, cycle %+v, p50 %v", len(f.Cfd), f.Cycle, f.CycleP50)
	}
	last := f.Cfd[len(f.Cfd)-1]
	if last.Done != 1 || last.Open != 1 || last.InProgress != 0 {
		t.Fatalf("today: %+v", last)
	}
	var done int
	for _, w := range f.Throughput {
		done += w.Done
	}
	if done != 1 {
		t.Fatalf("throughput: %+v", f.Throughput)
	}
	if len(f.Aging) != 1 || f.Aging[0].Number != waiting.Number || f.Aging[0].Days < 4.9 || f.Aging[0].Status != oapi.TaskStatusOpen {
		t.Fatalf("aging: %+v", f.Aging)
	}
	if f := flow("?days=7&assignee=none"); len(f.Cfd) != 7 || len(f.Aging) != 1 {
		t.Fatalf("filtered: %d days, aging %+v", len(f.Cfd), f.Aging)
	}
	wantProblem(t, op.do(http.MethodGet, "/api/v1/tasks/flow?days=5", nil), http.StatusBadRequest, "invalid_request")
}
