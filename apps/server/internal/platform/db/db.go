// Package db owns the PostgreSQL pool. Business code never touches the pool
// directly: tenant data goes through InTenantTx, global tables (users)
// through InGlobalTx.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoWorkspace is returned when InTenantTx gets the zero workspace ID.
var ErrNoWorkspace = errors.New("db: workspace id is required")

// DB wraps the connection pool of the application role.
type DB struct {
	pool *pgxpool.Pool
}

// Open connects and pings the database.
func Open(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Close releases all connections.
func (d *DB) Close() { d.pool.Close() }

// Ping checks that the database answers. Used by /readyz.
func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// InTenantTx runs fn in a transaction scoped to one workspace. RLS policies
// read the workspace from app.workspace_id, which is set with is_local=true
// (SET LOCAL semantics), so it never outlives the transaction and is safe
// with PgBouncer in transaction mode. Queries must still filter by
// workspace_id explicitly: RLS is the second line of defence.
func (d *DB) InTenantTx(ctx context.Context, workspaceID uuid.UUID, fn func(pgx.Tx) error) error {
	if workspaceID == uuid.Nil {
		return ErrNoWorkspace
	}
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.workspace_id', $1, true)", workspaceID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(tx)
	})
}

// InGlobalTx runs fn in a transaction without a workspace. Only global tables
// (users) are reachable: RLS hides every tenant row.
func (d *DB) InGlobalTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.pool, fn)
}
