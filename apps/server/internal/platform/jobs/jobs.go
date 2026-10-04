// Package jobs configures River, the PostgreSQL-backed job queue
// (docs/adr/0004-river-background-jobs.md). A job is inserted in the same
// transaction as the business change that needs it, so "saved but never
// sent" cannot happen.
//
// Workers that touch tenant data take workspace_id from the job arguments and
// open db.InTenantTx: the queue itself is not tenant-scoped.
package jobs

import (
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Schema holds River's tables (created by migration 00002 and rivermigrate).
const Schema = "river"

// Queues with separate concurrency limits, so one workspace generating AI
// replies cannot starve outgoing messages (ADR-0004).
const (
	QueueDefault  = river.QueueDefault
	QueueOutbound = "outbound"
	QueueAI       = "ai"
)

// Client is the River client bound to pgx transactions.
type Client = river.Client[pgx.Tx]

// Options configure New.
type Options struct {
	Log *slog.Logger
	// Workers run jobs. Nil gives an insert-only client (role=api).
	Workers *river.Workers
	// Periodic jobs are scheduled by the elected leader only, so running
	// several worker replicas does not multiply them.
	Periodic []*river.PeriodicJob
	// StopTimeout bounds how long running jobs may finish after Stop before
	// their contexts are cancelled.
	StopTimeout time.Duration
}

// New builds a River client on the application pool. The pool's role needs
// DML rights on the river schema, which migration 00002 grants to tolkovo_app.
func New(pool *pgxpool.Pool, opts Options) (*Client, error) {
	if opts.Log == nil {
		return nil, errors.New("jobs: logger is required")
	}
	cfg := &river.Config{
		Schema: Schema,
		Logger: opts.Log.With("component", "river"),
		// REINDEX needs table ownership, which the application role does not
		// have by design. Index maintenance is an operator task (docs/06).
		ReindexerSchedule: river.NeverSchedule(),
		SoftStopTimeout:   opts.StopTimeout,
	}
	if opts.Workers != nil {
		cfg.Workers = opts.Workers
		cfg.PeriodicJobs = opts.Periodic
		cfg.Queues = map[string]river.QueueConfig{
			QueueDefault:  {MaxWorkers: 20},
			QueueOutbound: {MaxWorkers: 20},
			QueueAI:       {MaxWorkers: 10},
		}
	} else if len(opts.Periodic) > 0 {
		return nil, errors.New("jobs: periodic jobs need workers")
	}
	return river.NewClient(riverpgxv5.New(pool), cfg)
}
