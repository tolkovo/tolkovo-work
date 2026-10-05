package widget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/clamav"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/filetype"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/widget/store"
)

// Files of visitors (W-02). The widget registers a file and gets a signed
// upload request; the server chooses the key, the request fixes the size
// and the type. A message lists the files it carries: before it is stored
// each file is checked to be in storage with its size, its first bytes to
// fit its type (an executable cannot pass for a screenshot) and, with
// ClamAV, scanned. A file that fails is deleted and the message refused, so
// the visitor sees at once which file did not go. Files no message took
// are deleted by the worker after UploadTTL.

// Limits of visitors' files.
const (
	MaxFileSize        = 10 << 20
	MaxFilesPerMessage = 5
	// UploadTTL is how long an upload waits for its message.
	UploadTTL = time.Hour
	// cleanupAfter leaves a message that took a file at the last moment
	// time to be stored before the worker looks at the file.
	cleanupAfter = UploadTTL + 15*time.Minute
	cleanupBatch = 200
	maxFileName  = 255
)

// fileTypes are what visitors may send: pictures, PDF, text and the
// archives and Office documents the type check recognises.
var fileTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "text/plain": true, "text/csv": true, "application/json": true,
	"application/zip": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       true,
}

// Uploads per session and per IP: the key is public, and every unsent file
// takes storage for an hour.
var (
	uploadsPerSession = ratelimit.Rule{Limit: 20, Window: time.Hour}
	uploadsPerIP      = ratelimit.Rule{Limit: 60, Window: time.Hour}
)

// ErrScanUnavailable means the antivirus gave no verdict; try again later.
var ErrScanUnavailable = errors.New("widget: files cannot be checked right now")

// Why a file of a message did not go (FileError.Reason).
const (
	// FileNotUploaded: the object is not in storage, or not whole. The
	// widget uploads it again or drops it.
	FileNotUploaded = "not_uploaded"
	// FileTypeMismatch and FileInfected: the file failed its check and was
	// deleted.
	FileTypeMismatch = "type_mismatch"
	FileInfected     = "infected"
)

// FileError means a file of the message did not pass. FileName is what the
// visitor named it; it never goes to the logs.
type FileError struct {
	UploadID uuid.UUID
	FileName string
	Reason   string
}

func (e *FileError) Error() string {
	return fmt.Sprintf("widget: file %s: %s", e.UploadID, e.Reason)
}

// Scanner scans a file for viruses; *clamav.Scanner implements it.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) (clamav.Verdict, error)
}

// UploadInput describes a file the widget is about to upload.
type UploadInput struct {
	FileName    string
	ContentType string
	Size        int64
}

// Upload is a registered file and the request that uploads it.
type Upload struct {
	ID      uuid.UUID
	Request storage.SignedRequest
}

// CreateUpload registers a visitor's file and signs its upload.
func (s *Service) CreateUpload(ctx context.Context, c Caller, token string, in UploadInput) (Upload, error) {
	if s.opts.Files == nil {
		return Upload{}, errors.New("widget: no file storage")
	}
	if err := s.allow(ctx, "widget:ip:"+c.IP, requestsPerIP); err != nil {
		return Upload{}, err
	}
	ct, name, err := in.validate()
	if err != nil {
		return Upload{}, err
	}
	v, err := s.Authenticate(ctx, c, token)
	if err != nil {
		return Upload{}, err
	}
	if err := s.allow(ctx, "widget:uploads:"+v.SessionID.String(), uploadsPerSession); err != nil {
		return Upload{}, err
	}
	if err := s.allow(ctx, "widget:uploads-ip:"+c.IP, uploadsPerIP); err != nil {
		return Upload{}, err
	}
	key, err := storage.NewKey(v.WorkspaceID, storage.KindConversation)
	if err != nil {
		return Upload{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Upload{}, err
	}
	err = s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		_, err := store.New(tx).InsertUpload(ctx, store.InsertUploadParams{
			ID: id, WorkspaceID: v.WorkspaceID, ChannelID: v.ChannelID, SessionID: v.SessionID,
			StorageKey: key, FileName: name, ContentType: ct, Size: in.Size, Now: s.now(),
		})
		return err
	})
	if err != nil {
		return Upload{}, fmt.Errorf("widget: save upload: %w", err)
	}
	// Signing stays outside the transaction like every storage call.
	req, err := s.opts.Files.PresignPut(ctx, key, storage.PresignPutOptions{ContentType: ct, MaxSize: MaxFileSize, Size: in.Size})
	if err != nil {
		return Upload{}, fmt.Errorf("widget: presign upload: %w", err)
	}
	return Upload{ID: id, Request: req}, nil
}

func (in UploadInput) validate() (contentType, name string, err error) {
	ct, _, perr := mime.ParseMediaType(in.ContentType)
	name = strings.TrimSpace(in.FileName)
	switch {
	case name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxFileName:
		return "", "", fmt.Errorf("%w: file_name must be 1 to %d characters", ErrInvalid, maxFileName)
	case perr != nil || !fileTypes[ct]:
		return "", "", fmt.Errorf("%w: this type of file is not accepted", ErrInvalid)
	case in.Size <= 0 || in.Size > MaxFileSize:
		return "", "", fmt.Errorf("%w: size must be between 1 and %d bytes", ErrInvalid, MaxFileSize)
	}
	return ct, storage.SanitizeFilename(name), nil
}

// takeUploads checks the files a message lists and claims them for the
// message's key, in the order given. Storage and the antivirus are called
// between transactions, never inside one; the claim re-checks the rows, so a
// concurrent message with another key cannot take the same file.
func (s *Service) takeUploads(ctx context.Context, v Visitor, key string, ids []uuid.UUID) ([]conversations.ReceivedFile, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if s.opts.Files == nil {
		return nil, errors.New("widget: no file storage")
	}
	since := s.now().Add(-UploadTTL)
	var rows []store.WidgetUpload
	err := s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		var err error
		rows, err = store.New(tx).SendableUploads(ctx, store.SendableUploadsParams{
			WorkspaceID: v.WorkspaceID, SessionID: v.SessionID, Ids: ids, MessageKey: &key, Since: since,
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("widget: load uploads: %w", err)
	}
	if len(rows) != len(ids) {
		return nil, fmt.Errorf("%w: a file is unknown, expired or sent with another message", ErrInvalid)
	}
	byID := make(map[uuid.UUID]store.WidgetUpload, len(rows))
	for _, u := range rows {
		byID[u.ID] = u
	}
	files := make([]conversations.ReceivedFile, 0, len(ids))
	for _, id := range ids {
		u := byID[id]
		if err := s.checkFile(ctx, u); err != nil {
			return nil, err
		}
		files = append(files, conversations.ReceivedFile{
			StorageKey: u.StorageKey, FileName: u.FileName, ContentType: u.ContentType, Size: u.Size,
		})
	}
	err = s.db.InTenantTx(ctx, v.WorkspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).ClaimUploads(ctx, store.ClaimUploadsParams{
			MessageKey: &key, WorkspaceID: v.WorkspaceID, SessionID: v.SessionID, Ids: ids, Since: since,
		})
		if err == nil && n != int64(len(ids)) {
			return fmt.Errorf("%w: a file was sent with another message", ErrInvalid)
		}
		return err
	})
	if errors.Is(err, ErrInvalid) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("widget: claim uploads: %w", err)
	}
	return files, nil
}

// checkFile: the object is whole, its first bytes fit its type and, with
// ClamAV, it is clean. A file that fails is deleted with its row.
func (s *Service) checkFile(ctx context.Context, u store.WidgetUpload) error {
	rc, obj, err := s.opts.Files.Open(ctx, u.StorageKey)
	if errors.Is(err, storage.ErrNotFound) {
		return &FileError{UploadID: u.ID, FileName: u.FileName, Reason: FileNotUploaded}
	}
	if err != nil {
		return fmt.Errorf("widget: open file: %w", err)
	}
	defer func() { _ = rc.Close() }()
	if obj.Size != u.Size {
		return &FileError{UploadID: u.ID, FileName: u.FileName, Reason: FileNotUploaded}
	}
	head := make([]byte, filetype.SniffLen)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("widget: read file: %w", err)
	}
	head = head[:n]
	if !filetype.Matches(u.ContentType, head, int64(n) < u.Size) {
		return s.reject(ctx, u, FileTypeMismatch)
	}
	if s.opts.Scanner == nil {
		return nil
	}
	verdict, err := s.opts.Scanner.Scan(ctx, io.MultiReader(bytes.NewReader(head), rc))
	switch {
	case errors.Is(err, clamav.ErrScan):
		s.log().Warn("widget file not scanned", "workspace_id", u.WorkspaceID, "upload_id", u.ID, "error", err)
		return ErrScanUnavailable
	case err != nil:
		return fmt.Errorf("widget: scan file: %w", err)
	case verdict.Infected:
		s.log().Warn("widget file infected", "workspace_id", u.WorkspaceID, "upload_id", u.ID, "signature", verdict.Signature)
		return s.reject(ctx, u, FileInfected)
	}
	return nil
}

// reject deletes a file that failed its check: an infected file must not
// stay around, and the visitor cannot send it again.
func (s *Service) reject(ctx context.Context, u store.WidgetUpload, reason string) error {
	if err := s.opts.Files.Delete(ctx, u.StorageKey); err != nil {
		return fmt.Errorf("widget: delete rejected file: %w", err)
	}
	err := s.db.InTenantTx(ctx, u.WorkspaceID, func(tx pgx.Tx) error {
		return store.New(tx).DeleteUpload(ctx, store.DeleteUploadParams{WorkspaceID: u.WorkspaceID, ID: u.ID})
	})
	if err != nil {
		return fmt.Errorf("widget: delete rejected upload: %w", err)
	}
	return &FileError{UploadID: u.ID, FileName: u.FileName, Reason: reason}
}

// validUploads checks the list of files a message names.
func validUploads(ids []uuid.UUID) error {
	if len(ids) > MaxFilesPerMessage {
		return fmt.Errorf("%w: at most %d files per message", ErrInvalid, MaxFilesPerMessage)
	}
	seen := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || slices.Contains(seen, id) {
			return fmt.Errorf("%w: attachment_ids must be distinct upload ids", ErrInvalid)
		}
		seen = append(seen, id)
	}
	return nil
}

// CleanupUploads removes uploads older than the cleanup age in every
// workspace: the object of one no message took, then the row. sys connects
// as tolkovo_system and only finds the rows (policy system_see_old); each
// is cleaned up in its tenant transaction. One failure does not stop the
// others: it is logged and retried on the next run.
func (s *Service) CleanupUploads(ctx context.Context, sys store.DBTX) error {
	old, err := store.New(sys).OldUploads(ctx, store.OldUploadsParams{Before: s.now().Add(-cleanupAfter), Lim: cleanupBatch})
	if err != nil {
		return fmt.Errorf("widget: list old uploads: %w", err)
	}
	for _, u := range old {
		if err := s.dropUpload(ctx, u.WorkspaceID, u.ID); err != nil {
			s.log().Warn("widget upload not removed", "workspace_id", u.WorkspaceID, "upload_id", u.ID, "error", err)
		}
	}
	return nil
}

func (s *Service) dropUpload(ctx context.Context, workspaceID, id uuid.UUID) error {
	var u store.WidgetUpload
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		u, err = store.New(tx).GetUpload(ctx, store.GetUploadParams{WorkspaceID: workspaceID, ID: id})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	attached, err := s.opts.Inbox.AttachedKeys(ctx, workspaceID, []string{u.StorageKey})
	if err != nil {
		return err
	}
	if len(attached) == 0 {
		if err := s.opts.Files.Delete(ctx, u.StorageKey); err != nil {
			return fmt.Errorf("delete file: %w", err)
		}
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).DeleteUpload(ctx, store.DeleteUploadParams{WorkspaceID: workspaceID, ID: id})
	})
}

// CleanupUploadsArgs is the periodic job that removes old uploads.
type CleanupUploadsArgs struct{}

// Kind implements river.JobArgs.
func (CleanupUploadsArgs) Kind() string { return "widget.cleanup_uploads" }

type cleanupUploadsWorker struct {
	river.WorkerDefaults[CleanupUploadsArgs]
	svc *Service
	sys *pgxpool.Pool
}

// Work implements river.Worker.
func (w *cleanupUploadsWorker) Work(ctx context.Context, _ *river.Job[CleanupUploadsArgs]) error {
	return w.svc.CleanupUploads(ctx, w.sys)
}

// AddWorkers registers the upload cleanup; sys must connect as
// tolkovo_system. The worker role calls it.
func (s *Service) AddWorkers(workers *river.Workers, sys *pgxpool.Pool) {
	river.AddWorker(workers, &cleanupUploadsWorker{svc: s, sys: sys})
}

// CleanupUploadsPeriodic schedules the cleanup every 15 minutes and once at
// start.
func CleanupUploadsPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupUploadsArgs{}, nil },
		&river.PeriodicJobOpts{ID: "widget.cleanup_uploads", RunOnStart: true},
	)
}
