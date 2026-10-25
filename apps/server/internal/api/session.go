package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Cookie and header names (api/openapi.yaml, "Authentication" and "CSRF").
const (
	cookieName       = "tolkovo_session"
	secureCookieName = "__Host-tolkovo_session" // the browser enforces Secure, Path=/ and no Domain
	csrfHeader       = "X-CSRF-Token"
)

// publicOperations are the only /api/v1 routes served without a session.
// Every other route requires one: authentication is deny-by-default, and
// TestEveryRouteRequiresSessionExceptPublic walks the router to prove it.
var publicOperations = map[string]bool{
	"POST /api/v1/auth/register":           true,
	"GET /api/v1/auth/registration":        true,
	"POST /api/v1/auth/login":              true,
	"POST /api/v1/auth/invitation/preview": true,
	"POST /api/v1/auth/invitation/accept":  true,
	"POST /api/v1/auth/password/forgot":    true,
	"POST /api/v1/auth/password/reset":     true,
	"POST /api/v1/auth/email/verify":       true,
	// Apps authenticate with an ingest key, checked by the handler.
	"POST /api/v1/ingest/reports":               true,
	"POST /api/v1/ingest/reports/{id}/complete": true,
	"GET /api/v1/ingest/reports/{id}/replies":   true,
	"POST /api/v1/ingest/reports/{id}/messages": true,
	// The widget authenticates with its session token, checked by the
	// handler (widget.go).
	"POST /api/v1/widget/session":     true,
	"DELETE /api/v1/widget/session":   true,
	"GET /api/v1/widget/messages":     true,
	"POST /api/v1/widget/messages":    true,
	"POST /api/v1/widget/receipts":    true,
	"POST /api/v1/widget/uploads":     true,
	"POST /api/v1/widget/contact":     true,
	"GET /api/v1/widget/availability": true,
	"POST /api/v1/widget/rating":      true,
	"POST /api/v1/widget/topic":       true,
}

// tokenOperations are the only routes a personal access token opens
// (ADR-0016): the tasks API and the MCP server, as api/openapi.yaml lists
// accessToken in their security. A token reaches nothing else, whatever its
// member's role, and cannot issue tokens. The scopes are checked again by
// authz.Require in the handlers.
var tokenOperations = map[string]bool{
	"GET /api/v1/topics":                        true,
	"GET /api/v1/tasks":                         true,
	"POST /api/v1/tasks":                        true,
	"GET /api/v1/tasks/summary":                 true,
	"GET /api/v1/tasks/board":                   true,
	"GET /api/v1/tasks/flow":                    true,
	"GET /api/v1/tasks/{id}":                    true,
	"PUT /api/v1/tasks/{id}":                    true,
	"GET /api/v1/tasks/{id}/activity":           true,
	"POST /api/v1/tasks/{id}/activity":          true,
	"GET /api/v1/task-queues":                   true,
	"GET /api/v1/task-tags":                     true,
	"POST /api/v1/task-tags":                    true,
	"POST /api/v1/mcp":                          true,
	"POST /api/v1/task-files":                   true,
	"GET /api/v1/task-files/{id}/download-link": true,
	"POST /api/v1/tasks/{id}/files":             true,
	"DELETE /api/v1/tasks/{id}/files/{file_id}": true,
}

// sessionCookies writes and reads the session cookie.
type sessionCookies struct {
	secure bool
	maxAge time.Duration
}

func (c sessionCookies) name() string {
	if c.secure {
		return secureCookieName
	}
	return cookieName
}

func (c sessionCookies) token(r *http.Request) string {
	ck, err := r.Cookie(c.name())
	if err != nil {
		return ""
	}
	return ck.Value
}

func (c sessionCookies) set(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is off only in development over plain HTTP (config.SecureCookies)
		Name: c.name(), Value: token, Path: "/",
		MaxAge: int(c.maxAge / time.Second), HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (c sessionCookies) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is off only in development over plain HTTP (config.SecureCookies)
		Name: c.name(), Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: c.secure, SameSite: http.SameSiteLaxMode,
	})
}

// authState is the verified session of the current request.
type authState struct {
	token      string
	session    auth.Session
	membership tenancy.Membership
}

type authStateKey struct{}

func authStateFrom(ctx context.Context) (authState, bool) {
	st, ok := ctx.Value(authStateKey{}).(authState)
	return st, ok
}

// authenticate runs after routing (as a generated-server middleware), so
// the route pattern is known. For every non-public operation it resolves
// the session cookie, re-checks the membership, enforces the CSRF token on
// unsafe methods and stores the Principal. The workspace comes only from
// the server-side session, never from the request.
func (s *server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.Method + " " + chi.RouteContext(r.Context()).RoutePattern()
		if publicOperations[route] {
			next.ServeHTTP(w, r)
			return
		}
		if h := r.Header.Get("Authorization"); h != "" {
			s.authenticateToken(next, w, r, route, h)
			return
		}
		token := s.cookies.token(r)
		if token == "" {
			s.writeError(w, r, auth.ErrNoSession)
			return
		}
		sess, m, err := s.auth.Authenticate(r.Context(), token)
		if err != nil {
			// Clear the cookie only for a dead session, not on an outage.
			if errors.Is(err, auth.ErrNoSession) {
				s.cookies.clear(w)
			}
			s.writeError(w, r, err)
			return
		}
		setLogWorkspace(r.Context(), m.Workspace.ID.String())
		if isUnsafe(r.Method) && !sess.CSRFMatches(r.Header.Get(csrfHeader)) {
			writeProblem(w, r, Problem{
				Status: http.StatusForbidden, Code: "csrf_failed", Title: "Forbidden",
				Detail: "send the session csrf_token in the " + csrfHeader + " header",
			})
			return
		}
		ctx := authz.WithPrincipal(r.Context(), authz.Principal{
			UserID: m.User.ID, WorkspaceID: m.Workspace.ID, Role: m.Role,
		})
		ctx = context.WithValue(ctx, authStateKey{}, authState{token: token, session: sess, membership: m})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authenticateToken lets in a request with a personal access token: no
// cookie and no CSRF token, the member and role read again on every
// request, the permissions narrowed to the token's scopes.
func (s *server) authenticateToken(next http.Handler, w http.ResponseWriter, r *http.Request, route, header string) {
	token, ok := bearerToken(header)
	if !ok || !auth.IsAccessToken(token) {
		s.writeError(w, r, auth.ErrInvalidAccessToken)
		return
	}
	ta, err := s.auth.AuthenticateToken(r.Context(), token, s.browser(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	m := ta.Membership
	setLogWorkspace(r.Context(), m.Workspace.ID.String())
	if !tokenOperations[route] {
		writeProblem(w, r, Problem{
			Status: http.StatusForbidden, Code: "token_not_allowed", Title: "Forbidden",
			Detail: "this operation does not take a personal access token",
		})
		return
	}
	ctx := authz.WithPrincipal(r.Context(), authz.Principal{
		UserID: m.User.ID, WorkspaceID: m.Workspace.ID, Role: m.Role, Scopes: ta.Scopes,
	})
	next.ServeHTTP(w, r.WithContext(ctx))
}

// bearerToken reads "Bearer <token>" (the scheme in any case).
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// sameOrigin rejects unsafe cross-origin browser requests, including login
// and registration, which have no session yet (login CSRF). Requests
// without Origin and Sec-Fetch-Site come from non-browser clients, which
// cannot carry a victim's cookies.
func sameOrigin(trusted []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(trusted))
	for _, o := range trusted {
		allowed[strings.TrimRight(o, "/")] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The widget API is cross-origin by design and carries no
			// cookie; it checks the Origin against the widget's list.
			if isUnsafe(r.Method) && !isWidgetRoute(r) && !originAllowed(r, allowed) {
				writeProblem(w, r, Problem{
					Status: http.StatusForbidden, Code: "cross_origin", Title: "Forbidden",
					Detail: "cross-origin requests are not allowed",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(r *http.Request, trusted map[string]bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	if trusted[origin] {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && u.Host == r.Host
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	return true
}

// noStore keeps session data and CSRF tokens out of shared caches.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// browser describes the client that signs in for the auth module.
func (s *server) browser(r *http.Request) auth.Client {
	return auth.Client{IP: s.clients.of(r), UserAgent: r.UserAgent()}
}
