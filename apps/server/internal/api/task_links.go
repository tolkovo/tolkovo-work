package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// Links between tasks (ADR-0022, P3). projectScope has checked that the
// caller sees the task of the path.

// ListTaskLinks implements GET /tasks/{id}/links.
func (s *server) ListTaskLinks(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	ls, err := s.tasks.Links(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, linkList(ls))
}

// AddTaskLink implements POST /tasks/{id}/links.
func (s *server) AddTaskLink(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskLinkInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	v := tasks.ViewerOf(p)
	other, err := s.taskByRef(r.Context(), p.WorkspaceID, v, req.Task)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	ls, err := s.tasks.AddLink(r.Context(), p.WorkspaceID, v, id, other, string(req.Kind))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, linkList(ls))
}

// RemoveTaskLink implements DELETE /tasks/{id}/links/{other_id}.
func (s *server) RemoveTaskLink(w http.ResponseWriter, r *http.Request, id oapi.Id, otherID uuid.UUID) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	if err := s.tasks.RemoveLink(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id, otherID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CloseTaskAsDuplicate implements POST /tasks/{id}/duplicate.
func (s *server) CloseTaskAsDuplicate(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskDuplicateInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	v := tasks.ViewerOf(p)
	orig, err := s.taskByRef(r.Context(), p.WorkspaceID, v, req.Original)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.tasks.CloseAsDuplicate(r.Context(), p.WorkspaceID, v, id, orig, req.Version)
	if err == nil {
		err = s.tasks.FillWaiting(r.Context(), p.WorkspaceID, v, &t)
	}
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskBody(t))
}

// taskByRef finds a task the viewer sees by its id or ref (KEY-N, #N, N).
func (s *server) taskByRef(ctx context.Context, workspaceID uuid.UUID, v tasks.Viewer, ref string) (uuid.UUID, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return id, s.tasks.Visible(ctx, workspaceID, v, id)
	}
	return s.tasks.IDByRef(ctx, workspaceID, v, ref)
}

func linkList(ls []tasks.Link) oapi.TaskLinkList {
	out := oapi.TaskLinkList{Items: make([]oapi.TaskLink, 0, len(ls))}
	for _, l := range ls {
		item := oapi.TaskLink{
			Kind: oapi.TaskLinkKind(l.Kind), TaskId: l.TaskID, Visible: l.Visible, Status: oapi.TaskStatus(l.Status), CreatedAt: l.CreatedAt,
		}
		if l.Visible {
			item.Ref, item.Title = &l.Ref, &l.Title
		}
		out.Items = append(out.Items, item)
	}
	return out
}

func blockerList(bs []tasks.Blocker) []oapi.TaskBlocker {
	out := make([]oapi.TaskBlocker, 0, len(bs))
	for _, b := range bs {
		item := oapi.TaskBlocker{Id: b.ID}
		if b.Ref != "" {
			item.Ref = &b.Ref
		}
		out = append(out, item)
	}
	return out
}
