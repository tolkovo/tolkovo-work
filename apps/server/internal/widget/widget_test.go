package widget

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
)

func TestToken(t *testing.T) {
	ws := uuid.New()
	token, hash, err := newToken(ws)
	if err != nil || len(hash) != 32 || len(token) != len(sessionPrefix)+64 {
		t.Fatalf("token: %q %d %v", token, len(hash), err)
	}
	if got, ok := parseToken(token); !ok || got != ws {
		t.Fatalf("parse: %v %v", got, ok)
	}
	for _, bad := range []string{"", sessionPrefix, token + "A", "tk_ingest_" + token[len(sessionPrefix):]} {
		if _, ok := parseToken(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

// The worker deletes expired sessions in every workspace and nothing else.
func TestPruneExpiredSessions(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for i, slug := range []string{"alpha", "bravo"} {
		ws, ch := uuid.New(), uuid.New()
		exec("INSERT INTO workspaces (id, name, slug) VALUES ($1, $2, $3)", ws, slug, slug)
		exec("INSERT INTO channels (id, workspace_id, type, name, status, config) VALUES ($1, $2, 'widget', 'Чат', 'active', '{}')", ch, ws)
		for j, expires := range []string{"now() - interval '1 minute'", "now() + interval '1 day'"} {
			hash := make([]byte, 32)
			hash[0], hash[1] = byte(i), byte(j)
			exec("INSERT INTO widget_sessions (id, workspace_id, channel_id, token_hash, created_at, last_seen_at, expires_at) "+
				"VALUES ($1, $2, $3, $4, now(), now(), "+expires+")", uuid.New(), ws, ch, hash)
		}
	}
	n, err := PruneExpiredSessions(ctx, d.OpenSystem(t).Pool()) //nolint:forbidigo // the system role, as in cmd
	if err != nil || n != 2 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	var left int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM widget_sessions WHERE expires_at > now()").Scan(&left); err != nil || left != 2 {
		t.Fatalf("live sessions left: %d %v", left, err)
	}
}
