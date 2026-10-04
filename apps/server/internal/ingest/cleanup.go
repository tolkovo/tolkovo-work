package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/ingest/store"
)

// Cleanup (docs/02 §11.6): a report not completed within UnfinishedTTL is
// deleted with its files; the files of a processed report are deleted when
// its retention ends, and their attachments become expired while the
// message stays. The system role finds the reports across workspaces
// (policy system_cleanup, ids only); each one is cleaned up in its tenant
// transaction.

// UnfinishedTTL is how long an app may take to upload and complete.
const UnfinishedTTL = time.Hour

// cleanupBatch bounds one run; the next run takes the rest.
const cleanupBatch = 200

// CleanupArgs is the periodic cleanup job.
type CleanupArgs struct{}

// Kind implements river.JobArgs.
func (CleanupArgs) Kind() string { return "ingest.cleanup" }

type cleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
	svc *Service
	sys *pgxpool.Pool
}

// Work implements river.Worker.
func (w *cleanupWorker) Work(ctx context.Context, _ *river.Job[CleanupArgs]) error {
	return w.svc.Cleanup(ctx, w.sys)
}

// CleanupPeriodic schedules the cleanup every 10 minutes and once at start.
func CleanupPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(10*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil },
		&river.PeriodicJobOpts{ID: "ingest.cleanup", RunOnStart: true},
	)
}

// AddWorkers registers the processing and the cleanup jobs; sys must
// connect as tolkovo_system. The worker role calls it.
func (s *Service) AddWorkers(workers *river.Workers, sys *pgxpool.Pool) {
	river.AddWorker(workers, &processWorker{svc: s})
	river.AddWorker(workers, &cleanupWorker{svc: s, sys: sys})
}

// Cleanup removes unfinished reports and expired files in every workspace.
// One broken report does not stop the others: it is logged and retried on
// the next run.
func (s *Service) Cleanup(ctx context.Context, sys store.DBTX) error {
	if s.opts.Inbox == nil {
		return errors.New("ingest: no inbox to expire attachments")
	}
	now := s.now()
	q := store.New(sys)
	unfinished, err := q.UnfinishedReports(ctx, store.UnfinishedReportsParams{Before: now.Add(-UnfinishedTTL), Lim: cleanupBatch})
	if err != nil {
		return fmt.Errorf("ingest: list unfinished reports: %w", err)
	}
	for _, r := range unfinished {
		if err := s.dropUnfinished(ctx, r.WorkspaceID, r.ID, now); err != nil {
			s.log().Warn("unfinished report not removed", "workspace_id", r.WorkspaceID, "report_id", r.ID, "error", err)
		}
	}
	expired, err := q.ReportsWithExpiredFiles(ctx, store.ReportsWithExpiredFilesParams{Now: &now, Lim: cleanupBatch})
	if err != nil {
		return fmt.Errorf("ingest: list expired report files: %w", err)
	}
	for _, r := range expired {
		if err := s.expireFiles(ctx, r.WorkspaceID, r.ID, now); err != nil {
			s.log().Warn("report files not expired", "workspace_id", r.WorkspaceID, "report_id", r.ID, "error", err)
		}
	}
	return nil
}

// dropUnfinished: the report first becomes expired, so a late complete
// cannot submit it while its files are deleted; the row goes last, so a
// failed deletion is retried.
func (s *Service) dropUnfinished(ctx context.Context, workspaceID, reportID uuid.UUID, now time.Time) error {
	var keys []string
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.ExpireReport(ctx, store.ExpireReportParams{WorkspaceID: workspaceID, ID: reportID, Before: now.Add(-UnfinishedTTL)}); err != nil {
			return err
		}
		report, err := q.GetReportForProcessing(ctx, store.GetReportForProcessingParams{WorkspaceID: workspaceID, ID: reportID})
		if err != nil || report.Status != StatusExpired {
			return err
		}
		keys, err = s.fileKeys(ctx, q, workspaceID, reportID)
		return err
	})
	if err != nil {
		return err
	}
	if err := s.deleteObjects(ctx, keys); err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		_, err := store.New(tx).DeleteExpiredReport(ctx, store.DeleteExpiredReportParams{WorkspaceID: workspaceID, ID: reportID})
		return err
	})
}

// expireFiles deletes the objects of a processed report, marks its
// attachments expired, then the report.
func (s *Service) expireFiles(ctx context.Context, workspaceID, reportID uuid.UUID, now time.Time) error {
	var keys []string
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		keys, err = s.fileKeys(ctx, store.New(tx), workspaceID, reportID)
		return err
	})
	if err != nil {
		return err
	}
	if err := s.deleteObjects(ctx, keys); err != nil {
		return err
	}
	if err := s.opts.Inbox.ExpireAttachments(ctx, workspaceID, keys); err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		_, err := store.New(tx).MarkFilesExpired(ctx, store.MarkFilesExpiredParams{WorkspaceID: workspaceID, ID: reportID, ExpiredAt: &now})
		return err
	})
}

func (s *Service) fileKeys(ctx context.Context, q *store.Queries, workspaceID, reportID uuid.UUID) ([]string, error) {
	files, err := q.ReportFiles(ctx, store.ReportFilesParams{WorkspaceID: workspaceID, ReportID: reportID})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(files))
	for _, f := range files {
		keys = append(keys, f.StorageKey)
	}
	return keys, nil
}

// deleteObjects removes objects; a missing one is not an error.
func (s *Service) deleteObjects(ctx context.Context, keys []string) error {
	for _, k := range keys {
		if err := s.opts.Files.Delete(ctx, k); err != nil {
			return fmt.Errorf("delete report file: %w", err)
		}
	}
	return nil
}
