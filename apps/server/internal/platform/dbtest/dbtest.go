// Package dbtest gives integration tests a fresh, migrated PostgreSQL
// database per test and a connection as the application role, so RLS is
// really enforced (superusers bypass it).
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/migrate"
)

// EnvAdminURL names the variable with a DSN of a role that can create
// databases and roles. Without it integration tests are skipped locally and
// fail in CI.
const EnvAdminURL = "TOLKOVO_TEST_DATABASE_URL"

// Credentials of a role that exists only in the local or CI test cluster.
const (
	appRole     = "tolkovo_app_test"
	appPassword = "tolkovo_app_test" //nolint:gosec // G101: test-only role in a throwaway cluster
)

// Database is a throwaway database dropped at test cleanup.
type Database struct {
	OwnerURL string // migrations and fixtures; bypasses RLS
	AppURL   string // application role; subject to RLS
}

// New creates and migrates a database for the test.
func New(t testing.TB) Database {
	t.Helper()
	admin := os.Getenv(EnvAdminURL)
	if admin == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s must be set in CI", EnvAdminURL)
		}
		t.Skipf("integration test: set %s to run it", EnvAdminURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "tolkovo_t_" + randomHex(t, 6)
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	exec(ctx, t, conn, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	t.Cleanup(func() { dropDatabase(t, admin, name) })

	d := Database{OwnerURL: withDatabase(t, admin, name, nil)}
	m, err := migrate.Open(d.OwnerURL)
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	defer func() { _ = m.Close() }()
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The role is created after migrations because they create tolkovo_app.
	exec(ctx, t, conn, `DO $$ BEGIN
		CREATE ROLE `+appRole+` LOGIN PASSWORD '`+appPassword+`' IN ROLE tolkovo_app;
	EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL; END $$`)
	d.AppURL = withDatabase(t, admin, name, url.UserPassword(appRole, appPassword))
	return d
}

// OpenApp opens the application pool and closes it at cleanup.
func (d Database) OpenApp(t testing.TB) *db.DB {
	t.Helper()
	pool, err := db.Open(context.Background(), d.AppURL)
	if err != nil {
		t.Fatalf("open app db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func exec(ctx context.Context, t testing.TB, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func dropDatabase(t testing.TB, admin, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Errorf("connect admin for cleanup: %v", err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Errorf("drop test database %s: %v", name, err)
	}
}

func withDatabase(t testing.TB, dsn, name string, user *url.Userinfo) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("%s must be a URL: %v", EnvAdminURL, err)
	}
	u.Path = "/" + name
	if user != nil {
		u.User = user
	}
	return u.String()
}

func randomHex(t testing.TB, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b)
}
