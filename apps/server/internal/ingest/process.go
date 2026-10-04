package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/clamav"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// A submitted report becomes a conversation in the job ingest.process
// (docs/02 §11.6): each file is checked by its first bytes and, with
// ClamAV configured, scanned; then conversations stores the report as a
// new conversation with the files attached. A file that fails a check is
// attached as rejected and its object is deleted: operators see that it
// was sent and why it is not there.

// processAttempts: storage or clamd may be away for a while; River backs
// off between attempts (about a day in total).
const processAttempts = 10

// File check results, stored with the message in file order.
const (
	CheckOK           = conversations.FileOK
	CheckTypeMismatch = conversations.FileTypeMismatch
	CheckInfected     = conversations.FileInfected
	// CheckNotScanned: clamd gave no verdict even on the last attempt.
	CheckNotScanned = conversations.FileNotScanned
	// CheckMissing: the object disappeared after complete.
	CheckMissing = conversations.FileMissing
)

// Inbox stores reports as conversations; *conversations.Service
// implements it.
type Inbox interface {
	ReceiveReport(ctx context.Context, workspaceID uuid.UUID, r conversations.AppReport) (conversations.Received, error)
	ExpireAttachments(ctx context.Context, workspaceID uuid.UUID, storageKeys []string) error
}

// Scanner is the optional antivirus; *clamav.Scanner implements it.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) (clamav.Verdict, error)
}

// ProcessArgs turns one submitted report into a conversation. Only ids.
type ProcessArgs struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	ReportID    uuid.UUID `json:"report_id"`
}

// Kind implements river.JobArgs.
func (ProcessArgs) Kind() string { return "ingest.process" }

// InsertOpts implements river.JobArgsWithInsertOpts: one job per report.
func (ProcessArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: processAttempts,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
				rivertype.JobStateRetryable, rivertype.JobStateScheduled, rivertype.JobStateCompleted,
			},
		},
	}
}

type processWorker struct {
	river.WorkerDefaults[ProcessArgs]
	svc *Service
}

// Timeout covers reading and scanning five files of 25 MB.
func (w *processWorker) Timeout(*river.Job[ProcessArgs]) time.Duration { return 10 * time.Minute }

// Work implements river.Worker.
func (w *processWorker) Work(ctx context.Context, job *river.Job[ProcessArgs]) error {
	return w.svc.Process(ctx, job.Args, job.Attempt >= job.MaxAttempts)
}

// Process turns a submitted report into a conversation; anything else
// (processed, deleted) is left alone, so a repeated job does nothing. last
// is the final attempt: a file clamd still cannot scan is then rejected
// instead of failing the report for good.
func (s *Service) Process(ctx context.Context, a ProcessArgs, last bool) error {
	if s.opts.Inbox == nil {
		return errors.New("ingest: no inbox to process reports")
	}
	var (
		report store.AppReport
		files  []store.AppReportFile
	)
	err := s.db.InTenantTx(ctx, a.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if report, err = q.GetReportForProcessing(ctx, store.GetReportForProcessingParams{WorkspaceID: a.WorkspaceID, ID: a.ReportID}); err != nil {
			return err
		}
		files, err = q.ReportFiles(ctx, store.ReportFilesParams{WorkspaceID: a.WorkspaceID, ReportID: a.ReportID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ingest: load report: %w", err)
	}
	if report.Status != StatusSubmitted {
		return nil
	}

	// Storage and clamd stay outside transactions (docs/00 §6.5).
	in := conversations.AppReport{
		ChannelID: report.ChannelID, ReportID: report.ID, Body: report.Message,
		Contact: conversations.ReportContact{
			Verified: report.ContactVerified, ExternalID: deref(report.ContactExternalID),
			Name: deref(report.ContactName), Email: deref(report.ContactEmail),
		},
		Info: conversations.ReportInfo{ContactVerified: report.ContactVerified},
	}
	// Create stored App; a row it cannot read shows without app details.
	_ = json.Unmarshal(report.App, &in.Info.App)
	var rejected []string
	for _, f := range files {
		check, err := s.check(ctx, f, last)
		if err != nil {
			return err
		}
		if check != CheckOK {
			s.log().Warn("report file rejected", "workspace_id", a.WorkspaceID, "report_id", report.ID, "position", f.Position, "check", check)
			rejected = append(rejected, f.StorageKey)
		}
		in.Info.Files = append(in.Info.Files, check)
		in.Files = append(in.Files, conversations.ReceivedFile{
			StorageKey: f.StorageKey, FileName: f.FileName, ContentType: f.ContentType, Size: f.Size, Rejected: check != CheckOK,
		})
	}
	res, err := s.opts.Inbox.ReceiveReport(ctx, a.WorkspaceID, in)
	if errors.Is(err, conversations.ErrInvalid) {
		// Cannot happen for a report that passed Create; retrying will not help.
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	// Rejected files are not kept: an infected file must not stay around.
	for _, key := range rejected {
		if err := s.opts.Files.Delete(ctx, key); err != nil {
			return fmt.Errorf("ingest: delete rejected file: %w", err)
		}
	}
	now := s.now()
	err = s.db.InTenantTx(ctx, a.WorkspaceID, func(tx pgx.Tx) error {
		_, err := store.New(tx).MarkProcessed(ctx, store.MarkProcessedParams{
			WorkspaceID: a.WorkspaceID, ID: report.ID, ConversationID: &res.ConversationID,
			ProcessedAt: &now, FilesExpireAt: ptr(now.AddDate(0, 0, int(report.RetentionDays))),
		})
		return err
	})
	if err != nil {
		return fmt.Errorf("ingest: mark report processed: %w", err)
	}
	return nil
}

// check reads the start of a file to compare it with its declared type and
// streams the whole file to the antivirus. An error means "try again".
func (s *Service) check(ctx context.Context, f store.AppReportFile, last bool) (string, error) {
	rc, _, err := s.opts.Files.Open(ctx, f.StorageKey)
	if errors.Is(err, storage.ErrNotFound) {
		return CheckMissing, nil
	}
	if err != nil {
		return "", fmt.Errorf("ingest: open report file: %w", err)
	}
	defer func() { _ = rc.Close() }()
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("ingest: read report file: %w", err)
	}
	head = head[:n]
	if !matchesType(f.ContentType, head, int64(n) < f.Size) {
		return CheckTypeMismatch, nil
	}
	if s.opts.Scanner == nil {
		return CheckOK, nil
	}
	v, err := s.opts.Scanner.Scan(ctx, io.MultiReader(bytes.NewReader(head), rc))
	switch {
	case errors.Is(err, clamav.ErrScan) && last:
		return CheckNotScanned, nil
	case err != nil:
		return "", fmt.Errorf("ingest: scan report file: %w", err)
	case v.Infected:
		s.log().Warn("report file infected", "report_id", f.ReportID, "position", f.Position, "signature", v.Signature)
		return CheckInfected, nil
	}
	return CheckOK, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
