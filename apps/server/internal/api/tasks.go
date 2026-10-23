package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// The tasks section (ADR-0015, docs/02 §12.5).

// taskProblems are the errors of the tasks module with their answers.
var taskProblems = []struct {
	err  error
	prob Problem
}{
	{tasks.ErrNotFound, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"}},
	{tasks.ErrConflict, Problem{
		Status: http.StatusConflict, Code: "task_conflict", Title: "Conflict", Detail: "the task has changed, reload it",
	}},
	{tasks.ErrLinked, Problem{Status: http.StatusConflict, Code: "task_linked", Title: "Conflict"}},
	{tasks.ErrQueueTaken, Problem{Status: http.StatusConflict, Code: "queue_exists", Title: "Conflict"}},
	{tasks.ErrTagTaken, Problem{Status: http.StatusConflict, Code: "tag_exists", Title: "Conflict"}},
	{tasks.ErrDisabled, Problem{
		Status: http.StatusConflict, Code: "tasks_disabled", Title: "Conflict", Detail: "the tasks section is turned off",
	}},
	{tasks.ErrMember, Problem{
		Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: "not a member of the workspace",
	}},
}

// writeTaskError answers the errors of the tasks module; false for others.
func (s *server) writeTaskError(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, c := range taskProblems {
		if errors.Is(err, c.err) {
			writeProblem(w, r, c.prob)
			return true
		}
	}
	var rejected *tasks.FileError
	if errors.As(err, &rejected) {
		writeProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Code: "file_rejected", Title: "Unprocessable Content",
			Detail: rejected.FileName + ": " + rejected.Reason,
		})
		return true
	}
	if errors.Is(err, tasks.ErrScanUnavailable) {
		writeProblem(w, r, Problem{
			Status: http.StatusServiceUnavailable, Code: "scan_unavailable", Title: "Service Unavailable",
			Detail: "the antivirus did not answer; send the file again later",
		})
		return true
	}
	if errors.Is(err, tasks.ErrInvalid) {
		writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: detail(err)})
		return true
	}
	return false
}

// conversationForTasks checks perm and that the caller sees the
// conversation (seesConversation). A personal access token, which has no
// conversation scope, is refused here.
func (s *server) conversationForTasks(w http.ResponseWriter, r *http.Request, id uuid.UUID, perm authz.Permission) (authz.Principal, bool) {
	p, ok := s.requirePrincipal(w, r, perm)
	if !ok {
		return p, false
	}
	if err := s.seesConversation(r.Context(), p, id); err != nil {
		s.writeError(w, r, err)
		return p, false
	}
	if _, err := s.conversations.Conversation(r.Context(), p.WorkspaceID, p.UserID, id); err != nil {
		s.writeError(w, r, err)
		return p, false
	}
	return p, true
}

// ListConversationTasks implements GET /conversations/{id}/tasks.
func (s *server) ListConversationTasks(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.conversationForTasks(w, r, id, authz.TasksView)
	if !ok {
		return
	}
	got, err := s.tasks.ConversationTasks(r.Context(), p.WorkspaceID, id, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.ConversationTasks{Items: make([]oapi.Task, 0, len(got.Items))}
	for _, t := range got.Items {
		out.Items = append(out.Items, taskBody(t))
	}
	out.Settings.Enabled, out.Settings.ConversationButton = got.Settings.Enabled, oapi.TaskButton(got.Settings.ConversationButton)
	if d := got.ReplyDraft; d != nil {
		out.ReplyDraft = &oapi.TaskReplyDraft{TaskId: d.TaskID, Number: d.Number, Text: d.Text}
	}
	writeJSON(w, http.StatusOK, out)
}

// GetConversationTaskDraft implements GET /conversations/{id}/tasks/draft.
func (s *server) GetConversationTaskDraft(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.conversationForTasks(w, r, id, authz.TasksEdit)
	if !ok {
		return
	}
	d, err := s.tasks.Draft(r.Context(), p.WorkspaceID, id, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TaskDraft{Title: d.Title, Body: d.Body, DraftedBy: oapi.TaskDraftDraftedBy(d.DraftedBy)}
	if d.AIFallback != "" {
		f := oapi.TaskDraftAiFallback(d.AIFallback)
		out.AiFallback = &f
	}
	writeJSON(w, http.StatusOK, out)
}

// ListTasks implements GET /tasks.
func (s *server) ListTasks(w http.ResponseWriter, r *http.Request, params oapi.ListTasksParams) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	sc, err := parseTaskScope(p.UserID, deref(params.Assignee), deref(params.Queue), deref(params.Topic))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	f := tasks.Filter{
		Query: deref(params.Q), Limit: deref(params.Limit), AssigneeID: sc.assigneeID, Unassigned: sc.unassigned,
		QueueID: sc.queueID, NoQueue: sc.noQueue, TopicID: sc.topicID, NoTopic: sc.noTopic, TagID: params.Tag,
		Flow: deref(params.Sort) == oapi.Flow, ClosedDays: deref(params.ClosedDays), Blocked: params.Blocked,
	}
	if params.State != nil {
		f.State = string(*params.State)
	}
	if params.Status != nil {
		f.Status = string(*params.Status)
	}
	if c := deref(params.Cursor); c != "" {
		if f.Flow {
			f.After, err = parseFlowCursor(c)
		} else {
			f.Before, err = strconv.ParseInt(c, 10, 64)
			if err == nil && f.Before <= 0 {
				err = errors.New("not positive")
			}
		}
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: cursor is not one of this list", errBadRequest))
			return
		}
	}
	page, err := s.tasks.List(r.Context(), p.WorkspaceID, f)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TaskPage{Items: make([]oapi.Task, 0, len(page.Items))}
	for _, t := range page.Items {
		out.Items = append(out.Items, taskBody(t))
	}
	if page.Next > 0 {
		c := strconv.FormatInt(page.Next, 10)
		out.NextCursor = &c
	}
	if page.NextKey != nil {
		c := flowCursor(*page.NextKey)
		out.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, out)
}

// GetTaskBoard implements GET /tasks/board.
func (s *server) GetTaskBoard(w http.ResponseWriter, r *http.Request, params oapi.GetTaskBoardParams) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	sc, err := parseTaskScope(p.UserID, deref(params.Assignee), deref(params.Queue), deref(params.Topic))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	cols, err := s.tasks.Board(r.Context(), p.WorkspaceID, tasks.BoardFilter{
		AssigneeID: sc.assigneeID, Unassigned: sc.unassigned, QueueID: sc.queueID, NoQueue: sc.noQueue, TopicID: sc.topicID,
		NoTopic: sc.noTopic, TagID: params.Tag, Query: deref(params.Q), ClosedDays: deref(params.ClosedDays), Canceled: deref(params.Canceled),
		Blocked: params.Blocked,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TaskBoard{Columns: make([]oapi.TaskBoardColumn, 0, len(cols))}
	for _, c := range cols {
		col := oapi.TaskBoardColumn{
			Status: oapi.TaskStatus(c.Status), Count: c.Count, Items: make([]oapi.Task, 0, len(c.Items)), WipLimit: c.WIPLimit, AgeDays: c.AgeDays,
		}
		for _, t := range c.Items {
			col.Items = append(col.Items, taskBody(t))
		}
		if c.Next != nil {
			cur := flowCursor(*c.Next)
			col.NextCursor = &cur
		}
		out.Columns = append(out.Columns, col)
	}
	writeJSON(w, http.StatusOK, out)
}

// GetTaskBoardSettings implements GET /task-board-settings.
func (s *server) GetTaskBoardSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	rules, err := s.tasks.ColumnRules(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, boardSettingsBody(rules))
}

// UpdateTaskBoardSettings implements PUT /task-board-settings.
func (s *server) UpdateTaskBoardSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskBoardSettings
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in := make([]tasks.ColumnRule, 0, len(req.Rules))
	for _, x := range req.Rules {
		in = append(in, tasks.ColumnRule{QueueID: x.QueueId, Status: string(x.Status), WIPLimit: x.WipLimit, AgeDays: x.AgeDays})
	}
	rules, err := s.tasks.SetColumnRules(r.Context(), p.WorkspaceID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, boardSettingsBody(rules))
}

func boardSettingsBody(rules []tasks.ColumnRule) oapi.TaskBoardSettings {
	out := oapi.TaskBoardSettings{Rules: make([]oapi.TaskColumnRule, 0, len(rules))}
	for _, x := range rules {
		out.Rules = append(out.Rules, oapi.TaskColumnRule{QueueId: x.QueueID, Status: oapi.TaskColumnRuleStatus(x.Status), WipLimit: x.WIPLimit, AgeDays: x.AgeDays})
	}
	return out
}

// taskScope is what the list and the board filter by besides tags and text.
type taskScope struct {
	assigneeID *uuid.UUID
	unassigned bool
	queueID    *uuid.UUID
	noQueue    bool
	topicID    *uuid.UUID
	noTopic    bool
}

func parseTaskScope(me uuid.UUID, assignee, queue, topic string) (taskScope, error) {
	var sc taskScope
	switch assignee {
	case "":
	case "me":
		sc.assigneeID = &me
	case "none":
		sc.unassigned = true
	default:
		id, err := uuid.Parse(assignee)
		if err != nil {
			return sc, fmt.Errorf("%w: assignee must be me, none or a member id", errBadRequest)
		}
		sc.assigneeID = &id
	}
	switch queue {
	case "":
	case "none":
		sc.noQueue = true
	default:
		id, err := uuid.Parse(queue)
		if err != nil {
			return sc, fmt.Errorf("%w: queue must be none or a queue id", errBadRequest)
		}
		sc.queueID = &id
	}
	byTopic, id, err := topicFilter(topic)
	if err != nil {
		return sc, err
	}
	sc.topicID, sc.noTopic = id, byTopic && id == nil
	return sc, nil
}

// flowCursor writes the place of a task in its board column as
// "f<rank>.<status changed, µs>.<number>"; parseFlowCursor reads it back.
func flowCursor(k tasks.FlowKey) string {
	return fmt.Sprintf("f%d.%d.%d", k.Rank, k.Changed.UnixMicro(), k.Number)
}

func parseFlowCursor(c string) (*tasks.FlowKey, error) {
	var rank int32
	var micros, number int64
	if n, err := fmt.Sscanf(c, "f%d.%d.%d", &rank, &micros, &number); err != nil || n != 3 || number <= 0 || rank < 0 || rank > 3 {
		return nil, errors.New("not a cursor of the board")
	}
	if c != fmt.Sprintf("f%d.%d.%d", rank, micros, number) {
		return nil, errors.New("not a cursor of the board")
	}
	return &tasks.FlowKey{Rank: rank, Changed: time.UnixMicro(micros).UTC(), Number: number}, nil
}

// CreateTask implements POST /tasks.
func (s *server) CreateTask(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskCreate
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if req.ConversationId != nil {
		if _, ok := s.conversationForTasks(w, r, *req.ConversationId, authz.TasksEdit); !ok {
			return
		}
	}
	if !s.activeMember(w, r, p, req.AssigneeId) {
		return
	}
	in := tasks.NewTask{
		Title: req.Title, Body: deref(req.Body), QueueID: req.QueueId, TopicID: req.TopicId, AssigneeID: req.AssigneeId,
		ConversationID: req.ConversationId, FileIDs: deref(req.FileIds), TagIDs: deref(req.TagIds),
	}
	if req.Priority != nil {
		in.Priority = string(*req.Priority)
	}
	t, err := s.tasks.Create(r.Context(), p.WorkspaceID, p.UserID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, taskBody(t))
}

// GetTaskSummary implements GET /tasks/summary.
func (s *server) GetTaskSummary(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	n, err := s.tasks.OpenAssigned(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.TaskSummary{AssignedOpen: n})
}

// GetTask implements GET /tasks/{id}.
func (s *server) GetTask(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	t, err := s.tasks.Get(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskBody(t))
}

// UpdateTask implements PUT /tasks/{id}.
func (s *server) UpdateTask(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskUpdate
	// A missing topic_id must not clear the topic of a client that does not know it.
	if err := decodeRequired(w, r, &req, "topic_id"); err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.activeMember(w, r, p, req.AssigneeId) {
		return
	}
	t, err := s.tasks.Update(r.Context(), p.WorkspaceID, p.UserID, id, tasks.Update{
		Version: req.Version, Title: req.Title, Body: req.Body, Status: string(req.Status), Priority: string(req.Priority),
		QueueID: req.QueueId, TopicID: req.TopicId, AssigneeID: req.AssigneeId, TagIDs: req.TagIds, BlockedReason: req.BlockedReason,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskBody(t))
}

// DeleteTask implements DELETE /tasks/{id}.
func (s *server) DeleteTask(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	if err := s.tasks.Delete(r.Context(), p.WorkspaceID, p.UserID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTaskActivity implements GET /tasks/{id}/activity.
func (s *server) ListTaskActivity(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	items, err := s.tasks.Activity(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TaskActivityList{Items: make([]oapi.TaskActivity, 0, len(items))}
	for _, a := range items {
		out.Items = append(out.Items, activityBody(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// CommentTask implements POST /tasks/{id}/activity.
func (s *server) CommentTask(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskComment
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	a, err := s.tasks.Comment(r.Context(), p.WorkspaceID, p.UserID, id, req.Body, deref(req.FileIds))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, activityBody(a))
}

// LinkTaskConversation implements POST /tasks/{id}/conversations.
func (s *server) LinkTaskConversation(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	var req oapi.TaskLinkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	p, ok := s.conversationForTasks(w, r, req.ConversationId, authz.TasksEdit)
	if !ok {
		return
	}
	t, err := s.tasks.Link(r.Context(), p.WorkspaceID, p.UserID, id, req.ConversationId)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, taskBody(t))
}

// UnlinkTaskConversation implements DELETE /tasks/{id}/conversations/{conversation_id}.
func (s *server) UnlinkTaskConversation(w http.ResponseWriter, r *http.Request, id oapi.Id, conversationID uuid.UUID) {
	p, ok := s.conversationForTasks(w, r, conversationID, authz.TasksEdit)
	if !ok {
		return
	}
	if err := s.tasks.Unlink(r.Context(), p.WorkspaceID, p.UserID, id, conversationID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTaskQueues implements GET /task-queues.
func (s *server) ListTaskQueues(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	qs, err := s.tasks.Queues(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TaskQueueList{Items: make([]oapi.TaskQueue, 0, len(qs))}
	for _, q := range qs {
		out.Items = append(out.Items, queueBody(q))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateTaskQueue implements POST /task-queues.
func (s *server) CreateTaskQueue(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskQueueInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	q, err := s.tasks.CreateQueue(r.Context(), p.WorkspaceID, tasks.QueueInput{Name: req.Name, MemberIDs: req.MemberIds})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, queueBody(q))
}

// UpdateTaskQueue implements PUT /task-queues/{id}.
func (s *server) UpdateTaskQueue(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskQueueInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	q, err := s.tasks.UpdateQueue(r.Context(), p.WorkspaceID, id, tasks.QueueInput{Name: req.Name, MemberIDs: req.MemberIds})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, queueBody(q))
}

// DeleteTaskQueue implements DELETE /task-queues/{id}.
func (s *server) DeleteTaskQueue(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	if err := s.tasks.DeleteQueue(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTaskTags implements GET /task-tags.
func (s *server) ListTaskTags(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	tags, err := s.tasks.Tags(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TagList{Items: make([]oapi.Tag, 0, len(tags))}
	for _, t := range tags {
		out.Items = append(out.Items, taskTagBody(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateTaskTag implements POST /task-tags: any member tags tasks.
func (s *server) CreateTaskTag(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskTagInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.tasks.CreateTag(r.Context(), p.WorkspaceID, p.UserID, taskTagInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, taskTagBody(t))
}

// UpdateTaskTag implements PUT /task-tags/{id}.
func (s *server) UpdateTaskTag(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskTagInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.tasks.UpdateTag(r.Context(), p.WorkspaceID, id, taskTagInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskTagBody(t))
}

// DeleteTaskTag implements DELETE /task-tags/{id}.
func (s *server) DeleteTaskTag(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	if err := s.tasks.DeleteTag(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetTaskSettings implements GET /task-settings.
func (s *server) GetTaskSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	set, err := s.tasks.Settings(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskSettingsBody(set))
}

// UpdateTaskSettings implements PUT /task-settings.
func (s *server) UpdateTaskSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskSettings
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	set, err := s.tasks.SaveSettings(r.Context(), p.WorkspaceID, tasks.Settings{
		Enabled: req.Enabled, ConversationButton: string(req.ConversationButton), ReplyRU: req.ReplyRu, ReplyEN: req.ReplyEn,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskSettingsBody(set))
}

// activeMember refuses an assignee who is not an active member of the
// caller's workspace; nil passes.
func (s *server) activeMember(w http.ResponseWriter, r *http.Request, p authz.Principal, userID *uuid.UUID) bool {
	if err := s.checkMember(r.Context(), p.WorkspaceID, userID); err != nil {
		s.writeError(w, r, err)
		return false
	}
	return true
}

// checkMember is nil for no member or an active one, tasks.ErrMember for
// anyone else.
func (s *server) checkMember(ctx context.Context, workspaceID uuid.UUID, userID *uuid.UUID) error {
	if userID == nil {
		return nil
	}
	_, err := s.tenancy.Membership(ctx, workspaceID, *userID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return tasks.ErrMember
	}
	return err
}

func taskBody(t tasks.Task) oapi.Task {
	return oapi.Task{
		Id: t.ID, Number: t.Number, QueueId: t.QueueID, TopicId: t.TopicID, Title: t.Title, Body: t.Body, Status: oapi.TaskStatus(t.Status),
		Priority: oapi.TaskPriority(t.Priority), AssigneeId: t.AssigneeID, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt, ClosedAt: t.ClosedAt, StatusChangedAt: t.StatusChangedAt, BlockedReason: t.BlockedReason, Version: t.Version,
		ConversationIds: nonNil(t.ConversationIDs), ConversationCount: t.ConversationCount, Files: taskFileList(t.Files), TagIds: nonNil(t.TagIDs),
	}
}

func taskTagBody(t tasks.Tag) oapi.Tag {
	return oapi.Tag{Id: t.ID, Name: t.Name, Color: oapi.TagColor(t.Color)}
}

func taskTagInput(req oapi.TaskTagInput) tasks.TagInput {
	in := tasks.TagInput{Name: req.Name}
	if req.Color != nil {
		in.Color = string(*req.Color)
	}
	return in
}

func activityBody(a tasks.Activity) oapi.TaskActivity {
	out := oapi.TaskActivity{Id: a.ID, ActorId: a.ActorID, Kind: oapi.TaskActivityKind(a.Kind), CreatedAt: a.CreatedAt, ConversationId: a.ConversationID}
	if a.Body != "" {
		out.Body = &a.Body
	}
	if a.Kind != "comment" && a.Kind != "created" && a.Kind != "edited" && a.Kind != "linked" && a.Kind != "unlinked" && a.Kind != "files" &&
		a.Kind != "tags" && a.Kind != "blocked" && a.Kind != "unblocked" {
		out.From, out.To = &a.From, &a.To
	}
	if a.Kind == "comment" {
		files := taskFileList(a.Files)
		out.Files = &files
	}
	if a.Kind == "files" {
		out.FilesAdded, out.FilesRemoved = &a.FilesAdded, &a.FilesRemoved
	}
	if a.Kind == "tags" {
		added, removed := nonNil(a.TagsAdded), nonNil(a.TagsRemoved)
		out.TagsAdded, out.TagsRemoved = &added, &removed
	}
	if len(a.Fields) > 0 {
		fields := make([]oapi.TaskActivityFields, len(a.Fields))
		for i, f := range a.Fields {
			fields[i] = oapi.TaskActivityFields(f)
		}
		out.Fields = &fields
	}
	return out
}

func queueBody(q tasks.Queue) oapi.TaskQueue {
	return oapi.TaskQueue{Id: q.ID, Name: q.Name, MemberIds: nonNil(q.MemberIDs), OpenTasks: q.OpenTasks}
}

func taskSettingsBody(set tasks.Settings) oapi.TaskSettingsView {
	out := oapi.TaskSettingsView{
		Settings: oapi.TaskSettings{
			Enabled: set.Enabled, ConversationButton: oapi.TaskButton(set.ConversationButton), ReplyRu: set.ReplyRU, ReplyEn: set.ReplyEN,
		},
		Placeholders: tasks.ReplyPlaceholders,
	}
	out.Defaults.ReplyRu, out.Defaults.ReplyEn = tasks.DefaultReplyRU, tasks.DefaultReplyEN
	return out
}
