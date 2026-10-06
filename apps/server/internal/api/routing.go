package api

import (
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing"
)

// GetRouting implements GET /routing.
func (s *server) GetRouting(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.MembersView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	mode, err := s.routing.Mode(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.RoutingSettings{Mode: oapi.RoutingSettingsMode(mode)})
}

// SetRouting implements PUT /routing.
func (s *server) SetRouting(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.RoutingManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.RoutingSettings
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	mode, err := s.routing.SetMode(r.Context(), p.WorkspaceID, routing.Mode(req.Mode))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.RoutingSettings{Mode: oapi.RoutingSettingsMode(mode)})
}

// GetMyPresence implements GET /me/presence.
func (s *server) GetMyPresence(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	status, err := s.routing.Status(r.Context(), st.session.WorkspaceID, st.session.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.MyPresence{Status: oapi.MyPresenceStatus(status)})
}

// SetMyPresence implements PUT /me/presence.
func (s *server) SetMyPresence(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	var req oapi.MyPresence
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	status, err := s.routing.SetStatus(r.Context(), st.session.WorkspaceID, st.session.UserID, routing.Status(req.Status))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.MyPresence{Status: oapi.MyPresenceStatus(status)})
}
