// Package ingest receives problem reports from company apps (docs/02
// §11.6, AP-02): text, who the user is, what the app is, and log files. The
// app authenticates with an ingest key of an app channel (internal/channels),
// registers the report, uploads each file by its signed link and calls
// Complete. Complete queues the job ingest.process, which turns the report
// into a conversation (process.go, AP-03); a periodic job removes what was
// never completed and files past their retention (cleanup.go).
//
// The key is extractable from the app, so everything here is bounded: per
// IP and per key rates, the channel's limits on files and its daily quota.
// The message, the contact and file contents are personal data and never
// reach the logs.
package ingest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// Report statuses (migration 00014).
const (
	StatusUploading = "uploading"
	StatusSubmitted = "submitted"
	StatusProcessed = "processed"
	StatusExpired   = "expired"
)

// Input bounds (docs/02 §11.6).
const (
	MaxMessageChars = 10000
	MaxExtraKeys    = 20
	mb              = 1 << 20
	reportTokenPref = "tk_report_"
)

// Rates (docs/02 §13.4). Creating a report counts against the client's IP
// and the key; completing one only against the IP, with more room, since
// every report is completed once.
var (
	createPerIP   = ratelimit.Rule{Limit: 10, Window: time.Minute}
	createPerKey  = ratelimit.Rule{Limit: 60, Window: time.Hour}
	completePerIP = ratelimit.Rule{Limit: 30, Window: time.Minute}
)

// allowedTypes are the file types an app may send: text logs, structured
// logs, archives and screenshots. Process checks the real type by its bytes.
var allowedTypes = map[string]bool{
	"text/plain": true, "text/csv": true, "application/json": true, "application/xml": true, "text/xml": true,
	"application/zip": true, "application/gzip": true, "image/png": true, "image/jpeg": true,
}

// logAliases are types apps and libraries give a .log file; with a .log or
// .txt name such a file is taken as text/plain.
var logAliases = map[string]bool{"text/x-log": true, "application/octet-stream": true}

// fileType is the type a file is stored with: the declared one without
// parameters, text/plain for a log under an alias.
func fileType(name, declared string) (string, error) {
	ct, _, err := mime.ParseMediaType(declared)
	if err != nil {
		return "", err
	}
	if ext := strings.ToLower(path.Ext(name)); logAliases[ct] && (ext == ".log" || ext == ".txt") {
		return "text/plain", nil
	}
	return ct, nil
}

// Errors of the module.
var (
	ErrInvalid  = errors.New("ingest: invalid input")
	ErrNotFound = errors.New("ingest: report not found")
	// ErrUnsupportedType: a file's content type is not allowed.
	ErrUnsupportedType = errors.New("ingest: file type is not allowed")
	// ErrNotUploaded: Complete found a file missing or of another size.
	ErrNotUploaded = errors.New("ingest: a file is not uploaded")
	// ErrSubmitted: the Idempotency-Key names a report already completed.
	ErrSubmitted = errors.New("ingest: the report is already submitted")
	// ErrIdempotencyMismatch: the Idempotency-Key came with another body.
	ErrIdempotencyMismatch = errors.New("ingest: the Idempotency-Key was used with a different request")
)

// TooLargeError means the report breaks one of the channel's size limits.
type TooLargeError struct {
	// What is file, files or report.
	What  string
	Limit int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("ingest: %s over the limit of %d", e.What, e.Limit)
}

// What a TooLargeError is about.
const (
	TooLargeFile   = "file"
	TooLargeFiles  = "files"
	TooLargeReport = "report"
)

// RateLimitedError means a rate or the daily quota is used up.
type RateLimitedError struct {
	// Quota is true for the channel's daily quota, false for a rate.
	Quota      bool
	RetryAfter time.Duration
	// Rate is the key's rate when it is known, for the RateLimit-* headers.
	Rate ratelimit.Result
}

func (e *RateLimitedError) Error() string {
	if e.Quota {
		return "ingest: the daily quota of the channel is used up"
	}
	return fmt.Sprintf("ingest: too many requests, retry after %s", e.RetryAfter)
}

// Keys authenticates ingest keys; *channels.Service implements it.
type Keys interface {
	AuthenticateIngestKey(ctx context.Context, key string) (channels.IngestApp, error)
	VerifyIdentity(ctx context.Context, workspaceID, channelID uuid.UUID, externalID, token string) (bool, error)
}

// Options configure the service.
type Options struct {
	Files storage.Storage
	Keys  Keys
	Redis *redis.Client
	// KeyPrefix separates Redis keys of tests.
	KeyPrefix string
	// Jobs queues ingest.process on Complete (role api).
	Jobs *jobs.Client
	// Inbox turns reports into conversations (role worker) and serves the
	// app user's replies and messages (role api). Scanner is used by the
	// jobs and is nil without ClamAV.
	Inbox   Inbox
	Scanner Scanner
	Log     *slog.Logger
}

// Service is the module facade.
type Service struct {
	db      *db.DB
	opts    Options
	limiter *ratelimit.Limiter
	now     func() time.Time
}

// New returns the ingest service.
func New(d *db.DB, opts Options) *Service {
	return &Service{
		db: d, opts: opts, limiter: ratelimit.New(opts.Redis, opts.KeyPrefix),
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Contact is who the app user says they are. All fields are optional.
type Contact struct {
	ExternalID string `json:"external_id,omitempty"`
	Email      string `json:"email,omitempty"`
	Name       string `json:"name,omitempty"`
	// IdentityToken, signed by the company's backend, makes the contact
	// verified. It is checked and not stored.
	IdentityToken string `json:"identity_token,omitempty"`
}

// App describes the app and the device.
type App struct {
	Name      string            `json:"name,omitempty"`
	Version   string            `json:"version,omitempty"`
	OS        string            `json:"os,omitempty"`
	OSVersion string            `json:"os_version,omitempty"`
	Locale    string            `json:"locale,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// File is a file the app is about to upload.
type File struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	// SHA256 in hex; the upload link refuses other content.
	SHA256 string `json:"sha256"`
}

// Report is the body of a new report.
type Report struct {
	Message string  `json:"message"`
	Contact Contact `json:"contact"`
	App     App     `json:"app"`
	Files   []File  `json:"files"`
}

// Caller is the app making a request.
type Caller struct {
	// Key is the ingest key from the Authorization header.
	Key string
	// IP is the client address for the per-IP rate.
	IP string
}

// Upload is the signed request that puts one file.
type Upload struct {
	Name    string
	Request storage.SignedRequest
}

// Created is a registered report. Token is shown once.
type Created struct {
	ReportID        uuid.UUID
	Token           string
	ContactVerified bool
	Uploads         []Upload
	// Replayed is true when the Idempotency-Key matched an earlier report.
	Replayed bool
	// Rate is the state of the key's rate, for the RateLimit-* headers.
	Rate ratelimit.Result
}

// Create registers a report and signs an upload for each of its files.
// With an Idempotency-Key, a retry with the same body returns the same
// report with a new token and fresh links, as long as it is not completed.
func (s *Service) Create(ctx context.Context, c Caller, idempotencyKey string, in Report) (Created, error) {
	if err := s.allow(ctx, "ingest:ip:"+c.IP, createPerIP); err != nil {
		return Created{}, err
	}
	app, err := s.opts.Keys.AuthenticateIngestKey(ctx, c.Key)
	if err != nil {
		return Created{}, err
	}
	rate, err := s.limiter.Hit(ctx, "ingest:key:"+app.KeyID.String(), createPerKey)
	if err != nil {
		return Created{}, err
	}
	if !rate.Allowed {
		return Created{}, &RateLimitedError{RetryAfter: rate.Reset, Rate: rate}
	}
	if len(idempotencyKey) > 255 {
		return Created{}, fmt.Errorf("%w: Idempotency-Key must be at most 255 characters", ErrInvalid)
	}
	files, total, err := in.validate(app.Limits)
	if err != nil {
		return Created{}, err
	}
	canonical, err := json.Marshal(in)
	if err != nil {
		return Created{}, fmt.Errorf("encode report: %w", err)
	}
	requestHash := sha256.Sum256(canonical)

	if idempotencyKey != "" {
		out, found, err := s.replay(ctx, app, idempotencyKey, requestHash[:])
		if found || err != nil {
			out.Rate = rate
			return out, err
		}
	}
	day := s.now().Format("20060102")
	retry, err := s.reserve(ctx, app, day, total)
	if err != nil {
		return Created{}, err
	}
	if retry > 0 {
		return Created{}, &RateLimitedError{Quota: true, RetryAfter: retry, Rate: rate}
	}
	out, rows, err := s.insert(ctx, app, idempotencyKey, requestHash[:], in, files, total)
	if err != nil {
		s.release(ctx, app, day, total)
		if idempotencyKey != "" && isUniqueViolation(err) {
			// The same report arrived twice at once: answer as a retry.
			out, _, err = s.replay(ctx, app, idempotencyKey, requestHash[:])
		}
		out.Rate = rate
		return out, err
	}
	if out.Uploads, err = s.sign(ctx, rows); err != nil {
		return Created{}, err
	}
	out.Rate = rate
	return out, nil
}

// insert stores the report and its files.
func (s *Service) insert(
	ctx context.Context, app channels.IngestApp, idempotencyKey string, requestHash []byte, in Report, files []fileInput, total int64,
) (Created, []store.AppReportFile, error) {
	verified := false
	if in.Contact.IdentityToken != "" {
		var err error
		if verified, err = s.opts.Keys.VerifyIdentity(ctx, app.WorkspaceID, app.ChannelID, in.Contact.ExternalID, in.Contact.IdentityToken); err != nil {
			return Created{}, nil, err
		}
	}
	token, tokenHash, err := newReportToken()
	if err != nil {
		return Created{}, nil, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Created{}, nil, err
	}
	appJSON, err := json.Marshal(in.App)
	if err != nil {
		return Created{}, nil, fmt.Errorf("encode app: %w", err)
	}
	rows := make([]store.AppReportFile, 0, len(files))
	for i, f := range files {
		key, err := storage.NewKey(app.WorkspaceID, storage.KindReports)
		if err != nil {
			return Created{}, nil, err
		}
		fid, err := uuid.NewV7()
		if err != nil {
			return Created{}, nil, err
		}
		rows = append(rows, store.AppReportFile{
			ID: fid, WorkspaceID: app.WorkspaceID, ReportID: id, Position: int16(i), StorageKey: key, //nolint:gosec // G115: at most MaxFiles
			FileName: f.name, ContentType: f.contentType, Size: f.size, Sha256: f.sha256,
		})
	}
	err = s.db.InTenantTx(ctx, app.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.InsertReport(ctx, store.InsertReportParams{
			ID: id, WorkspaceID: app.WorkspaceID, ChannelID: app.ChannelID, IngestKeyID: app.KeyID, TokenHash: tokenHash,
			IdempotencyKey: optional(idempotencyKey), RequestHash: requestHash, Message: in.Message,
			ContactExternalID: optional(in.Contact.ExternalID), ContactEmail: optional(in.Contact.Email),
			ContactName: optional(in.Contact.Name), ContactVerified: verified, App: appJSON, TotalSize: total, CreatedAt: s.now(),
			RetentionDays: int16(min(max(app.Limits.RetentionDays, 1), 365)), //nolint:gosec // G115: clamped to the column's range
		}); err != nil {
			return err
		}
		for _, f := range rows {
			if _, err := q.InsertReportFile(ctx, store.InsertReportFileParams(f)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Created{}, nil, fmt.Errorf("insert report: %w", err)
	}
	return Created{ReportID: id, Token: token, ContactVerified: verified}, rows, nil
}

// replay answers a retry: the same report with a new token and fresh links.
// The first token stops working; the app keeps whichever answer it got last.
func (s *Service) replay(ctx context.Context, app channels.IngestApp, idempotencyKey string, requestHash []byte) (Created, bool, error) {
	token, tokenHash, err := newReportToken()
	if err != nil {
		return Created{}, false, err
	}
	var (
		report store.AppReport
		files  []store.AppReportFile
	)
	err = s.db.InTenantTx(ctx, app.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		report, err = q.GetReportByIdempotencyKey(ctx, store.GetReportByIdempotencyKeyParams{
			WorkspaceID: app.WorkspaceID, ChannelID: app.ChannelID, IdempotencyKey: &idempotencyKey,
		})
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(report.RequestHash, requestHash) != 1 {
			return ErrIdempotencyMismatch
		}
		n, err := q.ReplaceReportToken(ctx, store.ReplaceReportTokenParams{WorkspaceID: app.WorkspaceID, ID: report.ID, TokenHash: tokenHash})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrSubmitted
		}
		files, err = q.ReportFiles(ctx, store.ReportFilesParams{WorkspaceID: app.WorkspaceID, ReportID: report.ID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Created{}, false, nil
	}
	if err != nil {
		return Created{}, true, fmt.Errorf("replay report: %w", err)
	}
	uploads, err := s.sign(ctx, files)
	if err != nil {
		return Created{}, true, err
	}
	return Created{ReportID: report.ID, Token: token, ContactVerified: report.ContactVerified, Uploads: uploads, Replayed: true}, true, nil
}

// sign returns an upload link per file. The link fixes the exact size, the
// type and the SHA-256, so nothing else can be put under the key.
func (s *Service) sign(ctx context.Context, files []store.AppReportFile) ([]Upload, error) {
	out := make([]Upload, 0, len(files))
	for _, f := range files {
		req, err := s.opts.Files.PresignPut(ctx, f.StorageKey, storage.PresignPutOptions{
			ContentType: f.ContentType, MaxSize: f.Size, Size: f.Size, SHA256: f.Sha256,
		})
		if err != nil {
			return nil, fmt.Errorf("presign report file: %w", err)
		}
		out = append(out, Upload{Name: f.FileName, Request: req})
	}
	return out, nil
}

// Complete checks that every file of the report is in storage with its
// size, marks the report submitted and queues ingest.process in the same
// transaction. Completing twice is not an error.
// The report token proves the caller registered the report; a wrong token
// is ErrNotFound, like a report of another channel.
func (s *Service) Complete(ctx context.Context, c Caller, reportID uuid.UUID, token string) error {
	if err := s.allow(ctx, "ingest:complete-ip:"+c.IP, completePerIP); err != nil {
		return err
	}
	app, err := s.opts.Keys.AuthenticateIngestKey(ctx, c.Key)
	if err != nil {
		return err
	}
	tokenHash := sha256.Sum256([]byte(token))
	var (
		report store.AppReport
		files  []store.AppReportFile
	)
	err = s.db.InTenantTx(ctx, app.WorkspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		report, err = q.GetReport(ctx, store.GetReportParams{WorkspaceID: app.WorkspaceID, ChannelID: app.ChannelID, ID: reportID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(report.TokenHash, tokenHash[:]) != 1 {
			return ErrNotFound
		}
		files, err = q.ReportFiles(ctx, store.ReportFilesParams{WorkspaceID: app.WorkspaceID, ReportID: reportID})
		return err
	})
	if err != nil {
		return fmt.Errorf("load report: %w", err)
	}
	if report.Status != StatusUploading {
		return nil // submitted or processed already; expired reports end with their files
	}
	// Storage calls stay outside the transaction (docs/00 §6.5).
	for _, f := range files {
		obj, err := s.opts.Files.Stat(ctx, f.StorageKey)
		if errors.Is(err, storage.ErrNotFound) || (err == nil && obj.Size != f.Size) {
			return fmt.Errorf("%w: %s", ErrNotUploaded, f.FileName)
		}
		if err != nil {
			return fmt.Errorf("stat report file: %w", err)
		}
	}
	if s.opts.Jobs == nil {
		return errors.New("ingest: no job queue to process reports")
	}
	err = s.db.InTenantTx(ctx, app.WorkspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).SubmitReport(ctx, store.SubmitReportParams{WorkspaceID: app.WorkspaceID, ID: reportID, SubmittedAt: ptr(s.now())})
		if err != nil || n == 0 {
			// A concurrent Complete won and queued the job.
			return err
		}
		_, err = s.opts.Jobs.InsertTx(ctx, tx, ProcessArgs{WorkspaceID: app.WorkspaceID, ReportID: reportID}, nil)
		return err
	})
	if err != nil {
		return fmt.Errorf("submit report: %w", err)
	}
	return nil
}

func (s *Service) log() *slog.Logger {
	if s.opts.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.opts.Log
}

func (s *Service) allow(ctx context.Context, key string, rule ratelimit.Rule) error {
	res, err := s.limiter.Hit(ctx, key, rule)
	if err != nil {
		return err
	}
	if !res.Allowed {
		// Rate stays empty: the RateLimit-* headers describe the key's rate.
		return &RateLimitedError{RetryAfter: res.Reset}
	}
	return nil
}

// fileInput is a validated file.
type fileInput struct {
	name        string
	contentType string
	size        int64
	sha256      []byte
}

// validate checks the report against the input bounds and the channel's
// limits and returns the files with their total size.
func (in *Report) validate(l channels.AppLimits) ([]fileInput, int64, error) {
	if strings.TrimSpace(in.Message) == "" || utf8.RuneCountInString(in.Message) > MaxMessageChars || !utf8.ValidString(in.Message) {
		return nil, 0, fmt.Errorf("%w: message must be 1 to %d characters", ErrInvalid, MaxMessageChars)
	}
	if err := in.Contact.validate(); err != nil {
		return nil, 0, err
	}
	if err := in.App.validate(); err != nil {
		return nil, 0, err
	}
	if len(in.Files) > l.MaxFiles {
		return nil, 0, &TooLargeError{What: TooLargeFiles, Limit: int64(l.MaxFiles)}
	}
	out := make([]fileInput, 0, len(in.Files))
	var total int64
	for i, f := range in.Files {
		ct, err := fileType(f.Name, f.ContentType)
		sum, herr := hex.DecodeString(f.SHA256)
		switch {
		case f.Name == "" || len(f.Name) > 1024:
			return nil, 0, fmt.Errorf("%w: files[%d].name must be 1 to 1024 bytes", ErrInvalid, i)
		case f.Size <= 0:
			return nil, 0, fmt.Errorf("%w: files[%d].size must be positive", ErrInvalid, i)
		case herr != nil || len(sum) != sha256.Size || f.SHA256 != strings.ToLower(f.SHA256):
			return nil, 0, fmt.Errorf("%w: files[%d].sha256 must be 64 lowercase hex characters", ErrInvalid, i)
		case err != nil || !allowedTypes[ct]:
			return nil, 0, fmt.Errorf("%w: files[%d]", ErrUnsupportedType, i)
		case f.Size > int64(l.MaxFileMB)*mb:
			return nil, 0, &TooLargeError{What: TooLargeFile, Limit: int64(l.MaxFileMB) * mb}
		}
		total += f.Size
		out = append(out, fileInput{name: storage.SanitizeFilename(f.Name), contentType: ct, size: f.Size, sha256: sum})
	}
	if total > int64(l.MaxReportMB)*mb {
		return nil, 0, &TooLargeError{What: TooLargeReport, Limit: int64(l.MaxReportMB) * mb}
	}
	return out, total, nil
}

func (c *Contact) validate() error {
	switch {
	case !textOK(c.ExternalID, 255):
		return fmt.Errorf("%w: contact.external_id must be at most 255 characters", ErrInvalid)
	case !textOK(c.Name, 120):
		return fmt.Errorf("%w: contact.name must be at most 120 characters", ErrInvalid)
	case c.Email != "" && (!textOK(c.Email, 254) || !strings.Contains(c.Email, "@") || strings.ContainsAny(c.Email, " \t\r\n")):
		return fmt.Errorf("%w: contact.email is not an email address", ErrInvalid)
	case c.IdentityToken != "" && c.ExternalID == "":
		return fmt.Errorf("%w: contact.identity_token needs contact.external_id", ErrInvalid)
	case len(c.IdentityToken) > 128:
		return fmt.Errorf("%w: contact.identity_token is too long", ErrInvalid)
	}
	return nil
}

func (a *App) validate() error {
	for _, f := range []struct {
		name, val string
		max       int
	}{
		{"name", a.Name, 100}, {"version", a.Version, 50}, {"os", a.OS, 50}, {"os_version", a.OSVersion, 100}, {"locale", a.Locale, 35},
	} {
		if !textOK(f.val, f.max) {
			return fmt.Errorf("%w: app.%s must be at most %d characters", ErrInvalid, f.name, f.max)
		}
	}
	if len(a.Extra) > MaxExtraKeys {
		return fmt.Errorf("%w: app.extra must have at most %d keys", ErrInvalid, MaxExtraKeys)
	}
	for k, v := range a.Extra {
		if k == "" || !textOK(k, 64) || !textOK(v, 500) {
			return fmt.Errorf("%w: app.extra keys must be 1 to 64 and values at most 500 characters", ErrInvalid)
		}
	}
	return nil
}

// textOK: valid UTF-8 of at most max characters.
func textOK(s string, maxChars int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= maxChars
}

// newReportToken returns tk_report_ and base64url of 32 random bytes, and
// its SHA-256 to store.
func newReportToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("random: %w", err)
	}
	token := reportTokenPref + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

// isUniqueViolation reports a second report with the same Idempotency-Key.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
