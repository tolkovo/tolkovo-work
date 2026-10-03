package api

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// requestID assigns a UUIDv7 to every request and returns it in X-Request-Id.
// chi's middleware.RequestID is not used because it embeds the hostname,
// which would leak into problem responses. Incoming IDs are ignored: clients
// must not be able to forge log correlation.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.Must(uuid.NewV7()).String()
		w.Header().Set(middleware.RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), middleware.RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requestLogger logs one line per request. It logs the route pattern, not
// the raw path or query, so IDs, tokens and personal data stay out of logs.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			level := slog.LevelInfo
			if ww.Status() >= 500 {
				level = slog.LevelError
			}
			log.LogAttrs(r.Context(), level, "http request",
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", ww.Status()),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// recoverer turns a panic into a 500 problem and logs the stack trace.
func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as net/http does
					panic(rec) // net/http uses it to abort the response silently
				}
				log.ErrorContext(r.Context(), "panic in handler",
					slog.String("request_id", middleware.GetReqID(r.Context())),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
			}()
			next.ServeHTTP(w, r)
		})
	}
}
