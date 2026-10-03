// Package api is the HTTP layer: routing, middleware, mapping domain errors
// to RFC 9457 problems. Business modules know nothing about HTTP.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Pinger reports whether a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the collaborators of the HTTP layer.
type Deps struct {
	Log     *slog.Logger
	DB      Pinger
	Version string
}

// NewRouter builds the root handler.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(requestID)
	r.Use(requestLogger(d.Log))
	r.Use(recoverer(d.Log))
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": d.Version})
	})
	r.Get("/readyz", readyz(d.DB))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, Problem{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Title: "Method Not Allowed"})
	})
	return r
}

func readyz(db Pinger) http.HandlerFunc {
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
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
