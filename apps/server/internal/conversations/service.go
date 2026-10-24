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
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
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

// Attention is why a conversation needs its member.
type Attention string

// Reasons of NeedsAttention.
const (
	// AttentionAssigned: someone else or the router gave it to the member.
	AttentionAssigned Attention = "assigned"
	// AttentionMessage: the customer wrote in it.
	AttentionMessage Attention = "message"
)

// Notifier hears about changes a member may need to act on, so it can
// remind them later (docs/04 §5.5) or tell them in Telegram (docs/06 I-06).
// It runs inside the transaction of the change: it may write rows and queue
// jobs there, never call the network.
type Notifier interface {
	NeedsAttention(ctx context.Context, tx pgx.Tx, workspaceID, conversationID, userID uuid.UUID, why Attention) error
	// WaitsInQueue hears that a customer's conversation entered the queue:
	// a new one, one the AI bot handed over, one the customer reopened.
	// topicID is its topic, whose members alone take it when it has some.
	WaitsInQueue(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, topicID *uuid.UUID) error
}

// Router distributes the queue (docs/02 §9). Its methods run inside the
// transaction of the change: they may write rows and queue jobs there,
// never call the network.
type Router interface {
	// Queued hears that a conversation entered the queue. releasedBy is the
	// member who gave it back, or nil.
	Queued(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, releasedBy *uuid.UUID) error
	// Available reports whether a conversation the customer reopens may go
	// back to userID rather than to the queue.
	Available(ctx context.Context, tx pgx.Tx, workspaceID, userID uuid.UUID) (bool, error)
	// Freed hears that a conversation left the work of userID, so they may
	// have room for another (ADR-0021).
	Freed(ctx context.Context, tx pgx.Tx, workspaceID, userID uuid.UUID) error
}

// Service is the module facade.
type Service struct {
	db       *db.DB
	files    Files
	notifier Notifier
	// router hears about the queue (Q-01); nil leaves the queue to operators.
	router Router
	// deliverer queues replies for channels (T-03); nil leaves them pending.
	deliverer Deliverer
	// bot is the AI bot (ADR-0018); nil sends every conversation to the queue.
	bot Bot
	// waiters are the other sources a conversation waits for a fix on
	// (GitHub issues, tasks), by name.
	waiters map[string]FixWaiter
	now     func() time.Time
}

// New returns the conversations service.
func New(d *db.DB, files Files) *Service {
	return &Service{db: d, files: files, now: func() time.Time { return time.Now().UTC() }}
}

// SetNotifier connects the notifications module; call it before serving.
func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

// SetRouter connects the routing module; call it before serving.
func (s *Service) SetRouter(r Router) { s.router = r }

// FixWaiter reports, inside the caller's transaction, whether a source
// keeps the conversation waiting for a fix (an open issue, an open task).
type FixWaiter func(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) (bool, error)

// Sources of waiting for a fix (AddFixWaiter).
const (
	FixSourceIssues = "issues"
	FixSourceTasks  = "tasks"
)

// AddFixWaiter connects a source of waiting for a fix; call it before
// serving. Recording an event of one source asks the others, so closing an
// issue does not end waiting for an open task, and the other way round.
func (s *Service) AddFixWaiter(source string, w FixWaiter) {
	if s.waiters == nil {
		s.waiters = map[string]FixWaiter{}
	}
	s.waiters[source] = w
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

// answering are the roles that answer customers: only their members are
// given conversations (ADR-0020: the programmer reads linked ones only).
var answering = authz.RolesWith(authz.ConversationsReply)

// waitsInQueue tells the notifier that a customer's conversation waits in
// the queue.
func (s *Service) waitsInQueue(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, topicID *uuid.UUID) error {
	if s.notifier == nil {
		return nil
	}
	return s.notifier.WaitsInQueue(ctx, tx, workspaceID, conversationID, topicID)
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
	ErrClosed, ErrIdempotencyMismatch, ErrNotUploaded, ErrTagExists, ErrNotFailed, ErrConversationOpen, ErrTopicExists,
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
