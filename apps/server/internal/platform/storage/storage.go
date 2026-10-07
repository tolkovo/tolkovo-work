// Package storage keeps files: attachments, Telegram media, knowledge base
// files and app reports (docs/adr/0009-file-storage.md). Two drivers share
// one interface: FS (local disk, self-host default) and S3 (any S3-compatible
// service). Object keys are unpredictable and never contain the file name;
// name, type and size live in the attachments table.
//
// Clients never talk to the storage with server credentials. They get a
// short-lived signed request: presigned S3 URLs for S3, HMAC-signed links to
// the api process for FS.
//
// Calls go over the network (S3) or to disk and must not run inside a
// database transaction (docs/00 §6.5).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// DefaultURLTTL is how long signed links live unless configured otherwise.
const DefaultURLTTL = 15 * time.Minute

// MaxURLTTL bounds signed links: S3 refuses presigned URLs older than 7 days,
// and a long-lived link to a customer file is a leak waiting to happen.
const MaxURLTTL = 24 * time.Hour

// Errors returned by drivers. Callers match them with errors.Is.
var (
	ErrNotFound         = errors.New("storage: object not found")
	ErrExists           = errors.New("storage: object already exists")
	ErrInvalidKey       = errors.New("storage: invalid object key")
	ErrInvalidOptions   = errors.New("storage: invalid options")
	ErrTooLarge         = errors.New("storage: object exceeds size limit")
	ErrSizeMismatch     = errors.New("storage: object size does not match")
	ErrChecksumMismatch = errors.New("storage: object checksum does not match")
	ErrLinkInvalid      = errors.New("storage: signed link is invalid")
	ErrLinkExpired      = errors.New("storage: signed link has expired")
)

// Storage is implemented by FS and S3. Keys must come from NewKey: drivers
// reject anything else, which also rules out path traversal.
type Storage interface {
	// Put streams r into the object, enforcing opts. A driver may refuse to
	// overwrite an existing object (FS does, with ErrExists); callers always
	// write fresh keys.
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) (Object, error)
	// Open returns the object's content. The caller closes it.
	Open(ctx context.Context, key string) (io.ReadCloser, Object, error)
	// Stat returns the object's metadata or ErrNotFound.
	Stat(ctx context.Context, key string) (Object, error)
	// Delete removes the object. Deleting a missing object is not an error.
	Delete(ctx context.Context, key string) error
	// PresignPut returns a request the client sends to upload the object.
	PresignPut(ctx context.Context, key string, opts PresignPutOptions) (SignedRequest, error)
	// PresignGet returns a request the client sends to download the object.
	PresignGet(ctx context.Context, key string, opts PresignGetOptions) (SignedRequest, error)
}

// Object is the metadata of a stored object. ContentType is what the driver
// knows: S3 returns the stored type, FS does not keep it.
type Object struct {
	Key         string
	Size        int64
	ContentType string
	ModTime     time.Time
}

// PutOptions constrain a write. MaxSize is required.
type PutOptions struct {
	ContentType string
	// MaxSize is the upper bound in bytes; a longer stream fails with
	// ErrTooLarge and leaves nothing behind.
	MaxSize int64
	// Size, when positive, is the exact expected size (ErrSizeMismatch).
	Size int64
	// SHA256, when set, is the expected digest (ErrChecksumMismatch).
	SHA256 []byte
}

// PresignPutOptions constrain an upload link. MaxSize is required.
//
// FS enforces every field. S3 enforces ContentType, Size (signed
// Content-Length) and SHA256 (signed x-amz-checksum-sha256); it cannot
// enforce MaxSize alone, so without Size the caller must Stat the object
// after the upload and reject it if it is too large.
type PresignPutOptions struct {
	ContentType string
	MaxSize     int64
	Size        int64
	SHA256      []byte
	// TTL overrides the driver default; zero keeps it.
	TTL time.Duration
}

// PresignGetOptions shape a download link. The response always carries
// Content-Disposition: attachment, so a file never opens as a page.
type PresignGetOptions struct {
	// Filename is offered to the browser; it is sanitized.
	Filename string
	// ContentType of the response; application/octet-stream when empty.
	ContentType string
	TTL         time.Duration
}

// SignedRequest is what the client needs to perform the request: method,
// URL and the headers it must send exactly as given.
type SignedRequest struct {
	Method    string
	URL       string
	Header    http.Header
	ExpiresAt time.Time
}

// Kind separates objects by purpose (docs/02 §5.4).
type Kind string

// Object kinds.
const (
	KindConversation Kind = "conv"
	KindKB           Kind = "kb"
	KindReports      Kind = "reports"
	KindTasks        Kind = "tasks"
)

func (k Kind) valid() bool {
	switch k {
	case KindConversation, KindKB, KindReports, KindTasks:
		return true
	}
	return false
}

// Key is a parsed object key ws/{workspace_id}/{kind}/{id}.
type Key struct {
	WorkspaceID uuid.UUID
	Kind        Kind
	ID          uuid.UUID
}

// String returns the canonical key.
func (k Key) String() string {
	return "ws/" + k.WorkspaceID.String() + "/" + string(k.Kind) + "/" + k.ID.String()
}

// NewKey returns a fresh key in the workspace. The id is a random UUIDv4,
// not v7: the key must not be guessable or reveal when the file arrived.
func NewKey(workspaceID uuid.UUID, kind Kind) (string, error) {
	if workspaceID == uuid.Nil {
		return "", fmt.Errorf("%w: empty workspace id", ErrInvalidKey)
	}
	if !kind.valid() {
		return "", fmt.Errorf("%w: unknown kind %q", ErrInvalidKey, kind)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("new object id: %w", err)
	}
	return Key{WorkspaceID: workspaceID, Kind: kind, ID: id}.String(), nil
}

// ParseKey accepts only the canonical form produced by NewKey: four
// segments, lowercase hyphenated UUIDs and a known kind. Anything else,
// including "..", empty segments, backslashes and alternative UUID spellings,
// is ErrInvalidKey.
func ParseKey(key string) (Key, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "ws" {
		return Key{}, ErrInvalidKey
	}
	ws, err := canonicalUUID(parts[1])
	if err != nil || ws == uuid.Nil {
		return Key{}, ErrInvalidKey
	}
	kind := Kind(parts[2])
	if !kind.valid() {
		return Key{}, ErrInvalidKey
	}
	id, err := canonicalUUID(parts[3])
	if err != nil {
		return Key{}, ErrInvalidKey
	}
	return Key{WorkspaceID: ws, Kind: kind, ID: id}, nil
}

func canonicalUUID(s string) (uuid.UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, err
	}
	if u.String() != s {
		return uuid.Nil, ErrInvalidKey
	}
	return u, nil
}

const (
	maxContentTypeLen = 255
	maxFilenameBytes  = 255
	sha256Size        = 32
)

func (o PutOptions) validate() error {
	return validateLimits(o.ContentType, o.MaxSize, o.Size, o.SHA256)
}

func (o PresignPutOptions) validate() error {
	if o.TTL < 0 || o.TTL > MaxURLTTL {
		return fmt.Errorf("%w: TTL must be between 0 and %s", ErrInvalidOptions, MaxURLTTL)
	}
	return validateLimits(o.ContentType, o.MaxSize, o.Size, o.SHA256)
}

func (o PresignGetOptions) validate() error {
	if o.TTL < 0 || o.TTL > MaxURLTTL {
		return fmt.Errorf("%w: TTL must be between 0 and %s", ErrInvalidOptions, MaxURLTTL)
	}
	return validateContentType(o.ContentType)
}

func validateLimits(contentType string, maxSize, size int64, sum []byte) error {
	if maxSize <= 0 {
		return fmt.Errorf("%w: MaxSize must be positive", ErrInvalidOptions)
	}
	if size < 0 || size > maxSize {
		return fmt.Errorf("%w: Size must be between 0 and MaxSize", ErrInvalidOptions)
	}
	if len(sum) != 0 && len(sum) != sha256Size {
		return fmt.Errorf("%w: SHA256 must be %d bytes", ErrInvalidOptions, sha256Size)
	}
	return validateContentType(contentType)
}

// validateContentType rejects anything that is not a plain media type, so a
// signed type cannot smuggle header syntax into a response.
func validateContentType(ct string) error {
	if ct == "" {
		return nil
	}
	if len(ct) > maxContentTypeLen {
		return fmt.Errorf("%w: content type too long", ErrInvalidOptions)
	}
	for _, r := range ct {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("%w: content type has invalid characters", ErrInvalidOptions)
		}
	}
	if _, _, err := mime.ParseMediaType(ct); err != nil {
		return fmt.Errorf("%w: content type: %w", ErrInvalidOptions, err)
	}
	return nil
}

// contentTypeOrDefault returns the type to serve a download with.
func contentTypeOrDefault(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

// SanitizeFilename turns a user-supplied name into something safe to put in
// Content-Disposition: base name only, no control or format characters,
// at most 255 bytes of valid UTF-8, never empty.
func SanitizeFilename(name string) string {
	name = strings.ToValidUTF8(name, "")
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > maxFilenameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// ContentDisposition builds an attachment header value for the file name,
// RFC 2231-encoded when it is not plain ASCII.
func ContentDisposition(filename string) string {
	v := mime.FormatMediaType("attachment", map[string]string{"filename": SanitizeFilename(filename)})
	if v == "" {
		return "attachment"
	}
	return v
}
