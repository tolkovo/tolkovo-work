package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// Lines of support L1/L2/L3 (ADR-0021): the inbox reads them,
// routing.manage changes them, escalation moves a conversation between them.

func (s *server) ListLines(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.ConversationsView)
	if !ok {
		return
	}
	lines, err := s.conversations.Lines(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	body := oapi.LineList{Items: make([]oapi.Line, 0, len(lines))}
	for _, l := range lines {
		body.Items = append(body.Items, lineBody(l))
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) CreateLine(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	var req oapi.LineInput
	if err := decodeRequired(w, r, &req, "name", "level", "member_ids"); err != nil {
		s.writeError(w, r, err)
		return
	}
	l, err := s.conversations.CreateLine(r.Context(), p.WorkspaceID, lineInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, lineBody(l))
}

func (s *server) UpdateLine(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	var req oapi.LineInput
	if err := decodeRequired(w, r, &req, "name", "level", "member_ids"); err != nil {
		s.writeError(w, r, err)
		return
	}
	l, err := s.conversations.UpdateLine(r.Context(), p.WorkspaceID, id, lineInput(req))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, lineBody(l))
}

func (s *server) DeleteLine(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.RoutingManage)
	if !ok {
		return
	}
	if err := s.conversations.DeleteLine(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) EscalateConversation(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, "")
	if !ok {
		return
	}
	if !p.Can(authz.ConversationsTake) && !p.Can(authz.ConversationsAssign) {
		s.writeError(w, r, authz.ErrForbidden)
		return
	}
	var req oapi.EscalateRequest
	if err := decodeRequired(w, r, &req, "line_id", "reason"); err != nil {
		s.writeError(w, r, err)
		return
	}
	c, err := s.conversations.Escalate(r.Context(), p.WorkspaceID, actorOf(p), id, req.LineId, req.Reason, req.Version)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, conversationBody(c))
}

// byLevel: the caller sees conversations by the level of their lines
// (ADR-0021). Owners and admins (conversations.manage) see every line.
func byLevel(p authz.Principal) bool { return !p.Can(authz.ConversationsManage) }

// lineScope keeps a member limited by level away from the conversations of
// lines above it, on every operation under /conversations/{id}: such a
// conversation is 404 to them, as a missing one. It runs after
// authenticate, which sets the principal.
func (s *server) lineScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pattern := chi.RouteContext(r.Context()).RoutePattern()
		p, ok := authz.PrincipalFrom(r.Context())
		if !ok || !strings.HasPrefix(pattern, "/api/v1/conversations/{id}") || !p.Can(authz.ConversationsView) || !byLevel(p) {
			next.ServeHTTP(w, r)
			return
		}
		id, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			next.ServeHTTP(w, r) // the handler answers the bad id
			return
		}
		sees, err := s.conversations.Sees(r.Context(), p.WorkspaceID, p.UserID, id)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if !sees {
			s.writeError(w, r, conversations.ErrNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// lineFilter reads the line parameter of a list: "" any, "first" the first
// line, or a line id.
func lineFilter(v string) (by bool, id *uuid.UUID, err error) {
	switch v {
	case "":
		return false, nil, nil
	case "first":
		return true, nil, nil
	}
	l, err := uuid.Parse(v)
	if err != nil {
		return false, nil, fmt.Errorf("%w: line must be first or a line id", errBadRequest)
	}
	return true, &l, nil
}

func lineInput(req oapi.LineInput) conversations.LineInput {
	return conversations.LineInput{Name: req.Name, Level: req.Level, MemberIDs: req.MemberIds}
}

func lineBody(l conversations.Line) oapi.Line {
	return oapi.Line{Id: l.ID, Name: l.Name, Level: l.Level, MemberIds: nonNil(l.MemberIDs)}
}
