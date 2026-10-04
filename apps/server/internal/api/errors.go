package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// maxBodyBytes bounds JSON request bodies (docs/00 §9.4).
const maxBodyBytes = 64 << 10

// errBadRequest marks malformed request bodies.
var errBadRequest = errors.New("bad request")

// writeError maps domain errors to problem+json. Unknown errors are logged
// with the request id and answered with a generic 500.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var limited *auth.RateLimitedError
	switch {
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(limited.RetryAfter.Seconds()))))
		writeProblem(w, r, Problem{Status: http.StatusTooManyRequests, Code: "too_many_requests", Title: "Too Many Requests"})
	case errors.Is(err, auth.ErrNoSession), errors.Is(err, authz.ErrUnauthenticated):
		writeProblem(w, r, Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Title: "Unauthorized"})
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, Problem{
			Status: http.StatusUnauthorized, Code: "invalid_credentials", Title: "Unauthorized",
			Detail: "wrong email or password",
		})
	case errors.Is(err, authz.ErrForbidden):
		writeProblem(w, r, Problem{Status: http.StatusForbidden, Code: "forbidden", Title: "Forbidden"})
	case errors.Is(err, auth.ErrNoWorkspace):
		writeProblem(w, r, Problem{
			Status: http.StatusForbidden, Code: "no_workspace", Title: "Forbidden",
			Detail: "the account is not an active member of any workspace",
		})
	case errors.Is(err, auth.ErrWorkspaceNotFound), errors.Is(err, tenancy.ErrNotFound):
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
	case errors.Is(err, tenancy.ErrEmailTaken):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "email_taken", Title: "Conflict"})
	case errors.Is(err, tenancy.ErrSlugTaken):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "slug_taken", Title: "Conflict"})
	case errors.Is(err, errBadRequest), errors.Is(err, auth.ErrInvalid), errors.Is(err, tenancy.ErrInvalid):
		writeProblem(w, r, Problem{
			Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request", Detail: detail(err),
		})
	default:
		s.log.ErrorContext(r.Context(), "request failed",
			slog.String("request_id", middleware.GetReqID(r.Context())),
			slog.String("error", err.Error()))
		writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
	}
}

// detail strips the sentinel prefix from "<sentinel>: <reason>" errors, so
// only the human reason reaches the client.
func detail(err error) string {
	msg := err.Error()
	for _, sentinel := range []error{errBadRequest, auth.ErrInvalid, tenancy.ErrInvalid} {
		if after, ok := strings.CutPrefix(msg, sentinel.Error()+": "); ok {
			return after
		}
	}
	return ""
}

// decodeJSON reads one JSON object into dst. Unknown fields are rejected,
// so a client cannot slip in fields such as workspace_id that the server
// would silently ignore.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", errBadRequest)
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: body is larger than %d bytes", errBadRequest, maxBodyBytes)
		}
		// encoding/json errors name fields and types, never the values.
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "json: "))
	}
	if dec.More() {
		return fmt.Errorf("%w: body must be a single JSON object", errBadRequest)
	}
	return nil
}
