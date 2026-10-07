// Package tasks is the tasks section (ADR-0015, docs/02 §12.5): a simple
// tracker for developers and the IT team next to GitHub issues. A task has
// a number in the workspace, a queue, a status, a priority and an
// assignee; comments and changes are its activity feed. Tasks are created
// by hand or from a conversation, with a draft the workspace's model
// writes when AI is on; a task can be linked to several conversations,
// and while it is open they wait for the fix (docs/02 §8). Done, it brings
// them back to their operators with a reply to send.
//
// Every member sees and works on tasks; queues, the settings and deleting
// tasks are for owners and admins (internal/platform/authz).
package tasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
)

// Statuses of a task (migration 00029).
const (
	StatusOpen       = "open"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusCanceled   = "canceled"
)

// Statuses lists every status in the order of the board.
var Statuses = []string{StatusOpen, StatusInProgress, StatusDone, StatusCanceled}

// Priorities of a task, lowest first.
var Priorities = []string{"low", "normal", "high", "urgent"}

// PriorityNormal is the priority of a task created without one.
const PriorityNormal = "normal"

// Limits of a task and its feed.
const (
	MaxTitle   = 256
	MaxBody    = 20000
	MaxComment = 4000
	MaxQueue   = 60
	MaxQueues  = 50
	MaxReply   = 4000
	// pageLimit bounds one page of the list.
	pageLimit = 100
)

// Errors of the module. The api layer maps them to problem+json codes.
var (
	ErrInvalid  = errors.New("tasks: invalid input")
	ErrNotFound = errors.New("tasks: not found")
	// ErrConflict: the task changed since the caller read it.
	ErrConflict = errors.New("tasks: the task changed meanwhile")
	// ErrQueueTaken: a queue of the workspace has this name.
	ErrQueueTaken = errors.New("tasks: a queue with this name exists")
	// ErrMember: the assignee or a queue member is not a member of the workspace.
	ErrMember = errors.New("tasks: not a member of the workspace")
	// ErrLinked: the conversation is linked to this task already.
	ErrLinked = errors.New("tasks: the conversation is linked to this task already")
	// ErrDisabled: the section is turned off in the settings.
	ErrDisabled = errors.New("tasks: the tasks section is turned off")
)

// Conversations is the part of the conversations module tasks need;
// *conversations.Service implements it.
type Conversations interface {
	IssueSource(ctx context.Context, workspaceID, conversationID uuid.UUID) (conversations.IssueSource, error)
	RecordTask(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, n conversations.TaskNote) error
	ReplySource(ctx context.Context, workspaceID, conversationID uuid.UUID) (conversations.ReplySource, error)
}

// Drafter writes drafts with the workspace's model; *ai.Service implements
// it. ai.ErrNotConfigured means AI is off.
type Drafter interface {
	DraftIssue(ctx context.Context, workspaceID uuid.UUID, in ai.IssueReport) (ai.IssueText, error)
}

// Notifier tells members about tasks outside the panel (Telegram), from
// inside the transaction of the change: it only queues the messages.
type Notifier interface {
	TaskNotify(ctx context.Context, tx pgx.Tx, n Notice) error
}

// What a notice is about (Notice.Kind).
const (
	// NoticeAssigned: the task was given to the member.
	NoticeAssigned = "assigned"
	// NoticeQueued: a new task without an assignee is in a queue of the member.
	NoticeQueued = "queued"
)

// Notice is a message about a task for members; only ids, the sender reads
// the task when it sends.
type Notice struct {
	WorkspaceID uuid.UUID
	TaskID      uuid.UUID
	Kind        string
	UserIDs     []uuid.UUID
}

// Options configure the module.
type Options struct {
	// Conversations links tasks to conversations; nil leaves tasks alone.
	Conversations Conversations
	// Drafter writes drafts from conversations; nil leaves them to the
	// customer's words.
	Drafter Drafter
	// Notifier sends notices outside the panel; nil sends none.
	Notifier Notifier
	// Files keeps the files of tasks; nil refuses files.
	Files Storage
	// Scanner checks files for viruses; nil checks their type only.
	Scanner Scanner
	Log     *slog.Logger
}

// Service is the facade of the module.
type Service struct {
	db   *db.DB
	opts Options
	now  func() time.Time
}

// New returns the service.
func New(database *db.DB, opts Options) *Service {
	return &Service{db: database, opts: opts, now: func() time.Time { return time.Now().UTC() }}
}

// SetNotifier connects the sender of notices; call it before serving.
func (s *Service) SetNotifier(n Notifier) { s.opts.Notifier = n }

func (s *Service) log() *slog.Logger {
	if s.opts.Log != nil {
		return s.opts.Log
	}
	return slog.Default()
}

// validText: UTF-8 without NUL, which Postgres text cannot hold.
func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }

// validTitle trims a title and checks it is one line of 1 to MaxTitle characters.
func validTitle(title string) (string, error) {
	t := strings.TrimSpace(title)
	if t == "" || utf8.RuneCountInString(t) > MaxTitle || strings.ContainsAny(t, "\r\n") || !validText(t) {
		return "", fmt.Errorf("%w: title must be 1 to %d characters on one line", ErrInvalid, MaxTitle)
	}
	return t, nil
}

func validBody(body string) (string, error) {
	b := strings.TrimRight(body, " \n\r\t")
	if utf8.RuneCountInString(b) > MaxBody || !validText(b) {
		return "", fmt.Errorf("%w: description must be at most %d characters", ErrInvalid, MaxBody)
	}
	return b, nil
}

func validPriority(p string) error {
	for _, v := range Priorities {
		if p == v {
			return nil
		}
	}
	return fmt.Errorf("%w: unknown priority %q", ErrInvalid, p)
}

func validStatus(st string) error {
	for _, v := range Statuses {
		if st == v {
			return nil
		}
	}
	return fmt.Errorf("%w: unknown status %q", ErrInvalid, st)
}

func closed(status string) bool { return status == StatusDone || status == StatusCanceled }

// dbError maps database errors of the module.
func dbError(err error, what string) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case pg.Code == "23505" && pg.ConstraintName == "task_queues_name_uq":
			return ErrQueueTaken
		case pg.Code == "23503" && strings.Contains(pg.ConstraintName, "assignee"),
			pg.Code == "23503" && strings.Contains(pg.ConstraintName, "user"),
			pg.Code == "23503" && strings.Contains(pg.ConstraintName, "created_by"):
			return ErrMember
		case pg.Code == "22021" || pg.Code == "22P05": // a character text cannot hold
			return fmt.Errorf("%w: text holds a character that cannot be stored", ErrInvalid)
		case pg.Code == "23503" && strings.Contains(pg.ConstraintName, "queue"):
			return fmt.Errorf("%w: unknown queue", ErrInvalid)
		case pg.Code == "23503" && strings.Contains(pg.ConstraintName, "conversation"):
			return ErrNotFound
		}
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, conversations.ErrNotFound) {
		return ErrNotFound
	}
	for _, d := range []error{ErrInvalid, ErrNotFound, ErrConflict, ErrQueueTaken, ErrMember, ErrLinked, ErrDisabled} {
		if errors.Is(err, d) {
			return err
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}
