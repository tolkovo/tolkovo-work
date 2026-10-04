package migrate_test

import (
	"context"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/migrate"
)

// Mandatory migration test (docs/00 §11.3): up on an empty DB, all the way
// down, and up again.
func TestMigrations_UpDownUp(t *testing.T) {
	d := dbtest.New(t) // already migrated up once
	ctx := context.Background()
	m, err := migrate.Open(ctx, d.OwnerURL)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = m.Close() }()

	latest, err := m.Version(ctx)
	if err != nil || latest == 0 {
		t.Fatalf("want a non-zero version after up, got %d (%v)", latest, err)
	}
	if err := m.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if v, _ := m.Version(ctx); v != 0 {
		t.Fatalf("want version 0 after reset, got %d", v)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if v, _ := m.Version(ctx); v != latest {
		t.Fatalf("want version %d after second up, got %d", latest, v)
	}
}
