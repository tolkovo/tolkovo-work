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
	set, err := s.routing.Settings(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, routingBody(set))
}

// SetRouting implements PUT /routing.
func (s *server) SetRouting(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.RoutingManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.RoutingSettings
	if err := decodeRequired(w, r, &req, "mode", "max_chats"); err != nil {
		s.writeError(w, r, err)
		return
	}
	set, err := s.routing.SetSettings(r.Context(), p.WorkspaceID, routing.Settings{Mode: routing.Mode(req.Mode), MaxChats: req.MaxChats})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, routingBody(set))
}

func routingBody(set routing.Settings) oapi.RoutingSettings {
	return oapi.RoutingSettings{Mode: oapi.RoutingSettingsMode(set.Mode), MaxChats: set.MaxChats}
}

// ListRoutingMembers implements GET /routing/members.
func (s *server) ListRoutingMembers(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.MembersView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	limits, err := s.routing.Limits(r.Context(), p.WorkspaceID, s.conversations.Loads)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.RoutingMember, len(limits))
	for i, l := range limits {
		items[i] = oapi.RoutingMember{UserId: l.UserID, MaxChats: l.MaxChats, Open: l.Open}
	}
	writeJSON(w, http.StatusOK, oapi.RoutingMemberList{Items: items})
}

// SetRoutingMember implements PUT /routing/members/{id}.
func (s *server) SetRoutingMember(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.RoutingManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.SetRoutingMemberRequest
	if err := decodeRequired(w, r, &req, "max_chats"); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.routing.SetLimit(r.Context(), p.WorkspaceID, id, req.MaxChats); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
