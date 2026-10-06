package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// GitHub issues from conversations (GH-02, ADR-0012). Every operation
// needs issues.create and a conversation the caller can see; the issues
// themselves belong to the integrations module.

// conversationForIssues checks the permission and that the conversation is
// visible (a foreign one is 404), and returns the principal.
func (s *server) conversationForIssues(w http.ResponseWriter, r *http.Request, id uuid.UUID) (authz.Principal, bool) {
	p, ok := s.requirePrincipal(w, r, authz.IssuesCreate)
	if !ok {
		return p, false
	}
	if _, err := s.conversations.Conversation(r.Context(), p.WorkspaceID, p.UserID, id); err != nil {
		s.writeError(w, r, err)
		return p, false
	}
	return p, true
}

// ListConversationIssues implements GET /conversations/{id}/issues.
func (s *server) ListConversationIssues(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.conversationForIssues(w, r, id)
	if !ok {
		return
	}
	got, err := s.integrations.ConversationIssues(r.Context(), p.WorkspaceID, id, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.ConversationIssues{Items: make([]oapi.IssueLink, 0, len(got.Items))}
	if got.Repository != nil {
		repo := repositoryBody(*got.Repository)
		out.Repository = &repo
	}
	if d := got.ReplyDraft; d != nil {
		out.ReplyDraft = &oapi.IssueReplyDraft{IssueId: d.LinkID, Number: d.Number, Text: d.Text}
	}
	for _, l := range got.Items {
		out.Items = append(out.Items, issueLinkBody(l))
	}
	writeJSON(w, http.StatusOK, out)
}

// GetConversationIssueDraft implements GET /conversations/{id}/issues/draft.
func (s *server) GetConversationIssueDraft(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.conversationForIssues(w, r, id)
	if !ok {
		return
	}
	d, err := s.integrations.IssueDraft(r.Context(), p.WorkspaceID, id, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.IssueDraft{
		Repository: repositoryBody(d.Repository), Title: d.Title, Body: d.Body, Quote: d.Quote, Labels: nonNil(d.Labels),
		DraftedBy: oapi.IssueDraftDraftedBy(d.DraftedBy),
	}
	if d.AIFallback != "" {
		f := oapi.IssueDraftAiFallback(d.AIFallback)
		out.AiFallback = &f
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateConversationIssue implements POST /conversations/{id}/issues.
func (s *server) CreateConversationIssue(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.conversationForIssues(w, r, id)
	if !ok {
		return
	}
	var req oapi.CreateIssueRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in := integrations.NewIssue{Title: req.Title, Body: req.Body, Quote: deref(req.Quote)}
	if req.Labels != nil {
		in.Labels = *req.Labels
	}
	l, err := s.integrations.CreateIssue(r.Context(), p.WorkspaceID, p.UserID, id, in, workspaceLocale(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, issueLinkBody(l))
}

// LinkConversationIssue implements POST /conversations/{id}/issues/link.
func (s *server) LinkConversationIssue(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.conversationForIssues(w, r, id)
	if !ok {
		return
	}
	var req oapi.LinkIssueRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	l, err := s.integrations.LinkIssue(r.Context(), p.WorkspaceID, p.UserID, id, req.Number)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, issueLinkBody(l))
}

// RetryConversationIssue implements POST /conversations/{id}/issues/{issue_id}/retry.
func (s *server) RetryConversationIssue(w http.ResponseWriter, r *http.Request, id oapi.Id, issueID oapi.IssueId) {
	p, ok := s.conversationForIssues(w, r, id)
	if !ok {
		return
	}
	l, err := s.integrations.RetryIssue(r.Context(), p.WorkspaceID, id, issueID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, issueLinkBody(l))
}

// DismissConversationIssue implements DELETE /conversations/{id}/issues/{issue_id}.
func (s *server) DismissConversationIssue(w http.ResponseWriter, r *http.Request, id oapi.Id, issueID oapi.IssueId) {
	p, ok := s.conversationForIssues(w, r, id)
	if !ok {
		return
	}
	if err := s.integrations.DismissIssue(r.Context(), p.WorkspaceID, id, issueID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListOpenGitHubIssues implements GET /integrations/github/issues.
func (s *server) ListOpenGitHubIssues(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.IssuesCreate)
	if !ok {
		return
	}
	got, err := s.integrations.OpenIssues(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := oapi.OpenGitHubIssueList{Items: make([]oapi.OpenGitHubIssue, 0, len(got))}
	for _, i := range got {
		out.Items = append(out.Items, oapi.OpenGitHubIssue{Repository: i.Repository, Number: i.Number, Title: i.Title, Url: i.URL})
	}
	writeJSON(w, http.StatusOK, out)
}

func issueLinkBody(l integrations.IssueLink) oapi.IssueLink {
	out := oapi.IssueLink{
		Id: l.ID, ConversationId: l.ConversationID, Repository: l.Repository, Title: l.Title,
		Status: oapi.IssueStatus(l.Status), CreatedBy: l.CreatedBy, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
	if l.Number != 0 {
		out.Number, out.Url = &l.Number, &l.URL
	}
	if l.Error != "" {
		e := oapi.IssueLinkError(l.Error)
		out.Error = &e
	}
	return out
}
