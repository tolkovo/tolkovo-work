// Package conversations owns contacts, conversations, messages and their
// attachments: the data model of docs/02 §7.2 and the state machine of §8.
// Other modules use the Service facade; the store package is private.
//
// Every method takes the workspace from the caller's authenticated context
// and runs in db.InTenantTx. Domain events go to the outbox in the same
// transaction; storage calls (network for S3) happen outside transactions.
package conversations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// Domain errors. The api layer maps them to problem+json codes.
var (
	ErrNotFound = errors.New("conversations: not found")
	ErrInvalid  = errors.New("conversations: invalid input")
	// ErrTransition is a status change the state machine does not allow.
	ErrTransition = errors.New("conversations: transition not allowed")
	// ErrConflict means the conversation changed since the caller read it.
	ErrConflict = errors.New("conversations: version conflict")
	// ErrNotAssignee is an action on a conversation assigned to someone
	// else (or nobody) by a caller allowed to act only on their own.
	ErrNotAssignee = errors.New("conversations: not your conversation")
	// ErrClosed rejects new messages in a closed conversation.
	ErrClosed = errors.New("conversations: conversation is closed")
	// ErrIdempotencyMismatch is an Idempotency-Key reused with another body.
	ErrIdempotencyMismatch = errors.New("conversations: idempotency key reused with a different request")
	// ErrNotUploaded is an attachment whose file is missing in storage.
	ErrNotUploaded = errors.New("conversations: attachment is not uploaded")
	// ErrTagExists is a new tag whose name is taken.
	ErrTagExists = errors.New("conversations: tag name is taken")
)

// Limits of the API (docs/00 §9.4).
const (
	DefaultPageSize   = 50
	MaxPageSize       = 100
	MaxBodyLength     = 10000 // characters, as the messages.body CHECK
	MaxAttachments    = 10    // per message
	MaxAttachmentSize = 25 << 20
)

// Files is the part of the file storage the module needs; storage.FS and
// storage.S3 implement it.
type Files interface {
	Stat(ctx context.Context, key string) (storage.Object, error)
	PresignPut(ctx context.Context, key string, opts storage.PresignPutOptions) (storage.SignedRequest, error)
	PresignGet(ctx context.Context, key string, opts storage.PresignGetOptions) (storage.SignedRequest, error)
}

// Actor is the member performing an action and what they may do beyond
// their own conversations. The api layer fills the flags from authz.
type Actor struct {
	UserID uuid.UUID
	// AnyConversation lets the actor reply in and change the status of
	// conversations assigned to someone else or to nobody.
	AnyConversation bool
	// AssignOthers lets the actor assign conversations to other members and
	// take or release conversations of others.
	AssignOthers bool
}

// Service is the module facade.
type Service struct {
	db    *db.DB
	files Files
	now   func() time.Time
}

// New returns the conversations service.
func New(d *db.DB, files Files) *Service {
	return &Service{db: d, files: files, now: func() time.Time { return time.Now().UTC() }}
}

// Page is one page of a cursor-paginated list. NextCursor is empty on the
// last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// tx runs fn in a tenant transaction with the module's queries.
func (s *Service) tx(ctx context.Context, workspaceID uuid.UUID, fn func(q *store.Queries, tx pgx.Tx) error) error {
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return fn(store.New(tx), tx)
	})
}

func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("new id: %w", err)
	}
	return id, nil
}

func pageSize(limit int) int32 {
	if limit <= 0 {
		return DefaultPageSize
	}
	return int32(min(limit, MaxPageSize)) //nolint:gosec // G115: bounded by MaxPageSize
}

var domainErrors = []error{
	ErrNotFound, ErrInvalid, ErrTransition, ErrConflict, ErrNotAssignee,
	ErrClosed, ErrIdempotencyMismatch, ErrNotUploaded, ErrTagExists,
}

// wrap returns domain errors as they are, turns "no rows" into ErrNotFound
// and adds context to everything else.
func wrap(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	for _, d := range domainErrors {
		if errors.Is(err, d) {
			return err
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func ptr[T any](v T) *T { return &v }
