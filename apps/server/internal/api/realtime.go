package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime"
)

// RealtimePath is the WebSocket endpoint of the panel (api/ws/*.schema.json).
// It lives under /api so the panel reaches it on its own origin with the
// session cookie, and the dev proxy forwards it like the REST API.
const RealtimePath = "/api/v1/realtime"

// Realtime is the WebSocket gateway; *realtime.Gateway implements it.
type Realtime interface {
	Serve(w http.ResponseWriter, r *http.Request, cl realtime.Client) error
}

// realtimeHandler authenticates the handshake with the session cookie, like
// every REST request (ADR-0010), and hands the connection to the gateway,
// which checks the Origin. The CSRF header is not needed: a handshake is a
// GET that changes nothing, and the Origin check stops cross-site pages.
// The token stays in this closure for re-checks and is never logged.
func (s *server) realtimeHandler(gw Realtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token := s.cookies.token(r)
		if token == "" {
			s.writeError(w, r, auth.ErrNoSession)
			return
		}
		_, m, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			if errors.Is(err, auth.ErrNoSession) {
				s.cookies.clear(w)
			}
			s.writeError(w, r, err)
			return
		}
		setLogWorkspace(r.Context(), m.Workspace.ID.String())
		cl := realtime.Client{
			UserID: m.User.ID, WorkspaceID: m.Workspace.ID, Role: m.Role,
			Recheck: func(ctx context.Context) (string, error) {
				_, m, err := s.auth.Authenticate(ctx, token)
				if errors.Is(err, auth.ErrNoSession) {
					return "", realtime.ErrSessionEnded
				}
				if err != nil {
					return "", err
				}
				return m.Role, nil
			},
		}
		switch err := gw.Serve(w, r, cl); {
		case errors.Is(err, realtime.ErrOriginNotAllowed):
			writeProblem(w, r, Problem{
				Status: http.StatusForbidden, Code: "cross_origin", Title: "Forbidden",
				Detail: "cross-origin requests are not allowed",
			})
		case errors.Is(err, realtime.ErrUnavailable):
			writeProblem(w, r, Problem{
				Status: http.StatusServiceUnavailable, Code: "dependency_unavailable",
				Title: "Service Unavailable", Detail: "realtime is not available, retry later",
			})
		}
	}
}
