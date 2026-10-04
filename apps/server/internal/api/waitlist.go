package api

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/waitlistapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/waitlist"
)

// waitlistBodyBytes bounds waitlist bodies: an email, a few flags and
// channels fit in far less (docs/00 §9.4).
const waitlistBodyBytes = 4 << 10

// waitlistPaths are the public waitlist routes; each answers CORS preflight.
var waitlistPaths = []string{"/api/v1/waitlist", "/api/v1/waitlist/confirm", "/api/v1/waitlist/unsubscribe"}

// mountWaitlist serves the waitlist in its own group: the landing calls it
// from another origin without cookies, so the panel's session, Origin and
// CSRF checks do not apply. CORS admits only the configured landing
// origins and rate limits stand in for authentication (docs/02 §13.1).
func mountWaitlist(r chi.Router, svc *waitlist.Service, origins []string, log *slog.Logger) {
	ws := &waitlistServer{svc: svc, log: log}
	cors := newCORSPolicy(origins)
	r.Group(func(r chi.Router) {
		r.Use(noStore, cors.check)
		for _, p := range waitlistPaths {
			r.Options(p, cors.preflight)
		}
		waitlistapi.HandlerWithOptions(ws, waitlistapi.ChiServerOptions{
			BaseURL:    "/api/v1",
			BaseRouter: r,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
				ws.writeError(w, r, errBadRequest)
			},
		})
	})
}

// corsPolicy allows simple credential-less CORS from a fixed origin list.
type corsPolicy struct {
	allowed map[string]bool
}

func newCORSPolicy(origins []string) corsPolicy {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.TrimRight(o, "/")] = true
	}
	return corsPolicy{allowed: allowed}
}

// check rejects browser requests from other origins on the server, not
// only in the browser, and marks allowed ones readable. Requests without
// Origin come from non-browser clients; the rate limits cover them.
// Access-Control-Allow-Credentials is never sent: no cookies here.
func (c corsPolicy) check(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Origin")
		if !c.allowed[origin] {
			writeProblem(w, r, Problem{
				Status: http.StatusForbidden, Code: "cross_origin", Title: "Forbidden",
				Detail: "this origin may not call the waitlist",
			})
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
		next.ServeHTTP(w, r)
	})
}

// preflight answers OPTIONS after check has vetted the origin.
func (c corsPolicy) preflight(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
		w.Header().Set("Access-Control-Max-Age", "600")
	}
	w.WriteHeader(http.StatusNoContent)
}

// waitlistServer implements the generated waitlistapi.ServerInterface.
type waitlistServer struct {
	svc *waitlist.Service
	log *slog.Logger
}

var _ waitlistapi.ServerInterface = (*waitlistServer)(nil)

// JoinWaitlist implements POST /waitlist.
func (s *waitlistServer) JoinWaitlist(w http.ResponseWriter, r *http.Request) {
	var req waitlistapi.WaitlistJoinRequest
	if err := decodeJSONLimit(w, r, &req, waitlistBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	in := waitlist.JoinInput{
		Email: req.Email, Locale: string(req.Locale), Consent: req.Consent, Honeypot: deref(req.Website),
	}
	if req.Channels != nil {
		for _, c := range *req.Channels {
			in.Channels = append(in.Channels, string(c))
		}
	}
	if err := s.svc.Join(r.Context(), in, clientIP(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, waitlistapi.WaitlistAccepted{Status: waitlistapi.PendingConfirmation})
}

// ConfirmWaitlist implements POST /waitlist/confirm.
func (s *waitlistServer) ConfirmWaitlist(w http.ResponseWriter, r *http.Request) {
	var req waitlistapi.WaitlistTokenRequest
	if err := decodeJSONLimit(w, r, &req, waitlistBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.svc.Confirm(r.Context(), req.Token, clientIP(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, waitlistapi.WaitlistConfirmed{Status: waitlistapi.Confirmed})
}

// UnsubscribeWaitlist implements POST /waitlist/unsubscribe.
func (s *waitlistServer) UnsubscribeWaitlist(w http.ResponseWriter, r *http.Request) {
	var req waitlistapi.WaitlistTokenRequest
	if err := decodeJSONLimit(w, r, &req, waitlistBodyBytes); err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.svc.Unsubscribe(r.Context(), req.Token, clientIP(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeError maps waitlist errors to problem+json. Errors never carry the
// email, so the default branch may log them.
func (s *waitlistServer) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var limited *waitlist.RateLimitedError
	switch {
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(limited.RetryAfter.Seconds()))))
		writeProblem(w, r, Problem{Status: http.StatusTooManyRequests, Code: "too_many_requests", Title: "Too Many Requests"})
	case errors.Is(err, waitlist.ErrTokenInvalid):
		writeProblem(w, r, Problem{
			Status: http.StatusNotFound, Code: "token_invalid", Title: "Not Found",
			Detail: "the link is unknown, expired or already used",
		})
	case errors.Is(err, errBadRequest), errors.Is(err, waitlist.ErrInvalid):
		msg := err.Error()
		for _, sentinel := range []error{errBadRequest, waitlist.ErrInvalid} {
			msg = strings.TrimPrefix(msg, sentinel.Error()+": ")
		}
		writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: msg})
	default:
		s.log.ErrorContext(r.Context(), "waitlist request failed",
			slog.String("request_id", middleware.GetReqID(r.Context())),
			slog.String("error", err.Error()))
		writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
	}
}
