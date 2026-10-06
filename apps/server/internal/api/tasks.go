package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

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
	if errors.Is(err, tasks.ErrInvalid) {
		writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: detail(err)})
		return true
	}
	return false
}

// conversationForTasks checks perm and that the caller sees the conversation.
func (s *server) conversationForTasks(w http.ResponseWriter, r *http.Request, id uuid.UUID, perm authz.Permission) (authz.Principal, bool) {
	p, ok := s.requirePrincipal(w, r, perm)
	if !ok {
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
	f := tasks.Filter{Query: deref(params.Q), Limit: deref(params.Limit)}
	if params.State != nil {
		f.State = string(*params.State)
	}
	if params.Status != nil {
		f.Status = string(*params.Status)
	}
	switch a := deref(params.Assignee); a {
	case "":
	case "me":
		f.AssigneeID = &p.UserID
	case "none":
		f.Unassigned = true
	default:
		id, err := uuid.Parse(a)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: assignee must be me, none or a member id", errBadRequest))
			return
		}
		f.AssigneeID = &id
	}
	switch q := deref(params.Queue); q {
	case "":
	case "none":
		f.NoQueue = true
	default:
		id, err := uuid.Parse(q)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: queue must be none or a queue id", errBadRequest))
			return
		}
		f.QueueID = &id
	}
	if c := deref(params.Cursor); c != "" {
		n, err := strconv.ParseInt(c, 10, 64)
		if err != nil || n <= 0 {
			s.writeError(w, r, fmt.Errorf("%w: cursor is not one of this list", errBadRequest))
			return
		}
		f.Before = n
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
	writeJSON(w, http.StatusOK, out)
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
		Title: req.Title, Body: deref(req.Body), QueueID: req.QueueId, AssigneeID: req.AssigneeId, ConversationID: req.ConversationId,
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
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.activeMember(w, r, p, req.AssigneeId) {
		return
	}
	t, err := s.tasks.Update(r.Context(), p.WorkspaceID, p.UserID, id, tasks.Update{
		Version: req.Version, Title: req.Title, Body: req.Body, Status: string(req.Status), Priority: string(req.Priority),
		QueueID: req.QueueId, AssigneeID: req.AssigneeId,
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
	a, err := s.tasks.Comment(r.Context(), p.WorkspaceID, p.UserID, id, req.Body)
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
	if userID == nil {
		return true
	}
	_, err := s.tenancy.Membership(r.Context(), p.WorkspaceID, *userID)
	if errors.Is(err, tenancy.ErrNotFound) {
		err = tasks.ErrMember
	}
	if err != nil {
		s.writeError(w, r, err)
		return false
	}
	return true
}

func taskBody(t tasks.Task) oapi.Task {
	return oapi.Task{
		Id: t.ID, Number: t.Number, QueueId: t.QueueID, Title: t.Title, Body: t.Body, Status: oapi.TaskStatus(t.Status),
		Priority: oapi.TaskPriority(t.Priority), AssigneeId: t.AssigneeID, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt, ClosedAt: t.ClosedAt, Version: t.Version, ConversationIds: nonNil(t.ConversationIDs),
	}
}

func activityBody(a tasks.Activity) oapi.TaskActivity {
	out := oapi.TaskActivity{Id: a.ID, ActorId: a.ActorID, Kind: oapi.TaskActivityKind(a.Kind), CreatedAt: a.CreatedAt, ConversationId: a.ConversationID}
	if a.Body != "" {
		out.Body = &a.Body
	}
	if a.Kind != "comment" && a.Kind != "created" && a.Kind != "edited" && a.Kind != "linked" && a.Kind != "unlinked" {
		out.From, out.To = &a.From, &a.To
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
