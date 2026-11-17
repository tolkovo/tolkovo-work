package api

import (
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// MoveTask implements POST /tasks/{id}/move (ADR-0022, P4). projectScope
// has checked that the caller sees the task.
func (s *server) MoveTask(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskMoveInput
	// A missing queue or assignee must not clear them unasked.
	if err := decodeRequired(w, r, &req, "queue_id", "assignee_id"); err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.activeMember(w, r, p, req.AssigneeId) {
		return
	}
	v := tasks.ViewerOf(p)
	t, err := s.tasks.Move(r.Context(), p.WorkspaceID, v, id, tasks.Move{
		ProjectID: req.ProjectId, QueueID: req.QueueId, AssigneeID: req.AssigneeId, AddAssignee: deref(req.AddAssigneeToProject),
		Version: req.Version,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskBody(t))
}
