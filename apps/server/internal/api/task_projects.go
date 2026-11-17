package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// Projects of tasks (ADR-0022).

// projectScope answers 404 for a task the caller does not see, before any
// route under /tasks/{id} runs: the handlers then need no check of their
// own, and a hidden task looks like a missing one.
func (s *server) projectScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pattern := chi.RouteContext(r.Context()).RoutePattern()
		p, ok := authz.PrincipalFrom(r.Context())
		if !ok || !strings.HasPrefix(pattern, "/api/v1/tasks/{id}") || !p.Can(authz.TasksView) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			next.ServeHTTP(w, r) // the handler answers the bad id
			return
		}
		if err := s.tasks.Visible(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id); err != nil {
			s.writeError(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListTaskProjects implements GET /task-projects.
func (s *server) ListTaskProjects(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	ps, err := s.tasks.Projects(r.Context(), p.WorkspaceID, tasks.ViewerOf(p))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.TaskProjectList{Items: make([]oapi.TaskProject, 0, len(ps))}
	for _, x := range ps {
		out.Items = append(out.Items, projectBody(x))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateTaskProject implements POST /task-projects.
func (s *server) CreateTaskProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskProjectInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	got, err := s.tasks.CreateProject(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), projectInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectBody(got))
}

// UpdateTaskProject implements PUT /task-projects/{id}.
func (s *server) UpdateTaskProject(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TaskProjectInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	got, err := s.tasks.UpdateProject(r.Context(), p.WorkspaceID, id, tasks.ViewerOf(p), projectInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, projectBody(got))
}

// DeleteTaskProject implements DELETE /task-projects/{id}.
func (s *server) DeleteTaskProject(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	if err := s.tasks.DeleteProject(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func projectInput(req oapi.TaskProjectInput) tasks.ProjectInput {
	in := tasks.ProjectInput{Name: req.Name, Key: deref(req.Key), MemberIDs: req.MemberIds, Archived: deref(req.Archived), Position: deref(req.Position)}
	if req.Icon != nil {
		icon := string(*req.Icon)
		in.Icon = &icon
	}
	if req.Color != nil {
		in.Color = string(*req.Color)
	}
	if req.Access != nil {
		in.Access = string(*req.Access)
	}
	return in
}

func projectBody(x tasks.Project) oapi.TaskProject {
	out := oapi.TaskProject{
		Id: x.ID, Name: x.Name, Key: x.Key, FormerKeys: nonNil(x.FormerKeys), Color: oapi.TagColor(x.Color), Access: oapi.TaskProjectAccess(x.Access), IsDefault: x.IsDefault,
		Position: x.Position, MemberIds: nonNil(x.MemberIDs), OpenTasks: x.OpenTasks, ArchivedAt: x.ArchivedAt, Member: x.Member,
	}
	if x.Icon != nil {
		icon := oapi.TaskProjectIcon(*x.Icon)
		out.Icon = &icon
	}
	return out
}
