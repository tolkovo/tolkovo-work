package tasks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Queue groups tasks: development, infrastructure and so on. Its members
// hear about new tasks without an assignee in it.
type Queue struct {
	ID        uuid.UUID
	Name      string
	MemberIDs []uuid.UUID
	OpenTasks int64
	CreatedAt time.Time
}

// QueueInput is a queue to create or change.
type QueueInput struct {
	Name      string
	MemberIDs []uuid.UUID
}

// Queues lists the queues of the workspace by name.
func (s *Service) Queues(ctx context.Context, workspaceID uuid.UUID) ([]Queue, error) {
	out := []Queue{}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListQueues(ctx, workspaceID)
		for _, r := range rows {
			out = append(out, Queue{ID: r.ID, Name: r.Name, MemberIDs: r.MemberIds, OpenTasks: r.OpenTasks, CreatedAt: r.CreatedAt})
		}
		return err
	})
	if err != nil {
		return nil, dbError(err, "list queues")
	}
	return out, nil
}

// CreateQueue adds a queue.
func (s *Service) CreateQueue(ctx context.Context, workspaceID uuid.UUID, in QueueInput) (Queue, error) {
	name, err := validQueue(in)
	if err != nil {
		return Queue{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Queue{}, err
	}
	var out Queue
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.CountQueues(ctx, workspaceID)
		if err != nil {
			return err
		}
		if n >= MaxQueues {
			return fmt.Errorf("%w: at most %d queues", ErrInvalid, MaxQueues)
		}
		row, err := q.InsertQueue(ctx, store.InsertQueueParams{ID: id, WorkspaceID: workspaceID, Name: name, CreatedAt: s.now()})
		if err != nil {
			return err
		}
		if err := setMembers(ctx, q, workspaceID, id, in.MemberIDs); err != nil {
			return err
		}
		out = Queue{ID: row.ID, Name: row.Name, MemberIDs: sorted(in.MemberIDs), CreatedAt: row.CreatedAt}
		return nil
	})
	if err != nil {
		return Queue{}, dbError(err, "create queue")
	}
	return out, nil
}

// UpdateQueue renames a queue and replaces its members.
func (s *Service) UpdateQueue(ctx context.Context, workspaceID, queueID uuid.UUID, in QueueInput) (Queue, error) {
	name, err := validQueue(in)
	if err != nil {
		return Queue{}, err
	}
	var out Queue
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.RenameQueue(ctx, store.RenameQueueParams{Name: name, WorkspaceID: workspaceID, ID: queueID})
		if err != nil {
			return err
		}
		if err := q.ClearQueueMembers(ctx, store.ClearQueueMembersParams{WorkspaceID: workspaceID, QueueID: queueID}); err != nil {
			return err
		}
		if err := setMembers(ctx, q, workspaceID, queueID, in.MemberIDs); err != nil {
			return err
		}
		out = Queue{ID: row.ID, Name: row.Name, MemberIDs: sorted(in.MemberIDs), CreatedAt: row.CreatedAt}
		return nil
	})
	if err != nil {
		return Queue{}, dbError(err, "update queue")
	}
	return out, nil
}

// DeleteQueue removes a queue; its tasks stay without one.
func (s *Service) DeleteQueue(ctx context.Context, workspaceID, queueID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		n, err := store.New(tx).DeleteQueue(ctx, store.DeleteQueueParams{WorkspaceID: workspaceID, ID: queueID})
		if err == nil && n == 0 {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return dbError(err, "delete queue")
	}
	return nil
}

func validQueue(in QueueInput) (string, error) {
	name := strings.Join(strings.Fields(in.Name), " ")
	if name == "" || utf8.RuneCountInString(name) > MaxQueue || !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: queue name must be 1 to %d characters", ErrInvalid, MaxQueue)
	}
	if len(in.MemberIDs) > 200 {
		return "", fmt.Errorf("%w: at most 200 queue members", ErrInvalid)
	}
	return name, nil
}

func setMembers(ctx context.Context, q *store.Queries, workspaceID, queueID uuid.UUID, ids []uuid.UUID) error {
	for _, id := range ids {
		if err := q.AddQueueMember(ctx, store.AddQueueMemberParams{WorkspaceID: workspaceID, QueueID: queueID, UserID: id}); err != nil {
			return err
		}
	}
	return nil
}

func sorted(ids []uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out
}

// Buttons a conversation shows (Settings.ConversationButton).
const (
	ButtonTasks  = "tasks"
	ButtonGitHub = "github"
	ButtonBoth   = "both"
)

// Settings of the section.
type Settings struct {
	Enabled            bool
	ConversationButton string
	// ReplyRU and ReplyEN are the reply about a done task, the built-in
	// text when not changed.
	ReplyRU string
	ReplyEN string
}

// ReplyPlaceholders are the placeholders of the reply.
var ReplyPlaceholders = []string{"name", "task_number"}

// Built-in replies about a done task.
const (
	DefaultReplyRU = "{name}, здравствуйте! Задачу по вашему обращению выполнили. Проверьте, пожалуйста, всё ли работает, и напишите, если нет."
	DefaultReplyEN = "Hi {name}! The task for your request is done. Please check that everything works and let us know if it does not."
)

// DefaultSettings are the settings of a workspace that never changed them.
func DefaultSettings() Settings {
	return Settings{Enabled: true, ConversationButton: ButtonBoth, ReplyRU: DefaultReplyRU, ReplyEN: DefaultReplyEN}
}

// Settings returns the settings of the section.
func (s *Service) Settings(ctx context.Context, workspaceID uuid.UUID) (Settings, error) {
	var out Settings
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		out, err = settingsIn(ctx, store.New(tx), workspaceID)
		return err
	})
	if err != nil {
		return Settings{}, dbError(err, "task settings")
	}
	return out, nil
}

// SaveSettings replaces the settings. A reply equal to the built-in one is
// stored as none, so new versions may improve the built-in text.
func (s *Service) SaveSettings(ctx context.Context, workspaceID uuid.UUID, in Settings) (Settings, error) {
	switch in.ConversationButton {
	case ButtonTasks, ButtonGitHub, ButtonBoth:
	default:
		return Settings{}, fmt.Errorf("%w: unknown button %q", ErrInvalid, in.ConversationButton)
	}
	ru, err := validReply(in.ReplyRU, DefaultReplyRU)
	if err != nil {
		return Settings{}, err
	}
	en, err := validReply(in.ReplyEN, DefaultReplyEN)
	if err != nil {
		return Settings{}, err
	}
	var out Settings
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).UpsertSettings(ctx, store.UpsertSettingsParams{
			WorkspaceID: workspaceID, Enabled: in.Enabled, ConversationButton: in.ConversationButton,
			ReplyRu: ru, ReplyEn: en, UpdatedAt: s.now(),
		})
		out = settingsFrom(row)
		return err
	})
	if err != nil {
		return Settings{}, dbError(err, "save task settings")
	}
	return out, nil
}

func validReply(text, def string) (*string, error) {
	t := strings.TrimSpace(text)
	if t == "" || t == def {
		return nil, nil
	}
	if utf8.RuneCountInString(t) > MaxReply || !utf8.ValidString(t) {
		return nil, fmt.Errorf("%w: reply must be at most %d characters", ErrInvalid, MaxReply)
	}
	if err := checkPlaceholders(t); err != nil {
		return nil, err
	}
	return &t, nil
}

// checkPlaceholders refuses a {placeholder} the reply does not know.
func checkPlaceholders(text string) error {
	rest := text
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			return nil
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return nil
		}
		name := rest[i+1 : i+j]
		known := false
		for _, p := range ReplyPlaceholders {
			known = known || p == name
		}
		if !known && name != "" && !strings.ContainsAny(name, " {\n") {
			return fmt.Errorf("%w: unknown placeholder {%s}", ErrInvalid, name)
		}
		rest = rest[i+j+1:]
	}
}

// render replaces {name} placeholders with vars.
func render(text string, vars map[string]string) string {
	pairs := make([]string, 0, 2*len(vars))
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

func settingsIn(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) (Settings, error) {
	row, err := q.GetSettings(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return settingsFrom(row), nil
}

func settingsFrom(r store.TaskSetting) Settings {
	out := Settings{Enabled: r.Enabled, ConversationButton: r.ConversationButton, ReplyRU: DefaultReplyRU, ReplyEN: DefaultReplyEN}
	if r.ReplyRu != nil {
		out.ReplyRU = *r.ReplyRu
	}
	if r.ReplyEn != nil {
		out.ReplyEN = *r.ReplyEn
	}
	return out
}
