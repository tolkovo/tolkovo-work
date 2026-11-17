package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// Articles of the knowledge base linked to tasks, and the tasks of a
// contact or an article (ADR-0022, P5). projectScope has checked that the
// caller sees the task of a /tasks/{id} path.

// requireBoth is requirePrincipal for two permissions.
func (s *server) requireBoth(w http.ResponseWriter, r *http.Request, perm, also authz.Permission) (authz.Principal, bool) {
	p, ok := s.requirePrincipal(w, r, perm)
	if ok && !p.Can(also) {
		s.writeError(w, r, authz.ErrForbidden)
		return authz.Principal{}, false
	}
	return p, ok
}

// ListTaskArticles implements GET /tasks/{id}/articles.
func (s *server) ListTaskArticles(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	as, err := s.tasks.Articles(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, articleList(as))
}

// AddTaskArticle implements POST /tasks/{id}/articles.
func (s *server) AddTaskArticle(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requireBoth(w, r, authz.TasksEdit, authz.KnowledgeManage)
	if !ok {
		return
	}
	var req oapi.TaskArticleInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	as, err := s.tasks.AddArticle(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id, req.ArticleId)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, articleList(as))
}

// RemoveTaskArticle implements DELETE /tasks/{id}/articles/{article_id}.
func (s *server) RemoveTaskArticle(w http.ResponseWriter, r *http.Request, id oapi.Id, articleID uuid.UUID) {
	p, ok := s.requireBoth(w, r, authz.TasksEdit, authz.KnowledgeManage)
	if !ok {
		return
	}
	if err := s.tasks.RemoveArticle(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id, articleID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListKBArticleTasks implements GET /kb/articles/{id}/tasks.
func (s *server) ListKBArticleTasks(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requireBoth(w, r, authz.KnowledgeManage, authz.TasksView)
	if !ok {
		return
	}
	b, err := s.tasks.ArticleTasks(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, briefList(b))
}

// ListContactTasks implements GET /contacts/{id}/tasks.
func (s *server) ListContactTasks(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requireBoth(w, r, authz.ContactsView, authz.TasksView)
	if !ok {
		return
	}
	b, err := s.tasks.ContactTasks(r.Context(), p.WorkspaceID, tasks.ViewerOf(p), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, briefList(b))
}

func articleList(as []tasks.Article) oapi.TaskArticleList {
	out := oapi.TaskArticleList{Items: make([]oapi.TaskArticle, 0, len(as))}
	for _, a := range as {
		out.Items = append(out.Items, oapi.TaskArticle{
			Id: a.ID, Kind: oapi.KBArticleKind(a.Kind), Title: a.Title, Published: a.Published, LinkedAt: a.LinkedAt,
		})
	}
	return out
}

func briefList(b tasks.Briefs) oapi.TaskBriefList {
	out := oapi.TaskBriefList{Items: make([]oapi.TaskBrief, 0, len(b.Items)), Hidden: b.Hidden}
	for _, x := range b.Items {
		out.Items = append(out.Items, oapi.TaskBrief{
			Id: x.ID, ProjectId: x.ProjectID, Ref: x.Ref, Title: x.Title, Status: oapi.TaskStatus(x.Status), AssigneeId: x.AssigneeID, UpdatedAt: x.UpdatedAt,
		})
	}
	return out
}
