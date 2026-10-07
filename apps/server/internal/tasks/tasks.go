package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks/store"
)

// Task is a task of the section.
type Task struct {
	ID      uuid.UUID
	Number  int64
	QueueID *uuid.UUID
	// TopicID is the topic of conversations (docs/02 §7.3); a task from a
	// conversation takes the conversation's.
	TopicID    *uuid.UUID
	Title      string
	Body       string
	Status     string
	Priority   string
	AssigneeID *uuid.UUID
	CreatedBy  *uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ClosedAt   *time.Time
	Version    int32
	// ConversationIDs are the linked conversations, oldest link first;
	// filled by Get, not by lists.
	ConversationIDs []uuid.UUID
	// Files of the description; filled for one task, not in lists.
	Files []File
}

// NewTask is a task to create.
type NewTask struct {
	Title   string
	Body    string
	QueueID *uuid.UUID
	// TopicID, when nil, is the conversation's topic for a task from a
	// conversation; a topic with a task queue gives a task without one
	// that queue.
	TopicID    *uuid.UUID
	Priority   string
	AssigneeID *uuid.UUID
	// ConversationID links the task to the conversation it is created
	// from, which then waits for the fix; nil for a task made by hand.
	ConversationID *uuid.UUID
	// FileIDs are the creator's uploaded files for the description.
	FileIDs []uuid.UUID
}

// Create records a task by actorID. A task from a conversation is linked
// to it at once.
func (s *Service) Create(ctx context.Context, workspaceID, actorID uuid.UUID, in NewTask) (Task, error) {
	title, err := validTitle(in.Title)
	if err != nil {
		return Task{}, err
	}
	body, err := validBody(in.Body)
	if err != nil {
		return Task{}, err
	}
	if in.Priority == "" {
		in.Priority = PriorityNormal
	}
	if err := validPriority(in.Priority); err != nil {
		return Task{}, err
	}
	if in.ConversationID != nil && s.opts.Conversations == nil {
		return Task{}, errors.New("tasks: no conversations to link")
	}
	if err := s.checkFiles(ctx, workspaceID, actorID, in.FileIDs); err != nil {
		return Task{}, err
	}
	in.Title, in.Body = title, body
	var out Task
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		out, err = s.create(ctx, tx, workspaceID, &actorID, in)
		return err
	})
	if err != nil {
		return Task{}, dbError(err, "create task")
	}
	return out, nil
}

// CreateFromBot records, in the caller's transaction, a task the AI bot
// collected from the customer in a conversation (ADR-0018), and returns
// its number: nobody created it, it is linked to the conversation and
// takes its topic and the topic's queue. While the section is off it
// creates nothing and returns 0.
func (s *Service) CreateFromBot(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID, title, body string) (int64, error) {
	title, err := validTitle(title)
	if err != nil {
		return 0, err
	}
	if body, err = validBody(body); err != nil {
		return 0, err
	}
	if s.opts.Conversations == nil {
		return 0, errors.New("tasks: no conversations to link")
	}
	out, err := s.create(ctx, tx, workspaceID, nil, NewTask{Title: title, Body: body, Priority: PriorityNormal, ConversationID: &conversationID})
	if errors.Is(err, ErrDisabled) {
		return 0, nil // checked first: nothing was written
	}
	if err != nil {
		return 0, dbError(err, "create task from bot")
	}
	return out.Number, nil
}

// create records a checked task in tx; actor is nil for the AI bot, whose
// tasks have no files.
func (s *Service) create(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, actor *uuid.UUID, in NewTask) (Task, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Task{}, err
	}
	q := store.New(tx)
	if err := s.enabled(ctx, q, workspaceID); err != nil {
		return Task{}, err
	}
	if err := s.topicDefaults(ctx, q, workspaceID, &in); err != nil {
		return Task{}, err
	}
	n, err := q.NextTaskNumber(ctx, workspaceID)
	if err != nil {
		return Task{}, err
	}
	row, err := q.InsertTask(ctx, store.InsertTaskParams{
		ID: id, WorkspaceID: workspaceID, Number: n, QueueID: in.QueueID, TopicID: in.TopicID, Title: in.Title, Body: in.Body,
		Priority: in.Priority, AssigneeID: in.AssigneeID, CreatedBy: actor, CreatedAt: s.now(),
	})
	if err != nil {
		return Task{}, err
	}
	if _, err := s.addActivity(ctx, q, row, actor, "created", nil, nil); err != nil {
		return Task{}, err
	}
	var actorID uuid.UUID // uuid.Nil for the bot: nobody is left out of the notices
	if actor != nil {
		actorID = *actor
		if err := s.attachFiles(ctx, q, row, actorID, nil, in.FileIDs); err != nil {
			return Task{}, err
		}
	}
	out := taskFrom(row)
	if out.Files, _, err = taskFiles(ctx, q, workspaceID, id); err != nil {
		return Task{}, err
	}
	if in.ConversationID != nil {
		if err := s.link(ctx, tx, q, row, *in.ConversationID, actor, conversations.EventTaskCreated); err != nil {
			return Task{}, err
		}
		out.ConversationIDs = []uuid.UUID{*in.ConversationID}
	}
	if err := s.notifyNew(ctx, tx, q, row, actorID); err != nil {
		return Task{}, err
	}
	return out, changed(ctx, tx, row, actor, row.AssigneeID != nil)
}

// topicDefaults fills what a new task takes from topics: the topic of the
// conversation it is created from, and the topic's queue for a task
// without one. A topic that does not exist is ErrInvalid.
func (s *Service) topicDefaults(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, in *NewTask) error {
	if in.TopicID == nil && in.ConversationID != nil {
		topic, err := q.ConversationTopic(ctx, store.ConversationTopicParams{WorkspaceID: workspaceID, ID: *in.ConversationID})
		if err != nil {
			return err // no rows: the conversation does not exist
		}
		in.TopicID = topic
	}
	if in.TopicID == nil {
		return nil
	}
	t, err := q.TopicQueue(ctx, store.TopicQueueParams{WorkspaceID: workspaceID, ID: *in.TopicID})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: unknown topic", ErrInvalid)
	}
	if err != nil {
		return err
	}
	if in.QueueID == nil {
		in.QueueID = t.TaskQueueID
	}
	return nil
}

// Get returns a task with its linked conversations.
func (s *Service) Get(ctx context.Context, workspaceID, taskID uuid.UUID) (Task, error) {
	var out Task
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTask(ctx, store.GetTaskParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		out = taskFrom(row)
		out.ConversationIDs, err = q.TaskConversationIDs(ctx, store.TaskConversationIDsParams{WorkspaceID: workspaceID, TaskID: taskID})
		if err != nil {
			return err
		}
		out.Files, _, err = taskFiles(ctx, q, workspaceID, taskID)
		return err
	})
	if err != nil {
		return Task{}, dbError(err, "get task")
	}
	return out, nil
}

// IDByNumber finds a task by its number in the workspace (#42), as people
// and agents call it.
func (s *Service) IDByNumber(ctx context.Context, workspaceID uuid.UUID, number int64) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		id, err = store.New(tx).TaskIDByNumber(ctx, store.TaskIDByNumberParams{WorkspaceID: workspaceID, Number: number})
		return err
	})
	if err != nil {
		return uuid.Nil, dbError(err, "find task")
	}
	return id, nil
}

// Filter narrows the list. Zero values do not filter.
type Filter struct {
	// State is "open" (not done or canceled), "closed" or "" for all.
	State      string
	Status     string
	AssigneeID *uuid.UUID
	Unassigned bool
	QueueID    *uuid.UUID
	NoQueue    bool
	TopicID    *uuid.UUID
	NoTopic    bool
	// Query matches the title, or the number exactly.
	Query string
	// Before is the number to continue below; 0 starts from the newest.
	Before int64
	Limit  int
}

// Page is one page of tasks; Next is the number to pass as Before for the
// next one, 0 on the last page.
type Page struct {
	Items []Task
	Next  int64
}

// List returns tasks newest first.
func (s *Service) List(ctx context.Context, workspaceID uuid.UUID, f Filter) (Page, error) {
	switch f.State {
	case "", "open", "closed":
	default:
		return Page{}, fmt.Errorf("%w: unknown state %q", ErrInvalid, f.State)
	}
	if f.Status != "" {
		if err := validStatus(f.Status); err != nil {
			return Page{}, err
		}
	}
	if utf8.RuneCountInString(f.Query) > 100 || !validText(f.Query) {
		return Page{}, fmt.Errorf("%w: query must be at most 100 characters", ErrInvalid)
	}
	limit := 50
	if f.Limit > 0 {
		limit = min(f.Limit, pageLimit)
	}
	var status *string
	if f.Status != "" {
		status = &f.Status
	}
	out := Page{Items: []Task{}}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListTasks(ctx, store.ListTasksParams{
			WorkspaceID: workspaceID, BeforeNumber: f.Before, OpenOnly: f.State == "open", ClosedOnly: f.State == "closed",
			AssigneeID: f.AssigneeID, Unassigned: f.Unassigned, QueueID: f.QueueID, NoQueue: f.NoQueue, TopicID: f.TopicID, NoTopic: f.NoTopic, Status: status,
			Query: likeEscape(strings.TrimSpace(f.Query)), Lim: int32(limit + 1), //nolint:gosec // G115: bounded by pageLimit
		})
		if err != nil {
			return err
		}
		for i, r := range rows {
			if i == limit {
				out.Next = rows[limit-1].Number
				break
			}
			out.Items = append(out.Items, taskFrom(r))
		}
		return nil
	})
	if err != nil {
		return Page{}, dbError(err, "list tasks")
	}
	return out, nil
}

// OpenAssigned counts the open tasks of a member, for the menu.
func (s *Service) OpenAssigned(ctx context.Context, workspaceID, userID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).CountOpenAssigned(ctx, store.CountOpenAssignedParams{WorkspaceID: workspaceID, AssigneeID: &userID})
		return err
	})
	if err != nil {
		return 0, dbError(err, "count tasks")
	}
	return n, nil
}

// Update is the whole changeable state of a task, read at Version.
type Update struct {
	Version    int32
	Title      string
	Body       string
	Status     string
	Priority   string
	QueueID    *uuid.UUID
	TopicID    *uuid.UUID
	AssigneeID *uuid.UUID
}

// Update changes a task by actorID. Version must be the one the caller
// read, or ErrConflict. A task put in progress without an assignee goes to
// the actor. Closing or reopening a task records it in its conversations:
// done brings them back from waiting for the fix.
func (s *Service) Update(ctx context.Context, workspaceID, actorID, taskID uuid.UUID, in Update) (Task, error) {
	title, err := validTitle(in.Title)
	if err != nil {
		return Task{}, err
	}
	body, err := validBody(in.Body)
	if err != nil {
		return Task{}, err
	}
	if err := validStatus(in.Status); err != nil {
		return Task{}, err
	}
	if err := validPriority(in.Priority); err != nil {
		return Task{}, err
	}
	var out Task
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		if cur.Version != in.Version {
			return ErrConflict
		}
		assignee := in.AssigneeID
		if in.Status == StatusInProgress && cur.Status != StatusInProgress && assignee == nil {
			assignee = &actorID
		}
		now := s.now()
		closedAt := cur.ClosedAt
		switch {
		case closed(in.Status) && (!closed(cur.Status) || cur.Status != in.Status):
			closedAt = &now
		case !closed(in.Status):
			closedAt = nil
		}
		row, err := q.UpdateTask(ctx, store.UpdateTaskParams{
			QueueID: in.QueueID, TopicID: in.TopicID, Title: title, Body: body, Status: in.Status, Priority: in.Priority,
			AssigneeID: assignee, ClosedAt: closedAt, UpdatedAt: now, WorkspaceID: workspaceID, ID: taskID,
		})
		if err != nil {
			return err
		}
		if err := s.recordChanges(ctx, tx, q, cur, row, actorID); err != nil {
			return err
		}
		out = taskFrom(row)
		out.ConversationIDs, err = q.TaskConversationIDs(ctx, store.TaskConversationIDsParams{WorkspaceID: workspaceID, TaskID: taskID})
		if err != nil {
			return err
		}
		out.Files, _, err = taskFiles(ctx, q, workspaceID, taskID)
		return err
	})
	if err != nil {
		return Task{}, dbError(err, "update task")
	}
	return out, nil
}

// recordChanges writes the activity of an update, tells the linked
// conversations about a task closed or reopened, and notifies a new
// assignee.
func (s *Service) recordChanges(ctx context.Context, tx pgx.Tx, q *store.Queries, cur, row store.Task, actorID uuid.UUID) error {
	actor := &actorID
	if cur.Title != row.Title || cur.Body != row.Body {
		var fields []string
		if cur.Title != row.Title {
			fields = append(fields, "title")
		}
		if cur.Body != row.Body {
			fields = append(fields, "body")
		}
		if _, err := s.addActivity(ctx, q, row, actor, "edited", nil, map[string]any{"fields": fields}); err != nil {
			return err
		}
	}
	if cur.Priority != row.Priority {
		if _, err := s.addActivity(ctx, q, row, actor, "priority", nil, map[string]any{"from": cur.Priority, "to": row.Priority}); err != nil {
			return err
		}
	}
	if !sameID(cur.QueueID, row.QueueID) {
		if _, err := s.addActivity(ctx, q, row, actor, "queue", nil, map[string]any{"from": cur.QueueID, "to": row.QueueID}); err != nil {
			return err
		}
	}
	if !sameID(cur.TopicID, row.TopicID) {
		if _, err := s.addActivity(ctx, q, row, actor, "topic", nil, map[string]any{"from": cur.TopicID, "to": row.TopicID}); err != nil {
			return err
		}
	}
	if !sameID(cur.AssigneeID, row.AssigneeID) {
		if _, err := s.addActivity(ctx, q, row, actor, "assignee", nil, map[string]any{"from": cur.AssigneeID, "to": row.AssigneeID}); err != nil {
			return err
		}
		if row.AssigneeID != nil && *row.AssigneeID != actorID && !closed(row.Status) {
			if err := s.notify(ctx, tx, row, NoticeAssigned, []uuid.UUID{*row.AssigneeID}); err != nil {
				return err
			}
		}
	}
	if cur.Status != row.Status {
		act, err := s.addActivity(ctx, q, row, actor, "status", nil, map[string]any{"from": cur.Status, "to": row.Status})
		if err != nil {
			return err
		}
		if err := s.recordStatus(ctx, tx, q, cur, row, actorID, act); err != nil {
			return err
		}
	}
	return changed(ctx, tx, row, &actorID, row.AssigneeID != nil && !sameID(cur.AssigneeID, row.AssigneeID))
}

// recordStatus tells the linked conversations that the task was closed or
// reopened; act keys the feed lines, so each change writes them once.
func (s *Service) recordStatus(ctx context.Context, tx pgx.Tx, q *store.Queries, cur, row store.Task, actorID, act uuid.UUID) error {
	var typ, reason string
	switch {
	case closed(row.Status) && !closed(cur.Status), closed(row.Status) && closed(cur.Status):
		typ, reason = conversations.EventTaskClosed, conversations.TaskDone
		if row.Status == StatusCanceled {
			reason = conversations.TaskCanceled
		}
	case !closed(row.Status) && closed(cur.Status):
		typ = conversations.EventTaskReopened
	default:
		return nil // open ↔ in progress: the conversations keep waiting
	}
	convs, err := q.TaskConversationIDs(ctx, store.TaskConversationIDsParams{WorkspaceID: row.WorkspaceID, TaskID: row.ID})
	if err != nil || len(convs) == 0 || s.opts.Conversations == nil {
		return err
	}
	for _, c := range convs {
		open, err := q.HasOpenTask(ctx, store.HasOpenTaskParams{WorkspaceID: row.WorkspaceID, ConversationID: c})
		if err != nil {
			return err
		}
		if err := s.opts.Conversations.RecordTask(ctx, tx, row.WorkspaceID, c, conversations.TaskNote{
			Type: typ, Task: conversations.TaskRef{TaskID: row.ID, Number: row.Number, Reason: reason},
			ActorID: &actorID, AwaitingFix: open, Key: act.String(),
		}); err != nil {
			return err
		}
		if err := conversationChanged(ctx, tx, row.WorkspaceID, c); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes a task. Its conversations stop waiting for it first.
func (s *Service) Delete(ctx context.Context, workspaceID, actorID, taskID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTaskForUpdate(ctx, store.GetTaskForUpdateParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		convs, err := q.TaskConversationIDs(ctx, store.TaskConversationIDsParams{WorkspaceID: workspaceID, TaskID: taskID})
		if err != nil {
			return err
		}
		for _, c := range convs {
			if err := s.unlink(ctx, tx, q, row, c, actorID); err != nil {
				return err
			}
		}
		if err := q.DeleteTask(ctx, store.DeleteTaskParams{WorkspaceID: workspaceID, ID: taskID}); err != nil {
			return err
		}
		return changed(ctx, tx, row, &actorID, false)
	})
	if err != nil {
		return dbError(err, "delete task")
	}
	return nil
}

// Activity is a line of a task's feed: a comment or a change.
type Activity struct {
	ID      uuid.UUID
	ActorID *uuid.UUID
	// Kind is comment, created, status, assignee, queue, priority, topic,
	// edited, linked, unlinked or files.
	Kind string
	// Body is the text of a comment.
	Body string
	// From and To are the values a change moved between: statuses,
	// priorities, member, queue or topic ids ("" for none).
	From string
	To   string
	// Fields are what an edit changed: title, body.
	Fields []string
	// ConversationID is set on linked and unlinked.
	ConversationID *uuid.UUID
	// Files of a comment.
	Files []File
	// FilesAdded and FilesRemoved count the files a "files" line added to
	// the description or took out of it.
	FilesAdded   int
	FilesRemoved int
	CreatedAt    time.Time
}

// Activity returns the feed of a task, oldest first.
func (s *Service) Activity(ctx context.Context, workspaceID, taskID uuid.UUID) ([]Activity, error) {
	out := []Activity{}
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.GetTask(ctx, store.GetTaskParams{WorkspaceID: workspaceID, ID: taskID}); err != nil {
			return err
		}
		rows, err := q.ListActivity(ctx, store.ListActivityParams{WorkspaceID: workspaceID, TaskID: taskID})
		if err != nil {
			return err
		}
		_, byComment, err := taskFiles(ctx, q, workspaceID, taskID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			a := activityFrom(r)
			if a.Kind == "comment" {
				a.Files = byComment[a.ID]
				if a.Files == nil {
					a.Files = []File{}
				}
			}
			out = append(out, a)
		}
		return nil
	})
	if err != nil {
		return nil, dbError(err, "task activity")
	}
	return out, nil
}

// Comment adds a comment to a task's feed, with the actor's uploaded
// files. A comment of files only may have no text.
func (s *Service) Comment(ctx context.Context, workspaceID, actorID, taskID uuid.UUID, text string, fileIDs []uuid.UUID) (Activity, error) {
	body := strings.TrimSpace(text)
	if (body == "" && len(fileIDs) == 0) || utf8.RuneCountInString(body) > MaxComment || !validText(body) {
		return Activity{}, fmt.Errorf("%w: comment must be 1 to %d characters", ErrInvalid, MaxComment)
	}
	if err := s.checkFiles(ctx, workspaceID, actorID, fileIDs); err != nil {
		return Activity{}, err
	}
	var out Activity
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetTask(ctx, store.GetTaskParams{WorkspaceID: workspaceID, ID: taskID})
		if err != nil {
			return err
		}
		id, err := s.addActivity(ctx, q, row, &actorID, "comment", &body, nil)
		if err != nil {
			return err
		}
		if err := s.attachFiles(ctx, q, row, actorID, &id, fileIDs); err != nil {
			return err
		}
		out = Activity{ID: id, ActorID: &actorID, Kind: "comment", Body: body, CreatedAt: s.now(), Files: []File{}}
		if len(fileIDs) > 0 {
			_, byComment, err := taskFiles(ctx, q, workspaceID, taskID)
			if err != nil {
				return err
			}
			out.Files = byComment[id]
		}
		return changed(ctx, tx, row, &actorID, false)
	})
	if err != nil {
		return Activity{}, dbError(err, "comment task")
	}
	return out, nil
}

// addActivity writes a feed line and returns its id.
func (s *Service) addActivity(
	ctx context.Context, q *store.Queries, row store.Task, actorID *uuid.UUID, kind string, body *string, meta map[string]any,
) (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	raw := []byte("{}")
	if meta != nil {
		if raw, err = json.Marshal(meta); err != nil {
			return uuid.Nil, fmt.Errorf("encode activity: %w", err)
		}
	}
	return id, q.InsertActivity(ctx, store.InsertActivityParams{
		ID: id, WorkspaceID: row.WorkspaceID, TaskID: row.ID, ActorID: actorID, Kind: kind, Body: body, Meta: raw,
		CreatedAt: s.now(),
	})
}

// notifyNew tells members about a new task: its assignee, or the members
// of its queue when it has none. The creator is never told about their own
// task.
func (s *Service) notifyNew(ctx context.Context, tx pgx.Tx, q *store.Queries, row store.Task, actorID uuid.UUID) error {
	if row.AssigneeID != nil {
		if *row.AssigneeID == actorID {
			return nil
		}
		return s.notify(ctx, tx, row, NoticeAssigned, []uuid.UUID{*row.AssigneeID})
	}
	if row.QueueID == nil {
		return nil
	}
	ids, err := q.QueueMemberIDs(ctx, store.QueueMemberIDsParams{WorkspaceID: row.WorkspaceID, QueueID: *row.QueueID})
	if err != nil {
		return err
	}
	users := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != actorID {
			users = append(users, id)
		}
	}
	return s.notify(ctx, tx, row, NoticeQueued, users)
}

func (s *Service) notify(ctx context.Context, tx pgx.Tx, row store.Task, kind string, users []uuid.UUID) error {
	if s.opts.Notifier == nil || len(users) == 0 {
		return nil
	}
	return s.opts.Notifier.TaskNotify(ctx, tx, Notice{WorkspaceID: row.WorkspaceID, TaskID: row.ID, Kind: kind, UserIDs: users})
}

// changed tells clients to fetch the task and the list again. assigned:
// this change gave the task to its assignee, so their panel may tell them.
// actor is nil for the AI bot.
func changed(ctx context.Context, tx pgx.Tx, row store.Task, actor *uuid.UUID, assigned bool) error {
	return outbox.Add(ctx, tx, row.WorkspaceID, "task.changed", map[string]any{
		"task_id": row.ID, "assignee_id": row.AssigneeID, "actor_id": actor, "assigned": assigned,
	})
}

// conversationChanged tells clients to fetch the conversation's tasks again.
func conversationChanged(ctx context.Context, tx pgx.Tx, workspaceID, conversationID uuid.UUID) error {
	return outbox.Add(ctx, tx, workspaceID, "conversation.tasks_changed", map[string]any{"conversation_id": conversationID})
}

// enabled refuses new tasks while the section is turned off.
func (s *Service) enabled(ctx context.Context, q *store.Queries, workspaceID uuid.UUID) error {
	row, err := q.GetSettings(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !row.Enabled {
		return ErrDisabled
	}
	return nil
}

func taskFrom(r store.Task) Task {
	return Task{
		ID: r.ID, Number: r.Number, QueueID: r.QueueID, TopicID: r.TopicID, Title: r.Title, Body: r.Body, Status: r.Status, Priority: r.Priority,
		AssigneeID: r.AssigneeID, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ClosedAt: r.ClosedAt,
		Version: r.Version,
	}
}

func activityFrom(r store.TaskActivity) Activity {
	out := Activity{ID: r.ID, ActorID: r.ActorID, Kind: r.Kind, CreatedAt: r.CreatedAt}
	if r.Body != nil {
		out.Body = *r.Body
	}
	var meta struct {
		From           *string    `json:"from"`
		To             *string    `json:"to"`
		Fields         []string   `json:"fields"`
		ConversationID *uuid.UUID `json:"conversation_id"`
		Added          int        `json:"added"`
		Removed        int        `json:"removed"`
	}
	if json.Unmarshal(r.Meta, &meta) == nil {
		out.FilesAdded, out.FilesRemoved = meta.Added, meta.Removed
		if meta.From != nil {
			out.From = *meta.From
		}
		if meta.To != nil {
			out.To = *meta.To
		}
		out.Fields, out.ConversationID = meta.Fields, meta.ConversationID
	}
	return out
}

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// likeEscape escapes the wildcards of ILIKE in a search query.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
