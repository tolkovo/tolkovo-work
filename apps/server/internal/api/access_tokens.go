package api

import (
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Personal access tokens (ADR-0016). Only a session reaches these routes:
// tokenOperations keeps tokens out, so a token cannot issue tokens.

// ListMyAccessTokens implements GET /me/access-tokens.
func (s *server) ListMyAccessTokens(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, "")
	if !ok {
		return
	}
	items, err := s.auth.AccessTokens(r.Context(), p.WorkspaceID, &p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, accessTokenList(items))
}

// CreateAccessToken implements POST /me/access-tokens.
func (s *server) CreateAccessToken(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, "")
	if !ok {
		return
	}
	var req oapi.AccessTokenCreate
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	scopes := make([]string, 0, len(req.Scopes))
	for _, sc := range req.Scopes {
		scopes = append(scopes, string(sc))
	}
	got, err := s.auth.IssueAccessToken(r.Context(), p.WorkspaceID, p.UserID, auth.IssueTokenInput{
		Name: req.Name, Scopes: scopes, ExpiresInDays: req.ExpiresInDays,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.AccessTokenCreated{Token: accessTokenBody(got.Token), Secret: got.Secret.Reveal()})
}

// RevokeMyAccessToken implements DELETE /me/access-tokens/{id}.
func (s *server) RevokeMyAccessToken(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, "")
	if !ok {
		return
	}
	if err := s.auth.RevokeAccessToken(r.Context(), p.WorkspaceID, id, &p.UserID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListAccessTokens implements GET /access-tokens.
func (s *server) ListAccessTokens(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.MembersManage)
	if !ok {
		return
	}
	items, err := s.auth.AccessTokens(r.Context(), p.WorkspaceID, nil)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, accessTokenList(items))
}

// RevokeAccessToken implements DELETE /access-tokens/{id}.
func (s *server) RevokeAccessToken(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.MembersManage)
	if !ok {
		return
	}
	if err := s.auth.RevokeAccessToken(r.Context(), p.WorkspaceID, id, nil); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func accessTokenList(items []tenancy.AccessToken) oapi.AccessTokenList {
	out := oapi.AccessTokenList{Items: make([]oapi.AccessToken, 0, len(items))}
	for _, t := range items {
		out.Items = append(out.Items, accessTokenBody(t))
	}
	return out
}

func accessTokenBody(t tenancy.AccessToken) oapi.AccessToken {
	scopes := make([]oapi.AccessTokenScope, 0, len(t.Scopes))
	for _, sc := range t.Scopes {
		scopes = append(scopes, oapi.AccessTokenScope(sc))
	}
	return oapi.AccessToken{
		Id: t.ID, UserId: t.UserID, UserName: t.UserName, Name: t.Name, Scopes: scopes, Prefix: t.Prefix,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt,
	}
}
