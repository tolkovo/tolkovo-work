package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/guard"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Tasks from conversations (ADR-0015). A conversation linked to an open
// task waits for the fix (docs/02 §8), as with a GitHub issue; when the
// task is done, the operator gets a reply to send.

// Link links a conversation to a task that exists: one bug, many
// complaints. An open task puts the conversation to wait for the fix.
func (s *Service) Link(ctx context.Context, workspaceID, actorID, taskID, conversationID uuid.UUID) (Task, error) {
	if s.opts.Conversations == nil {
		return Task{}, errors.New("tasks: no conversations to link")
	}
	var out Task
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		if err := s.link(ctx, tx, q, row, conversationID, actorID, conversations.EventTaskLinked); err != nil {
			return err
		}
		if err := changed(ctx, tx, row, actorID, false); err != nil {
			return err
		}
		out = taskFrom(row)
		out.ConversationIDs, err = q.TaskConversationIDs(ctx, store.TaskConversationIDsParams{WorkspaceID: workspaceID, TaskID: taskID})
		return err
	})
	if err != nil {
		return Task{}, dbError(err, "link task")
	}
	return out, nil
}

// Unlink removes the link of a conversation to a task; the conversation
// stops waiting for it.
func (s *Service) Unlink(ctx context.Context, workspaceID, actorID, taskID, conversationID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		if err := s.unlink(ctx, tx, q, row, conversationID, actorID); err != nil {
			return err
		}
		return changed(ctx, tx, row, actorID, false)
	})
	if err != nil {
		return dbError(err, "unlink task")
	}
	return nil
}

// link stores the link and records it in the conversation (typ is
// EventTaskCreated or EventTaskLinked).
func (s *Service) link(ctx context.Context, tx pgx.Tx, q *store.Queries, row store.Task, conversationID, actorID uuid.UUID, typ string) error {
	n, err := q.LinkConversation(ctx, store.LinkConversationParams{
		WorkspaceID: row.WorkspaceID, TaskID: row.ID, ConversationID: conversationID, CreatedBy: &actorID, CreatedAt: s.now(),
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLinked
	}
	if _, err := s.addActivity(ctx, q, row, &actorID, "linked", nil, map[string]any{"conversation_id": conversationID}); err != nil {
		return err
	}
	if s.opts.Conversations == nil {
		return nil
	}
	open, err := q.HasOpenTask(ctx, store.HasOpenTaskParams{WorkspaceID: row.WorkspaceID, ConversationID: conversationID})
	if err != nil {
		return err
	}
	if err := s.opts.Conversations.RecordTask(ctx, tx, row.WorkspaceID, conversationID, conversations.TaskNote{
		Type: typ, Task: conversations.TaskRef{TaskID: row.ID, Number: row.Number}, ActorID: &actorID, AwaitingFix: open,
	}); err != nil {
		return err
	}
	return conversationChanged(ctx, tx, row.WorkspaceID, conversationID)
}

// unlink removes a link and records it in the conversation.
func (s *Service) unlink(ctx context.Context, tx pgx.Tx, q *store.Queries, row store.Task, conversationID, actorID uuid.UUID) error {
	n, err := q.UnlinkConversation(ctx, store.UnlinkConversationParams{WorkspaceID: row.WorkspaceID, TaskID: row.ID, ConversationID: conversationID})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	act, err := s.addActivity(ctx, q, row, &actorID, "unlinked", nil, map[string]any{"conversation_id": conversationID})
	if err != nil {
		return err
	}
	if s.opts.Conversations == nil {
		return nil
	}
	open, err := q.HasOpenTask(ctx, store.HasOpenTaskParams{WorkspaceID: row.WorkspaceID, ConversationID: conversationID})
	if err != nil {
		return err
	}
	if err := s.opts.Conversations.RecordTask(ctx, tx, row.WorkspaceID, conversationID, conversations.TaskNote{
		Type: conversations.EventTaskUnlinked, Task: conversations.TaskRef{TaskID: row.ID, Number: row.Number},
		ActorID: &actorID, AwaitingFix: open, Key: act.String(),
	}); err != nil {
		return err
	}
	return conversationChanged(ctx, tx, row.WorkspaceID, conversationID)
}

// HasOpenTask reports whether an open task keeps the conversation waiting
// for the fix; it is the module's conversations.FixWaiter.
func (s *Service) HasOpenTask(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) (bool, error) {
	return store.New(tx).HasOpenTask(ctx, store.HasOpenTaskParams{WorkspaceID: workspaceID, ConversationID: conversationID})
}

// ConversationTasks are the tasks of a conversation, what the conversation
// shows of the section and the reply to send about a done task.
type ConversationTasks struct {
	Items    []Task
	Settings Settings
	// ReplyDraft is the reply about the task done most recently, until an
	// operator writes to the customer after it; nil when there is none.
	ReplyDraft *ReplyDraft
}

// ReplyDraft is the reply about a done task the operator may send.
type ReplyDraft struct {
	TaskID uuid.UUID
	Number int64
	Text   string
}

// ConversationTasks lists the tasks of a conversation, oldest link first.
// The caller has checked the conversation is visible; locale is the
// workspace language, the reply's when the contact's is unknown.
func (s *Service) ConversationTasks(ctx context.Context, workspaceID, conversationID uuid.UUID, locale string) (ConversationTasks, error) {
	out := ConversationTasks{Items: []Task{}}
	var done *store.Task
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if out.Settings, err = settingsIn(ctx, q, workspaceID); err != nil {
			return err
		}
		rows, err := q.ConversationTasks(ctx, store.ConversationTasksParams{WorkspaceID: workspaceID, ConversationID: conversationID})
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Items = append(out.Items, taskFrom(r))
		}
		got, err := q.DoneTask(ctx, store.DoneTaskParams{WorkspaceID: workspaceID, ConversationID: conversationID})
		if err != nil || len(got) == 0 {
			return err
		}
		done = &got[0]
		return nil
	})
	if err != nil {
		return ConversationTasks{}, dbError(err, "conversation tasks")
	}
	if done != nil && s.opts.Conversations != nil {
		if out.ReplyDraft, err = s.replyDraft(ctx, workspaceID, conversationID, *done, out.Settings, locale); err != nil {
			return ConversationTasks{}, dbError(err, "draft reply")
		}
	}
	return out, nil
}

// replyDraft fills the reply template on the contact's language, or
// returns nil when an operator already wrote to the customer after the
// task was done.
func (s *Service) replyDraft(
	ctx context.Context, workspaceID, conversationID uuid.UUID, done store.Task, set Settings, locale string,
) (*ReplyDraft, error) {
	src, err := s.opts.Conversations.ReplySource(ctx, workspaceID, conversationID)
	if err != nil {
		return nil, err
	}
	if done.ClosedAt == nil || !done.ClosedAt.After(src.LastReplyAt) {
		return nil, nil
	}
	if src.ContactLocale != "" {
		locale = src.ContactLocale
	}
	text := set.ReplyRU
	if locale == "en" {
		text = set.ReplyEN
	}
	text = render(text, map[string]string{"name": src.ContactName, "task_number": fmt.Sprint(done.Number)})
	return &ReplyDraft{TaskID: done.ID, Number: done.Number, Text: truncate(text, MaxReply)}, nil
}

// Who wrote a draft (Draft.DraftedBy).
const (
	DraftedByTemplate = "template"
	DraftedByAI       = "ai"
)

// Why the AI did not write a draft that was asked of it (Draft.AIFallback).
const (
	AIFallbackBudget = "budget_exceeded"
	AIFallbackFailed = "failed"
)

// draftTimeout bounds the AI draft: the operator waits for the form.
const draftTimeout = 30 * time.Second

// Draft is what the form of a task from a conversation starts from.
type Draft struct {
	Title string
	Body  string
	// DraftedBy is DraftedByAI or DraftedByTemplate.
	DraftedBy string
	// AIFallback says why the AI did not write it although AI is on.
	AIFallback string
}

// Draft prepares a task from a conversation. With AI on, the workspace's
// model writes the title, the summary, the steps, the expected and the
// actual result, as for a GitHub issue (docs/03 §8.1); without it the
// draft is the subject and the customer's first message. The customer's
// words reach the draft with their personal data masked (guard): every
// member sees tasks, and a task outlives the deletion of the contact's
// data (ADR-0015). The AI's text comes masked already. locale is the
// workspace language.
func (s *Service) Draft(ctx context.Context, workspaceID, conversationID uuid.UUID, locale string) (Draft, error) {
	if s.opts.Conversations == nil {
		return Draft{}, errors.New("tasks: no conversations to draft from")
	}
	src, err := s.opts.Conversations.IssueSource(ctx, workspaceID, conversationID)
	if err != nil {
		return Draft{}, dbError(err, "task draft")
	}
	l := labelsFor(locale)
	out := Draft{DraftedBy: DraftedByTemplate}
	masker := guard.NewMasker()
	summary := masker.Mask(strings.TrimSpace(src.Subject))
	if summary == "" {
		summary = firstLine(masker.Mask(src.CustomerText), 120)
	}
	var b strings.Builder
	if text, fallback := s.aiDraft(ctx, workspaceID, conversationID, src, locale); text != nil {
		out.DraftedBy, out.Title = DraftedByAI, text.Title
		b.WriteString(text.Summary)
		if len(text.Steps) > 0 {
			fmt.Fprintf(&b, "\n\n%s\n", l.steps)
			for i, st := range text.Steps {
				fmt.Fprintf(&b, "%d. %s\n", i+1, st)
			}
		}
		if text.Expected != "" {
			fmt.Fprintf(&b, "\n%s\n%s\n", l.expected, text.Expected)
		}
		if text.Actual != "" {
			fmt.Fprintf(&b, "\n%s\n%s\n", l.actual, text.Actual)
		}
	} else {
		out.AIFallback, out.Title = fallback, summary
	}
	if src.App != nil {
		app := strings.TrimSpace(src.App.Name + " " + src.App.Version)
		osName := strings.TrimSpace(src.App.OS + " " + src.App.OSVersion)
		fmt.Fprintf(&b, "\n%s %s, %s\n", l.app, app, osName)
	}
	if q := strings.TrimSpace(src.CustomerText); q != "" {
		fmt.Fprintf(&b, "\n%s\n%s\n", l.quote, truncate(masker.Mask(q), 4000))
	}
	out.Title = firstLine(out.Title, MaxTitle)
	if out.Title == "" {
		out.Title = fmt.Sprintf(l.untitled, src.Number)
	}
	out.Body = strings.TrimSpace(b.String())
	return out, nil
}

// aiDraft asks the model for the draft. A nil text with an empty fallback
// means AI is off or there is nothing to draft from.
func (s *Service) aiDraft(
	ctx context.Context, workspaceID, conversationID uuid.UUID, src conversations.IssueSource, locale string,
) (*ai.IssueText, string) {
	if s.opts.Drafter == nil || strings.TrimSpace(src.CustomerText) == "" {
		return nil, ""
	}
	in := ai.IssueReport{ConversationID: conversationID, Locale: locale, Subject: src.Subject, Text: src.CustomerText}
	if src.App != nil {
		in.App = ai.AppInfo{Name: src.App.Name, Version: src.App.Version, OS: src.App.OS, OSVersion: src.App.OSVersion, Locale: src.App.Locale}
	}
	ctx, cancel := context.WithTimeout(ctx, draftTimeout)
	defer cancel()
	text, err := s.opts.Drafter.DraftIssue(ctx, workspaceID, in)
	switch {
	case err == nil:
		return &text, ""
	case errors.Is(err, ai.ErrNotConfigured):
		return nil, ""
	case errors.Is(err, ai.ErrBudgetExceeded):
		return nil, AIFallbackBudget
	}
	// The error names the kind of failure, never the provider's text.
	s.log().WarnContext(ctx, "task draft: the AI failed, the customer's words are used",
		"workspace_id", workspaceID, "conversation_id", conversationID, "error", err)
	return nil, AIFallbackFailed
}

// draftLabels are the headings of a drafted description.
type draftLabels struct {
	steps, expected, actual, app, quote, untitled string
}

func labelsFor(locale string) draftLabels {
	if locale == "en" {
		return draftLabels{
			steps: "Steps to reproduce:", expected: "Expected:", actual: "Actual:", app: "App:",
			quote: "Customer's message:", untitled: "Conversation #%d",
		}
	}
	return draftLabels{
		steps: "Шаги воспроизведения:", expected: "Ожидалось:", actual: "Фактически:", app: "Приложение:",
		quote: "Сообщение клиента:", untitled: "Обращение №%d",
	}
}

// firstLine is the first line of text with spaces collapsed, up to n
// characters.
func firstLine(text string, n int) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return truncate(strings.Join(strings.Fields(line), " "), n)
}

// truncate cuts text to n characters, ending with "…" when cut.
func truncate(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	r := []rune(text)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
