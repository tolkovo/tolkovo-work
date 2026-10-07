package tasks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/clamav"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/filetype"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Files of tasks (migration 00033): screenshots, logs and documents in a
// task's description and in its comments. A member registers a file and
// uploads it with a signed request; creating a task, a comment, or adding
// files to a description takes it. Only then is the file checked: whole,
// its first bytes fit its type and, with ClamAV, clean.

// Limits of files, as for conversations (docs/00 §9.4).
const (
	MaxFileSize = 25 << 20
	// MaxFilesAtOnce a task, a comment or one addition takes.
	MaxFilesAtOnce = 10
	// MaxTaskFiles a task holds in all.
	MaxTaskFiles = 100
	// uploadTTL is how long the signed upload lives.
	uploadTTL = time.Hour
	// filesCleanupAfter leaves an upload that a task took at the last
	// moment alone.
	filesCleanupAfter = uploadTTL + 15*time.Minute
	filesCleanupBatch = 200
)

// fileTypes are the types a file of a task may have; downloads always come
// as attachments, so even an allowed type never opens as a page.
var fileTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "text/plain": true, "text/csv": true,
	"application/json": true, "application/zip": true, "application/gzip": true,
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.ms-excel": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
}

// Errors of files.
var (
	// ErrScanUnavailable: ClamAV is configured but did not answer; the file
	// stays and can be sent again.
	ErrScanUnavailable = errors.New("tasks: the antivirus is unavailable")
	// ErrNoStorage: the server has no file storage.
	ErrNoStorage = errors.New("tasks: no file storage")
)

// Reasons a file is refused.
const (
	FileNotUploaded  = "not_uploaded"
	FileTypeMismatch = "type_mismatch"
	FileInfected     = "infected"
)

// FileError is a file that failed its check. A file of the wrong type or
// with a virus is deleted; one not uploaded yet can be uploaded and sent
// again.
type FileError struct {
	FileID   uuid.UUID
	FileName string
	Reason   string
}

func (e *FileError) Error() string { return fmt.Sprintf("tasks: file %s: %s", e.FileID, e.Reason) }

// Storage is the part of the file storage the module needs; storage.FS and
// storage.S3 implement it.
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error)
	Open(ctx context.Context, key string) (io.ReadCloser, storage.Object, error)
	Delete(ctx context.Context, key string) error
	PresignPut(ctx context.Context, key string, opts storage.PresignPutOptions) (storage.SignedRequest, error)
	PresignGet(ctx context.Context, key string, opts storage.PresignGetOptions) (storage.SignedRequest, error)
}

// Scanner scans a file for viruses; *clamav.Scanner implements it.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) (clamav.Verdict, error)
}

// File is a file of a task, or one uploaded and not taken yet. The storage
// key stays inside: clients get short-lived signed links.
type File struct {
	ID          uuid.UUID
	TaskID      *uuid.UUID
	ActivityID  *uuid.UUID
	FileName    string
	ContentType string
	Size        int64
	UploadedBy  *uuid.UUID
	CreatedAt   time.Time
}

// FileInput describes a file about to be uploaded.
type FileInput struct {
	FileName    string
	ContentType string
	Size        int64
}

// FileUpload is a registered file and the signed request that uploads it.
type FileUpload struct {
	File    File
	Request storage.SignedRequest
}

func (in FileInput) validate() (string, string, error) {
	ct, _, err := mime.ParseMediaType(in.ContentType)
	name := storage.SanitizeFilename(in.FileName)
	switch {
	case in.FileName == "" || len(in.FileName) > 1024 || name == "":
		return "", "", fmt.Errorf("%w: file_name must be 1-1024 bytes", ErrInvalid)
	case err != nil || !fileTypes[ct]:
		return "", "", fmt.Errorf("%w: this type of file is not allowed", ErrInvalid)
	case in.Size <= 0 || in.Size > MaxFileSize:
		return "", "", fmt.Errorf("%w: a file must be 1 byte to %d MB", ErrInvalid, MaxFileSize>>20)
	}
	if len([]rune(name)) > 255 {
		name = string([]rune(name)[:255])
	}
	return ct, name, nil
}

func (s *Service) storage() (Storage, error) {
	if s.opts.Files == nil {
		return nil, ErrNoStorage
	}
	return s.opts.Files, nil
}

// RegisterFile records a file the actor is about to upload and signs its
// upload. The file belongs to no task until one takes it.
func (s *Service) RegisterFile(ctx context.Context, workspaceID, actorID uuid.UUID, in FileInput) (FileUpload, error) {
	files, err := s.storage()
	if err != nil {
		return FileUpload{}, err
	}
	row, err := s.insertFile(ctx, workspaceID, actorID, in)
	if err != nil {
		return FileUpload{}, err
	}
	// Signing is local for both drivers, but stays outside the transaction
	// like every storage call.
	req, err := files.PresignPut(ctx, row.StorageKey, storage.PresignPutOptions{
		ContentType: row.ContentType, MaxSize: MaxFileSize, Size: row.Size, TTL: uploadTTL,
	})
	if err != nil {
		return FileUpload{}, fmt.Errorf("presign upload: %w", err)
	}
	return FileUpload{File: fileFrom(row), Request: req}, nil
}

// PutFile stores a file the server received whole (from an AI agent over
// MCP) and returns it registered, ready for a task to take.
func (s *Service) PutFile(ctx context.Context, workspaceID, actorID uuid.UUID, in FileInput, data []byte) (File, error) {
	files, err := s.storage()
	if err != nil {
		return File{}, err
	}
	in.Size = int64(len(data))
	row, err := s.insertFile(ctx, workspaceID, actorID, in)
	if err != nil {
		return File{}, err
	}
	if _, err := files.Put(ctx, row.StorageKey, bytes.NewReader(data), storage.PutOptions{
		ContentType: row.ContentType, MaxSize: MaxFileSize, Size: row.Size,
	}); err != nil {
		return File{}, fmt.Errorf("store file: %w", err)
	}
	return fileFrom(row), nil
}

func (s *Service) insertFile(ctx context.Context, workspaceID, actorID uuid.UUID, in FileInput) (store.TaskFile, error) {
	ct, name, err := in.validate()
	if err != nil {
		return store.TaskFile{}, err
	}
	key, err := storage.NewKey(workspaceID, storage.KindTasks)
	if err != nil {
		return store.TaskFile{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return store.TaskFile{}, err
	}
	var row store.TaskFile
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		if err := s.enabled(ctx, store.New(tx), workspaceID); err != nil {
			return err
		}
		row, err = store.New(tx).InsertTaskFile(ctx, store.InsertTaskFileParams{
			ID: id, WorkspaceID: workspaceID, StorageKey: key, FileName: name, ContentType: ct, Size: in.Size,
			UploadedBy: &actorID, CreatedAt: s.now(),
		})
		return err
	})
	if err != nil {
		return store.TaskFile{}, dbError(err, "register file")
	}
	return row, nil
}

// checkFiles makes sure the actor's files are uploaded, not taken and pass
// the check, before the transaction that takes them. The transaction takes
// them again only if they are still loose, so two requests cannot both
// take one.
func (s *Service) checkFiles(ctx context.Context, workspaceID, actorID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if err := validFileIDs(ids); err != nil {
		return err
	}
	if _, err := s.storage(); err != nil {
		return err
	}
	var rows []store.TaskFile
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		rows, err = store.New(tx).LooseTaskFiles(ctx, store.LooseTaskFilesParams{WorkspaceID: workspaceID, Ids: ids, UploadedBy: &actorID})
		return err
	})
	if err != nil {
		return dbError(err, "load files")
	}
	if len(rows) != len(ids) {
		return fmt.Errorf("%w: a file is unknown, someone else's or already in a task", ErrInvalid)
	}
	for _, f := range rows {
		if err := s.checkFile(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

// attachFiles gives the checked files their task inside the caller's
// transaction, within MaxTaskFiles.
func (s *Service) attachFiles(ctx context.Context, q *store.Queries, row store.Task, actorID uuid.UUID, activityID *uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	n, err := q.CountTaskFiles(ctx, store.CountTaskFilesParams{WorkspaceID: row.WorkspaceID, TaskID: &row.ID})
	if err != nil {
		return err
	}
	if n+int64(len(ids)) > MaxTaskFiles {
		return fmt.Errorf("%w: a task holds at most %d files", ErrInvalid, MaxTaskFiles)
	}
	now := s.now()
	got, err := q.AttachTaskFiles(ctx, store.AttachTaskFilesParams{
		TaskID: &row.ID, ActivityID: activityID, AttachedAt: &now, WorkspaceID: row.WorkspaceID, Ids: ids, UploadedBy: &actorID,
	})
	if err != nil {
		return err
	}
	if got != int64(len(ids)) {
		return fmt.Errorf("%w: a file was taken by another task meanwhile", ErrInvalid)
	}
	return nil
}

func validFileIDs(ids []uuid.UUID) error {
	if len(ids) > MaxFilesAtOnce {
		return fmt.Errorf("%w: at most %d files at once", ErrInvalid, MaxFilesAtOnce)
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return fmt.Errorf("%w: file_ids must be distinct file ids", ErrInvalid)
		}
		seen[id] = true
	}
	return nil
}

// checkFile: the object is whole, its first bytes fit its type and, with
// ClamAV, it is clean. A file of the wrong type or with a virus is deleted.
func (s *Service) checkFile(ctx context.Context, f store.TaskFile) error {
	rc, obj, err := s.opts.Files.Open(ctx, f.StorageKey)
	if errors.Is(err, storage.ErrNotFound) {
		return &FileError{FileID: f.ID, FileName: f.FileName, Reason: FileNotUploaded}
	}
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = rc.Close() }()
	if obj.Size != f.Size {
		return &FileError{FileID: f.ID, FileName: f.FileName, Reason: FileNotUploaded}
	}
	head := make([]byte, filetype.SniffLen)
	n, err := io.ReadFull(rc, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read file: %w", err)
	}
	head = head[:n]
	if !filetype.Matches(f.ContentType, head, int64(n) < f.Size) {
		return s.rejectFile(ctx, f, FileTypeMismatch)
	}
	if s.opts.Scanner == nil {
		return nil
	}
	verdict, err := s.opts.Scanner.Scan(ctx, io.MultiReader(bytes.NewReader(head), rc))
	switch {
	case errors.Is(err, clamav.ErrScan):
		s.log().Warn("task file not scanned", "workspace_id", f.WorkspaceID, "file_id", f.ID, "error", err)
		return ErrScanUnavailable
	case err != nil:
		return fmt.Errorf("scan file: %w", err)
	case verdict.Infected:
		s.log().Warn("task file infected", "workspace_id", f.WorkspaceID, "file_id", f.ID, "signature", verdict.Signature)
		return s.rejectFile(ctx, f, FileInfected)
	}
	return nil
}

func (s *Service) rejectFile(ctx context.Context, f store.TaskFile, reason string) error {
	if err := s.dropFile(ctx, f.WorkspaceID, f.ID); err != nil {
		return err
	}
	return &FileError{FileID: f.ID, FileName: f.FileName, Reason: reason}
}

// AddFiles puts the actor's uploaded files into a task's description.
func (s *Service) AddFiles(ctx context.Context, workspaceID, actorID, taskID uuid.UUID, ids []uuid.UUID) (Task, error) {
	if len(ids) == 0 {
		return Task{}, fmt.Errorf("%w: give one file or more", ErrInvalid)
	}
	if err := s.checkFiles(ctx, workspaceID, actorID, ids); err != nil {
		return Task{}, err
	}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		if err := s.attachFiles(ctx, q, row, actorID, nil, ids); err != nil {
			return err
		}
		if _, err := s.addActivity(ctx, q, row, &actorID, "files", nil, map[string]any{"added": len(ids)}); err != nil {
			return err
		}
		return changed(ctx, tx, row, &actorID, false)
	})
	if err != nil {
		return Task{}, dbError(err, "add files")
	}
	return s.Get(ctx, workspaceID, taskID)
}

// RemoveFile takes a file out of a task's description; files of comments
// stay, as comments do. The cleanup deletes the object.
func (s *Service) RemoveFile(ctx context.Context, workspaceID, actorID, taskID, fileID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		n, err := q.DetachTaskFile(ctx, store.DetachTaskFileParams{WorkspaceID: workspaceID, ID: fileID, TaskID: &taskID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if _, err := s.addActivity(ctx, q, row, &actorID, "files", nil, map[string]any{"removed": 1}); err != nil {
			return err
		}
		return changed(ctx, tx, row, &actorID, false)
	})
	if err != nil {
		return dbError(err, "remove file")
	}
	return nil
}

// FileLink signs a download of a file of a live task.
func (s *Service) FileLink(ctx context.Context, workspaceID, fileID uuid.UUID) (File, storage.SignedRequest, error) {
	files, err := s.storage()
	if err != nil {
		return File{}, storage.SignedRequest{}, err
	}
	row, err := s.attachedFile(ctx, workspaceID, fileID)
	if err != nil {
		return File{}, storage.SignedRequest{}, err
	}
	req, err := files.PresignGet(ctx, row.StorageKey, storage.PresignGetOptions{Filename: row.FileName, ContentType: row.ContentType})
	if err != nil {
		return File{}, storage.SignedRequest{}, fmt.Errorf("presign download: %w", err)
	}
	return fileFrom(row), req, nil
}

// ReadFile returns the content of a file of a live task, up to limit
// bytes; a bigger file is ErrInvalid.
func (s *Service) ReadFile(ctx context.Context, workspaceID, fileID uuid.UUID, limit int64) (File, []byte, error) {
	files, err := s.storage()
	if err != nil {
		return File{}, nil, err
	}
	row, err := s.attachedFile(ctx, workspaceID, fileID)
	if err != nil {
		return File{}, nil, err
	}
	if row.Size > limit {
		return fileFrom(row), nil, fmt.Errorf("%w: the file is larger than %d KB", ErrInvalid, limit>>10)
	}
	rc, _, err := files.Open(ctx, row.StorageKey)
	if err != nil {
		return File{}, nil, fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return File{}, nil, fmt.Errorf("read file: %w", err)
	}
	return fileFrom(row), data, nil
}

func (s *Service) attachedFile(ctx context.Context, workspaceID, fileID uuid.UUID) (store.TaskFile, error) {
	var row store.TaskFile
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		row, err = store.New(tx).GetTaskFile(ctx, store.GetTaskFileParams{WorkspaceID: workspaceID, ID: fileID})
		if err == nil && row.TaskID == nil {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return store.TaskFile{}, dbError(err, "get file")
	}
	return row, nil
}

// taskFiles returns the files of a task: of its description, and of its
// comments by comment.
func taskFiles(ctx context.Context, q *store.Queries, workspaceID, taskID uuid.UUID) ([]File, map[uuid.UUID][]File, error) {
	rows, err := q.TaskFiles(ctx, store.TaskFilesParams{WorkspaceID: workspaceID, TaskID: &taskID})
	if err != nil {
		return nil, nil, err
	}
	desc := []File{}
	byComment := map[uuid.UUID][]File{}
	for _, r := range rows {
		if r.ActivityID == nil {
			desc = append(desc, fileFrom(r))
		} else {
			byComment[*r.ActivityID] = append(byComment[*r.ActivityID], fileFrom(r))
		}
	}
	return desc, byComment, nil
}

func fileFrom(r store.TaskFile) File {
	return File{
		ID: r.ID, TaskID: r.TaskID, ActivityID: r.ActivityID, FileName: r.FileName, ContentType: r.ContentType,
		Size: r.Size, UploadedBy: r.UploadedBy, CreatedAt: r.CreatedAt,
	}
}

// CleanupFiles deletes files without a task in every workspace: those a
// task gave up (removed, task deleted) and uploads nobody took. sys
// connects as tolkovo_system and only finds the rows (policy
// system_see_loose); each is deleted in its tenant transaction. A failure
// is logged and retried on the next run.
func (s *Service) CleanupFiles(ctx context.Context, sys store.DBTX) error {
	if s.opts.Files == nil {
		return nil
	}
	rows, err := store.New(sys).LooseTaskFilesToClean(ctx, store.LooseTaskFilesToCleanParams{
		Before: s.now().Add(-filesCleanupAfter), Lim: filesCleanupBatch,
	})
	if err != nil {
		return fmt.Errorf("tasks: list loose files: %w", err)
	}
	for _, r := range rows {
		if err := s.dropFile(ctx, r.WorkspaceID, r.ID); err != nil {
			s.log().Warn("task file not removed", "workspace_id", r.WorkspaceID, "file_id", r.ID, "error", err)
		}
	}
	return nil
}

// dropFile deletes a file without a task: the object, then the row.
func (s *Service) dropFile(ctx context.Context, workspaceID, id uuid.UUID) error {
	var row store.TaskFile
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		row, err = store.New(tx).GetTaskFile(ctx, store.GetTaskFileParams{WorkspaceID: workspaceID, ID: id})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.TaskID != nil) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.opts.Files.Delete(ctx, row.StorageKey); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).DeleteTaskFile(ctx, store.DeleteTaskFileParams{WorkspaceID: workspaceID, ID: id})
	})
}

// CleanupFilesArgs is the periodic job that deletes files without a task.
type CleanupFilesArgs struct{}

// Kind implements river.JobArgs.
func (CleanupFilesArgs) Kind() string { return "tasks.cleanup_files" }

type cleanupFilesWorker struct {
	river.WorkerDefaults[CleanupFilesArgs]
	svc *Service
	sys *pgxpool.Pool
}

// Work implements river.Worker.
func (w *cleanupFilesWorker) Work(ctx context.Context, _ *river.Job[CleanupFilesArgs]) error {
	return w.svc.CleanupFiles(ctx, w.sys)
}

// AddWorkers registers the cleanup of files; sys must connect as
// tolkovo_system. The worker role calls it.
func (s *Service) AddWorkers(workers *river.Workers, sys *pgxpool.Pool) {
	river.AddWorker(workers, &cleanupFilesWorker{svc: s, sys: sys})
}

// CleanupFilesPeriodic schedules the cleanup every 15 minutes and once at
// start.
func CleanupFilesPeriodic() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(15*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) { return CleanupFilesArgs{}, nil },
		&river.PeriodicJobOpts{ID: "tasks.cleanup_files", RunOnStart: true},
	)
}
