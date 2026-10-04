package api

import (
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Personal settings of the signed-in user («Лично для меня», docs/07 §7).
// They need a session, never a permission: every role manages its own
// account. The user always comes from the session.

// UpdateMe implements PATCH /me.
func (s *server) UpdateMe(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	var req oapi.UpdateMeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	var locale *string
	if req.Locale != nil {
		l := string(*req.Locale)
		locale = &l
	}
	u, err := s.tenancy.UpdateUser(r.Context(), st.session.UserID, tenancy.UpdateUserInput{Name: req.Name, Locale: locale})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	m := st.membership
	m.User = u
	writeJSON(w, http.StatusOK, sessionBody(m, st.session))
}

// ChangePassword implements POST /me/password.
func (s *server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	var req oapi.ChangePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	token, sess, err := s.auth.ChangePassword(r.Context(), st.token, st.session, req.CurrentPassword, req.NewPassword, r.UserAgent())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.cookies.set(w, token)
	writeJSON(w, http.StatusOK, sessionBody(st.membership, sess))
}

// ListMySessions implements GET /me/sessions.
func (s *server) ListMySessions(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	list, err := s.auth.Sessions(r.Context(), st.token, st.session.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.DeviceSession, 0, len(list))
	for _, d := range list {
		items = append(items, oapi.DeviceSession{
			Id: d.ID, CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt, UserAgent: d.UserAgent, Current: d.Current,
		})
	}
	writeJSON(w, http.StatusOK, oapi.DeviceSessionList{Items: items})
}

// RevokeSession implements DELETE /me/sessions/{id}.
func (s *server) RevokeSession(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	if err := s.auth.RevokeSession(r.Context(), st.token, st.session.UserID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeOtherSessions implements POST /me/sessions/revoke-others.
func (s *server) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	if _, err := s.auth.RevokeOtherSessions(r.Context(), st.token, st.session.UserID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
