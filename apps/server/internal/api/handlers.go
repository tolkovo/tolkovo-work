package api

import (
	"log/slog"
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/ingest"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// server implements the generated oapi.ServerInterface.
type server struct {
	log           *slog.Logger
	tenancy       *tenancy.Service
	auth          *auth.Service
	conversations *conversations.Service
	notifications *notifications.Service
	channels      *channels.Service
	ingest        *ingest.Service
	integrations  *integrations.Service
	cookies       sessionCookies
	clients       clientIPs
}

var _ oapi.ServerInterface = (*server)(nil)

// Register implements POST /auth/register.
func (s *server) Register(w http.ResponseWriter, r *http.Request) {
	var req oapi.RegisterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in, err := s.auth.Register(r.Context(), auth.RegisterInput{
		Email: req.Email, Password: req.Password, Name: req.Name, Locale: string(deref(req.Locale)),
		WorkspaceName: req.WorkspaceName, WorkspaceSlug: req.WorkspaceSlug, Timezone: deref(req.Timezone),
	}, s.browser(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.revokeCookieSession(r)
	s.cookies.set(w, in.Token)
	writeJSON(w, http.StatusCreated, sessionBody(in.Membership, in.Session))
}

// Login implements POST /auth/login.
func (s *server) Login(w http.ResponseWriter, r *http.Request) {
	var req oapi.LoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in, err := s.auth.Login(r.Context(), req.Email, req.Password, s.browser(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.revokeCookieSession(r)
	s.cookies.set(w, in.Token)
	writeJSON(w, http.StatusOK, sessionBody(in.Membership, in.Session))
}

// revokeCookieSession revokes a session the browser still sent along when
// it signs in again, so the old token cannot be replayed. Best effort: the
// new session is valid either way.
func (s *server) revokeCookieSession(r *http.Request) {
	if old := s.cookies.token(r); old != "" {
		if err := s.auth.Logout(r.Context(), old); err != nil {
			s.log.WarnContext(r.Context(), "revoke previous session", slog.String("error", err.Error()))
		}
	}
}

// Logout implements POST /auth/logout.
func (s *server) Logout(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	if err := s.auth.Logout(r.Context(), st.token); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.cookies.clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// SwitchWorkspace implements POST /auth/workspace.
func (s *server) SwitchWorkspace(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	var req oapi.SwitchWorkspaceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in, err := s.auth.SwitchWorkspace(r.Context(), st.token, st.session, req.WorkspaceId)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.cookies.set(w, in.Token)
	writeJSON(w, http.StatusOK, sessionBody(in.Membership, in.Session))
}

// GetMe implements GET /me.
func (s *server) GetMe(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	writeJSON(w, http.StatusOK, sessionBody(st.membership, st.session))
}

// ListMyWorkspaces implements GET /me/workspaces.
func (s *server) ListMyWorkspaces(w http.ResponseWriter, r *http.Request) {
	// One's own workspaces need a session, not a permission.
	p, ok := authz.PrincipalFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	list, err := s.tenancy.UserWorkspaces(r.Context(), p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.WorkspaceSummary, 0, len(list))
	for _, ws := range list {
		items = append(items, oapi.WorkspaceSummary{Id: ws.ID, Slug: ws.Slug, Name: ws.Name, Role: oapi.Role(ws.Role)})
	}
	writeJSON(w, http.StatusOK, oapi.WorkspaceList{Items: items})
}

// ListMembers implements GET /members.
func (s *server) ListMembers(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.MembersView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	members, err := s.tenancy.Members(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.Member, 0, len(members))
	for _, m := range members {
		items = append(items, oapi.Member{UserId: m.UserID, Email: m.Email, Name: m.Name, Role: oapi.Role(m.Role), Active: m.Active})
	}
	writeJSON(w, http.StatusOK, oapi.MemberList{Items: items})
}

// UpdateWorkspace implements PATCH /workspace.
func (s *server) UpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.WorkspaceManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.UpdateWorkspaceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	var locale *string
	if req.Locale != nil {
		l := string(*req.Locale)
		locale = &l
	}
	ws, err := s.tenancy.UpdateWorkspace(r.Context(), p.WorkspaceID, tenancy.UpdateWorkspaceInput{
		Name: req.Name, Locale: locale, Timezone: req.Timezone,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, workspaceBody(ws))
}

func sessionBody(m tenancy.Membership, sess auth.Session) oapi.Session {
	perms := authz.Permissions(m.Role)
	names := make([]string, len(perms))
	for i, p := range perms {
		names[i] = string(p)
	}
	return oapi.Session{
		User: oapi.User{
			Id: m.User.ID, Email: m.User.Email, Name: m.User.Name, Locale: oapi.Locale(m.User.Locale),
			EmailVerified: m.User.EmailVerifiedAt != nil,
		},
		Workspace:   workspaceBody(m.Workspace),
		Role:        oapi.Role(m.Role),
		Permissions: names,
		CsrfToken:   sess.CSRFToken,
	}
}

func workspaceBody(ws tenancy.Workspace) oapi.Workspace {
	return oapi.Workspace{
		Id: ws.ID, Slug: ws.Slug, Name: ws.Name, Locale: oapi.Locale(ws.Locale), Timezone: ws.Timezone,
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
