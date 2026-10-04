// Package migrate applies the embedded goose migrations and then River's own
// migrations into the "river" schema that goose creates.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/migrations"
)

// Migrator runs migrations against one database. It must connect as the
// table owner, not as the application role: default privileges on the river
// schema are granted for objects that this role creates.
type Migrator struct {
	db       *sql.DB
	pool     *pgxpool.Pool
	provider *goose.Provider
	river    *rivermigrate.Migrator[pgx.Tx]
}

// Open connects with the owner DSN.
func Open(ctx context.Context, dsn string) (*Migrator, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init goose: %w", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open pool: %w", err)
	}
	rm, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{
		Schema: jobs.Schema,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		pool.Close()
		_ = db.Close()
		return nil, fmt.Errorf("init river migrations: %w", err)
	}
	return &Migrator{db: db, pool: pool, provider: p, river: rm}, nil
}

// Close closes the connections.
func (m *Migrator) Close() error {
	m.pool.Close()
	return m.db.Close()
}

// Up applies every pending migration, ours first and then River's, and
// returns how many were applied in total.
func (m *Migrator) Up(ctx context.Context) (int, error) {
	res, err := m.provider.Up(ctx)
	if err != nil {
		return len(res), fmt.Errorf("migrate up: %w", err)
	}
	rr, err := m.river.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return len(res), fmt.Errorf("migrate river: %w", err)
	}
	return len(res) + len(rr.Versions), nil
}

// Down rolls back the latest migration.
func (m *Migrator) Down(ctx context.Context) error {
	if _, err := m.provider.Down(ctx); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Reset rolls back every migration. River needs no separate step: the
// migration that creates its schema drops it with all River tables.
func (m *Migrator) Reset(ctx context.Context) error {
	if _, err := m.provider.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("migrate reset: %w", err)
	}
	return nil
}

// Version returns the current schema version (0 when empty).
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	v, err := m.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	return v, nil
}
