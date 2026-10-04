// Package api is the HTTP layer: routing, middleware, mapping domain errors
// to RFC 9457 problems. Business modules know nothing about HTTP.
// The /api/v1 handlers implement the server interface generated from
// api/openapi.yaml (internal/api/oapi).
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Pinger reports whether a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the collaborators of the HTTP layer.
type Deps struct {
	Log     *slog.Logger
	DB      Pinger
	Redis   Pinger // checked by /readyz when set
	Version string
	// Files serves signed links of the fs storage driver; nil with s3.
	Files LocalFiles

	Tenancy       *tenancy.Service
	Auth          *auth.Service
	Conversations *conversations.Service
	// SecureCookies marks the session cookie Secure and names it with the
	// __Host- prefix. Off only in development over plain HTTP.
	SecureCookies bool
	// SessionMaxAge is the cookie lifetime; the server enforces its own.
	SessionMaxAge time.Duration
	// TrustedOrigins may send unsafe requests besides the API's own origin.
	TrustedOrigins []string
	// TrustedProxies may name the client in X-Forwarded-For.
	TrustedProxies []netip.Prefix
}

// NewRouter builds the root handler.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(requestID)
	r.Use(requestLogger(d.Log))
	r.Use(recoverer(d.Log))
	r.Use(requestTimeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": d.Version})
	})
	r.Get("/readyz", readyz(d.DB, d.Redis))
	if d.Files != nil {
		mountFiles(r, d.Files, d.Log)
	}

	srv := &server{
		log: d.Log, tenancy: d.Tenancy, auth: d.Auth, conversations: d.Conversations,
		cookies: sessionCookies{secure: d.SecureCookies, maxAge: d.SessionMaxAge},
		clients: clientIPs{trusted: d.TrustedProxies},
	}
	r.Group(func(r chi.Router) {
		r.Use(noStore, sameOrigin(d.TrustedOrigins))
		oapi.HandlerWithOptions(srv, oapi.ChiServerOptions{
			BaseURL:    "/api/v1",
			BaseRouter: r,
			// Runs after routing, so authenticate sees the route pattern.
			Middlewares:      []oapi.MiddlewareFunc{srv.authenticate},
			ErrorHandlerFunc: srv.paramError,
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, Problem{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Title: "Method Not Allowed"})
	})
	return r
}

func readyz(db, redis Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			writeProblem(w, r, Problem{
				Status: http.StatusServiceUnavailable, Code: "dependency_unavailable",
				Title: "Service Unavailable", Detail: "database is not reachable",
			})
			return
		}
		if redis != nil {
			if err := redis.Ping(ctx); err != nil {
				writeProblem(w, r, Problem{
					Status: http.StatusServiceUnavailable, Code: "dependency_unavailable",
					Title: "Service Unavailable", Detail: "redis is not reachable",
				})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// paramError answers a request whose parameters do not bind. Binding runs
// before the generated middlewares, so the answer goes through authenticate
// too: without a session it is 401, never a hint about the route. A
// malformed {id} is 404 like a missing resource.
func (s *server) paramError(w http.ResponseWriter, r *http.Request, err error) {
	s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var bad *oapi.InvalidParamFormatError
		switch {
		case errors.As(err, &bad) && bad.ParamName == "id":
			writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		case bad != nil:
			s.writeError(w, r, fmt.Errorf("%w: %s has a wrong format", errBadRequest, bad.ParamName))
		default:
			s.writeError(w, r, errBadRequest)
		}
	})).ServeHTTP(w, r)
}
