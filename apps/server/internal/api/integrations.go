package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// Integrations (GH-01, ADR-0012): the workspace's own GitHub App, created
// from a manifest and installed on its repositories.

// GetGitHubIntegration implements GET /integrations/github.
func (s *server) GetGitHubIntegration(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	locale := workspaceLocale(r)
	cur, err := s.integrations.GitHubIntegration(r.Context(), p.WorkspaceID, locale)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.GitHubIntegrationState{
		Defaults:            templatesBody(integrations.DefaultTemplates(locale)),
		EncryptionAvailable: s.integrations.EncryptionAvailable(),
	}
	out.Placeholders.Issue = integrations.IssuePlaceholders
	out.Placeholders.Reply = integrations.ReplyPlaceholders
	if cur != nil {
		b := githubBody(*cur)
		out.Integration = &b
	}
	writeJSON(w, http.StatusOK, out)
}

// StartGitHubIntegration implements POST /integrations/github/manifest.
func (s *server) StartGitHubIntegration(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.StartGitHubIntegrationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	st, err := s.integrations.StartGitHub(r.Context(), p.WorkspaceID, p.UserID, deref(req.Organization), workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.GitHubManifestStart{Integration: githubBody(st.Integration), PostUrl: st.PostURL, Manifest: st.Manifest})
}

// CompleteGitHubApp implements POST /integrations/github/app.
func (s *server) CompleteGitHubApp(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.CompleteGitHubAppRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	got, err := s.integrations.CompleteGitHubApp(r.Context(), p.WorkspaceID, req.Code, req.State, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, githubBody(got))
}

// CompleteGitHubInstallation implements POST /integrations/github/installation.
func (s *server) CompleteGitHubInstallation(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.CompleteGitHubInstallationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	got, err := s.integrations.CompleteGitHubInstallation(r.Context(), p.WorkspaceID, req.InstallationId, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, githubBody(got))
}

// ListGitHubRepositories implements GET /integrations/github/repositories.
func (s *server) ListGitHubRepositories(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	list, more, err := s.integrations.Repositories(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.GitHubRepository, 0, len(list))
	for _, repo := range list {
		items = append(items, repositoryBody(repo))
	}
	writeJSON(w, http.StatusOK, oapi.GitHubRepositoryList{Items: items, More: more})
}

// UpdateGitHubIntegration implements PATCH /integrations/github.
func (s *server) UpdateGitHubIntegration(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.UpdateGitHubIntegrationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in := integrations.GitHubSettings{Repository: req.Repository, Labels: req.Labels}
	if t := req.Templates; t != nil {
		in.Templates = &integrations.Templates{IssueTitle: t.IssueTitle, IssueBody: t.IssueBody, ReplyRU: t.ReplyRu, ReplyEN: t.ReplyEn}
	}
	got, err := s.integrations.UpdateGitHub(r.Context(), p.WorkspaceID, in, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, githubBody(got))
}

// DisconnectGitHubIntegration implements DELETE /integrations/github.
func (s *server) DisconnectGitHubIntegration(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.IntegrationsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.integrations.DisconnectGitHub(r.Context(), p.WorkspaceID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workspaceLocale is the language of the caller's workspace; it picks the
// built-in issue template.
func workspaceLocale(r *http.Request) string {
	if st, ok := authStateFrom(r.Context()); ok {
		return st.membership.Workspace.Locale
	}
	return ""
}

func githubBody(g integrations.GitHubIntegration) oapi.GitHubIntegration {
	out := oapi.GitHubIntegration{
		Id: g.ID, Status: oapi.GitHubIntegrationStatus(g.Status), Labels: g.Labels,
		Templates: templatesBody(g.Templates), CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
	out.CustomTemplates.IssueTitle = g.Custom.IssueTitle
	out.CustomTemplates.IssueBody = g.Custom.IssueBody
	out.CustomTemplates.ReplyRu = g.Custom.ReplyRU
	out.CustomTemplates.ReplyEn = g.Custom.ReplyEN
	if g.App != nil {
		out.App = &oapi.GitHubApp{Id: g.App.ID, Slug: g.App.Slug, Owner: g.App.Owner, Url: g.App.URL}
	}
	if g.InstallURL != "" {
		out.InstallUrl = &g.InstallURL
	}
	if g.Account != "" {
		out.Account = &g.Account
	}
	if g.Repository != nil {
		repo := repositoryBody(*g.Repository)
		out.Repository = &repo
	}
	return out
}

func repositoryBody(r integrations.Repository) oapi.GitHubRepository {
	return oapi.GitHubRepository{Id: r.ID, FullName: r.FullName, Private: r.Private, Url: r.URL}
}

func templatesBody(t integrations.Templates) oapi.GitHubTemplates {
	return oapi.GitHubTemplates{IssueTitle: t.IssueTitle, IssueBody: t.IssueBody, ReplyRu: t.ReplyRU, ReplyEn: t.ReplyEN}
}

// integrationProblems are the integrations errors with their answers.
var integrationProblems = []struct {
	err  error
	prob Problem
}{
	{integrations.ErrAlreadyConnected, Problem{
		Status: http.StatusConflict, Code: "github_connected", Title: "Conflict", Detail: "GitHub is already connected: disconnect it first",
	}},
	{integrations.ErrStep, Problem{
		Status: http.StatusConflict, Code: "github_step", Title: "Conflict", Detail: "the connection is not waiting for this step: reload it",
	}},
	{integrations.ErrState, Problem{
		Status: http.StatusConflict, Code: "github_state", Title: "Conflict",
		Detail: "the GitHub redirect belongs to another connection or is older than an hour: start again",
	}},
	{integrations.ErrCodeExpired, Problem{
		Status: http.StatusUnprocessableEntity, Code: "github_code_expired", Title: "Unprocessable Content",
		Detail: "the code from GitHub was used or is older than an hour: start again",
	}},
	{integrations.ErrInstallation, Problem{
		Status: http.StatusUnprocessableEntity, Code: "github_installation_invalid", Title: "Unprocessable Content",
		Detail: "not an installation of this workspace's GitHub App",
	}},
	{integrations.ErrRepository, Problem{
		Status: http.StatusUnprocessableEntity, Code: "github_repository_unreachable", Title: "Unprocessable Content",
		Detail: "the GitHub App is not installed on this repository",
	}},
	{integrations.ErrGitHubAuth, Problem{
		Status: http.StatusUnprocessableEntity, Code: "github_app_refused", Title: "Unprocessable Content",
		Detail: "GitHub refused the App: it was deleted or its key revoked; disconnect and connect again",
	}},
	{integrations.ErrNotConnected, Problem{
		Status: http.StatusConflict, Code: "github_not_connected", Title: "Conflict",
		Detail: "GitHub is not connected or has no repository picked",
	}},
	{integrations.ErrIssueInProgress, Problem{
		Status: http.StatusConflict, Code: "issue_in_progress", Title: "Conflict", Detail: "an issue of this conversation is being created",
	}},
	{integrations.ErrIssueLinked, Problem{
		Status: http.StatusConflict, Code: "issue_linked", Title: "Conflict", Detail: "the conversation is linked to this issue already",
	}},
	{integrations.ErrIssueState, Problem{
		Status: http.StatusConflict, Code: "issue_state", Title: "Conflict", Detail: "only a failed issue can be sent again or removed",
	}},
	{integrations.ErrIssueNotFound, Problem{
		Status: http.StatusUnprocessableEntity, Code: "issue_not_found", Title: "Unprocessable Content",
		Detail: "the repository has no issue with this number, or it is a pull request",
	}},
	{integrations.ErrNoEncryption, Problem{
		Status: http.StatusServiceUnavailable, Code: "encryption_unavailable", Title: "Service Unavailable",
		Detail: "TOLKOVO_MASTER_KEY is not set on this server",
	}},
}

// writeIntegrationError answers the errors of the integrations module and
// reports whether err was one. GitHub outages are logged with their cause,
// which never holds keys or tokens.
func (s *server) writeIntegrationError(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, c := range integrationProblems {
		if errors.Is(err, c.err) {
			writeProblem(w, r, c.prob)
			return true
		}
	}
	if errors.Is(err, integrations.ErrGitHub) {
		s.log.WarnContext(r.Context(), "github request failed",
			slog.String("request_id", middleware.GetReqID(r.Context())), slog.String("error", err.Error()))
		writeProblem(w, r, Problem{Status: http.StatusBadGateway, Code: "github_unavailable", Title: "Bad Gateway"})
		return true
	}
	return false
}
