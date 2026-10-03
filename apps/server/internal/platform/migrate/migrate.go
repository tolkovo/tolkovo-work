// Package migrate applies the embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/tolkovo/tolkovo/apps/server/migrations"
)

// Migrator runs migrations against one database. It must connect as the
// table owner, not as the application role.
type Migrator struct {
	db       *sql.DB
	provider *goose.Provider
}

// Open connects with the owner DSN.
func Open(dsn string) (*Migrator, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init goose: %w", err)
	}
	return &Migrator{db: db, provider: p}, nil
}

// Close closes the connection.
func (m *Migrator) Close() error { return m.db.Close() }

// Up applies every pending migration and returns how many were applied.
func (m *Migrator) Up(ctx context.Context) (int, error) {
	res, err := m.provider.Up(ctx)
	if err != nil {
		return len(res), fmt.Errorf("migrate up: %w", err)
	}
	return len(res), nil
}

// Down rolls back the latest migration.
func (m *Migrator) Down(ctx context.Context) error {
	if _, err := m.provider.Down(ctx); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Reset rolls back every migration.
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
