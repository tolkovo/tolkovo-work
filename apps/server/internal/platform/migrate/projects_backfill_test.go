package migrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/migrate"
)

// 00047 and 00048 over existing tasks: the default project takes them, its
// queues and the counter; down gives the counter back (ADR-0022).
func TestMigrations_ProjectsBackfill(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	m, err := migrate.Open(ctx, d.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	for v, _ := m.Version(ctx); v > 46; v, _ = m.Version(ctx) {
		if err := m.Down(ctx); err != nil {
			t.Fatalf("down: %v", err)
		}
	}
	c, err := pgx.Connect(ctx, d.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	exec := func(sql string) {
		t.Helper()
		if _, err := c.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO workspaces (id, slug, name) VALUES ('00000000-0000-7000-8000-000000000001', 'one', 'One'), ('00000000-0000-7000-8000-000000000002', 'two', 'Two')`)
	exec(`INSERT INTO task_counters VALUES ('00000000-0000-7000-8000-000000000001', 7)`)
	exec(`INSERT INTO task_queues (id, workspace_id, name, created_at) VALUES ('00000000-0000-7000-8000-0000000000a1', '00000000-0000-7000-8000-000000000001', 'Dev', now())`)
	exec(`INSERT INTO tasks (id, workspace_id, number, queue_id, title, status, created_at, updated_at, status_changed_at)
		VALUES ('00000000-0000-7000-8000-0000000000b1', '00000000-0000-7000-8000-000000000001', 7, '00000000-0000-7000-8000-0000000000a1', 'x', 'open', now(), now(), now())`)
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	var key string
	var last int64
	if err := c.QueryRow(ctx, `SELECT count(*) FROM task_projects`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("projects: %d %v", n, err)
	}
	if err := c.QueryRow(ctx, `SELECT p.key, p.last_number FROM tasks t JOIN task_projects p ON p.id = t.project_id
		JOIN task_queues q ON q.project_id = p.id WHERE t.number = 7`).Scan(&key, &last); err != nil || key != "TSK" || last != 7 {
		t.Fatalf("task: %s %d %v", key, last, err)
	}
	if err := c.QueryRow(ctx, `SELECT last_number FROM task_projects WHERE workspace_id = '00000000-0000-7000-8000-000000000002'`).Scan(&last); err != nil || last != 0 {
		t.Fatalf("empty workspace: %d %v", last, err)
	}
	for v, _ := m.Version(ctx); v > 46; v, _ = m.Version(ctx) {
		if err := m.Down(ctx); err != nil {
			t.Fatalf("down again: %v", err)
		}
	}
	if err := c.QueryRow(ctx, `SELECT last_number FROM task_counters WHERE workspace_id = '00000000-0000-7000-8000-000000000001'`).Scan(&last); err != nil || last != 7 {
		t.Fatalf("counter back: %d %v", last, err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
