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

	"github.com/tolkovo/tolkovo/apps/server/internal/ai"
	"github.com/tolkovo/tolkovo/apps/server/internal/auth"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/routing"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// maxBodyBytes bounds JSON request bodies (docs/00 §9.4).
const maxBodyBytes = 64 << 10

// errBadRequest marks malformed request bodies.
var errBadRequest = errors.New("bad request")

// writeError maps domain errors to problem+json. Unknown errors are logged
// with the request id and answered with a generic 500.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if s.writeChannelError(w, r, err) || s.writeIntegrationError(w, r, err) || s.writeAIError(w, r, err) {
		return
	}
	var (
		limited *auth.RateLimitedError
		flood   *telegram.FloodWaitError
	)
	switch {
	case errors.As(err, &limited):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(limited.RetryAfter.Seconds()))))
		writeProblem(w, r, Problem{Status: http.StatusTooManyRequests, Code: "too_many_requests", Title: "Too Many Requests"})
	case errors.As(err, &flood):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(flood.Wait.Seconds()))))
		writeProblem(w, r, Problem{
			Status: http.StatusTooManyRequests, Code: "too_many_requests", Title: "Too Many Requests",
			Detail: "Telegram asks to wait before the next attempt",
		})
	case errors.Is(err, auth.ErrNoSession), errors.Is(err, authz.ErrUnauthenticated):
		writeProblem(w, r, Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Title: "Unauthorized"})
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeProblem(w, r, Problem{
			Status: http.StatusUnauthorized, Code: "invalid_credentials", Title: "Unauthorized",
			Detail: "wrong email or password",
		})
	case errors.Is(err, auth.ErrWrongPassword):
		writeProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Code: "wrong_password", Title: "Unprocessable Content",
			Detail: "the current password does not match",
		})
	case errors.Is(err, auth.ErrInvalidToken):
		writeProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Code: "invalid_token", Title: "Unprocessable Content",
			Detail: "the link is invalid, used, revoked or expired",
		})
	case errors.Is(err, auth.ErrEmailDisabled):
		writeProblem(w, r, Problem{
			Status: http.StatusServiceUnavailable, Code: "email_disabled", Title: "Service Unavailable",
			Detail: "outgoing email is not configured on this server",
		})
	case errors.Is(err, auth.ErrEmailVerified):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "email_verified", Title: "Conflict"})
	case errors.Is(err, tenancy.ErrAlreadyMember):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "already_member", Title: "Conflict",
			Detail: "this person is already an active member of the workspace",
		})
	case errors.Is(err, auth.ErrCurrentSession):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "current_session", Title: "Conflict",
			Detail: "use POST /auth/logout to end the current session",
		})
	case errors.Is(err, authz.ErrForbidden):
		writeProblem(w, r, Problem{Status: http.StatusForbidden, Code: "forbidden", Title: "Forbidden"})
	case errors.Is(err, auth.ErrNoWorkspace):
		writeProblem(w, r, Problem{
			Status: http.StatusForbidden, Code: "no_workspace", Title: "Forbidden",
			Detail: "the account is not an active member of any workspace",
		})
	case errors.Is(err, conversations.ErrNotAssignee):
		writeProblem(w, r, Problem{
			Status: http.StatusForbidden, Code: "not_assignee", Title: "Forbidden",
			Detail: "the conversation is not assigned to you",
		})
	case errors.Is(err, auth.ErrWorkspaceNotFound), errors.Is(err, auth.ErrSessionNotFound), errors.Is(err, tenancy.ErrNotFound),
		errors.Is(err, tenancy.ErrInvitationNotFound), errors.Is(err, conversations.ErrNotFound), errors.Is(err, channels.ErrNotFound),
		errors.Is(err, integrations.ErrNotFound):
		writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
	case errors.Is(err, tenancy.ErrRegistrationClosed):
		writeProblem(w, r, Problem{Status: http.StatusForbidden, Code: "registration_closed", Title: "Forbidden",
			Detail: "this server takes no new accounts; ask an admin for an invitation"})
	case errors.Is(err, tenancy.ErrEmailTaken):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "email_taken", Title: "Conflict"})
	case errors.Is(err, tenancy.ErrSlugTaken):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "slug_taken", Title: "Conflict"})
	case errors.Is(err, conversations.ErrTransition):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "invalid_transition", Title: "Conflict", Detail: detail(err)})
	case errors.Is(err, conversations.ErrConflict):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "version_conflict", Title: "Conflict",
			Detail: "the conversation has changed, reload it",
		})
	case errors.Is(err, conversations.ErrTagExists):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "tag_exists", Title: "Conflict"})
	case errors.Is(err, conversations.ErrNotFailed):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "not_failed", Title: "Conflict",
			Detail: "only a reply that failed to deliver can be sent again",
		})
	case errors.Is(err, conversations.ErrClosed):
		writeProblem(w, r, Problem{Status: http.StatusConflict, Code: "conversation_closed", Title: "Conflict"})
	case errors.Is(err, conversations.ErrNotUploaded):
		writeProblem(w, r, Problem{
			Status: http.StatusConflict, Code: "attachment_not_uploaded", Title: "Conflict",
			Detail: "upload the file by its link before sending it",
		})
	case errors.Is(err, conversations.ErrIdempotencyMismatch):
		writeProblem(w, r, Problem{
			Status: http.StatusUnprocessableEntity, Code: "idempotency_key_reused", Title: "Unprocessable Content",
			Detail: "the Idempotency-Key was used with a different request",
		})
	case errors.Is(err, errBadRequest), errors.Is(err, auth.ErrInvalid), errors.Is(err, tenancy.ErrInvalid),
		errors.Is(err, conversations.ErrInvalid), errors.Is(err, notifications.ErrInvalid), errors.Is(err, channels.ErrInvalid),
		errors.Is(err, integrations.ErrInvalid), errors.Is(err, routing.ErrInvalid), errors.Is(err, ai.ErrInvalid):
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
	for _, sentinel := range []error{
		errBadRequest, auth.ErrInvalid, tenancy.ErrInvalid, conversations.ErrInvalid, conversations.ErrTransition,
		notifications.ErrInvalid, channels.ErrInvalid, integrations.ErrInvalid, routing.ErrInvalid, ai.ErrInvalid,
	} {
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
	return decodeJSONLimit(w, r, dst, maxBodyBytes)
}

// decodeJSONLimit is decodeJSON with a tighter body limit for public routes.
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return fmt.Errorf("%w: Content-Type must be application/json", errBadRequest)
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: body is larger than %d bytes", errBadRequest, limit)
		}
		// encoding/json errors name fields and types, never the values.
		return fmt.Errorf("%w: %s", errBadRequest, strings.TrimPrefix(err.Error(), "json: "))
	}
	if dec.More() {
		return fmt.Errorf("%w: body must be a single JSON object", errBadRequest)
	}
	return nil
}

// channelProblems are the channel and Telegram errors with their answers.
var channelProblems = []struct {
	err  error
	prob Problem
}{
	{channels.ErrAlreadyConnected, Problem{
		Status: http.StatusConflict, Code: "channel_exists", Title: "Conflict",
		Detail: "this bot or account is already connected, here or in another workspace",
	}},
	{channels.ErrLoginStep, Problem{Status: http.StatusConflict, Code: "login_step", Title: "Conflict"}},
	{channels.ErrTooManyKeys, Problem{
		Status: http.StatusConflict, Code: "too_many_keys", Title: "Conflict", Detail: "20 ingest keys are in use, revoke one first",
	}},
	{channels.ErrLoginExpired, Problem{
		Status: http.StatusConflict, Code: "login_expired", Title: "Conflict", Detail: "the login has expired, start again",
	}},
	{channels.ErrBotToken, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_token_invalid", Title: "Unprocessable Content"}},
	{telegram.ErrPhoneInvalid, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_phone_invalid", Title: "Unprocessable Content"}},
	{telegram.ErrNoAccount, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_no_account", Title: "Unprocessable Content"}},
	{telegram.ErrBanned, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_banned", Title: "Unprocessable Content"}},
	{telegram.ErrCodeInvalid, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_code_invalid", Title: "Unprocessable Content"}},
	{telegram.ErrCodeExpired, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_code_expired", Title: "Unprocessable Content"}},
	{telegram.ErrPasswordInvalid, Problem{Status: http.StatusUnprocessableEntity, Code: "telegram_password_invalid", Title: "Unprocessable Content"}},
	{channels.ErrNoEncryption, Problem{
		Status: http.StatusServiceUnavailable, Code: "encryption_unavailable", Title: "Service Unavailable",
		Detail: "TOLKOVO_MASTER_KEY is not set on this server",
	}},
	{channels.ErrUserUnavailable, Problem{
		Status: http.StatusServiceUnavailable, Code: "telegram_user_unavailable", Title: "Service Unavailable",
		Detail: "TOLKOVO_TELEGRAM_API_ID and TOLKOVO_TELEGRAM_API_HASH are not set on this server",
	}},
}

// writeChannelError answers the errors of the channels module and reports
// whether err was one. Telegram outages are logged with their cause, which
// never holds tokens, phones or codes.
func (s *server) writeChannelError(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, c := range channelProblems {
		if errors.Is(err, c.err) {
			writeProblem(w, r, c.prob)
			return true
		}
	}
	if errors.Is(err, channels.ErrTelegram) {
		s.log.WarnContext(r.Context(), "telegram request failed",
			slog.String("request_id", middleware.GetReqID(r.Context())), slog.String("error", err.Error()))
		writeProblem(w, r, Problem{Status: http.StatusBadGateway, Code: "telegram_unavailable", Title: "Bad Gateway"})
		return true
	}
	return false
}
