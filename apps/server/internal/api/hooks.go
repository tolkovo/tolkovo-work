package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/integrations"
)

// maxHookBody bounds a webhook update; Telegram's are a few kilobytes.
const maxHookBody = 1 << 20

// mountHooks serves the updates channels push to the server (docs/02
// §11.2). They come from Telegram, not from browsers: no session, no CSRF
// check; the secret token in the header proves the sender.
func mountHooks(r chi.Router, chans *channels.Service, log *slog.Logger) {
	r.Post("/hooks/telegram/{workspace}/{channel}", func(w http.ResponseWriter, r *http.Request) {
		ws, err1 := uuid.Parse(chi.URLParam(r, "workspace"))
		ch, err2 := uuid.Parse(chi.URLParam(r, "channel"))
		if err1 != nil || err2 != nil {
			writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHookBody))
		if err != nil {
			writeProblem(w, r, Problem{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Title: "Request Entity Too Large"})
			return
		}
		err = chans.BotWebhook(r.Context(), ws, ch, r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), body)
		switch {
		case err == nil:
			w.WriteHeader(http.StatusOK)
		case errors.Is(err, channels.ErrNotFound):
			writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		case errors.Is(err, channels.ErrHookSecret):
			writeProblem(w, r, Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Title: "Unauthorized"})
		default:
			// Telegram retries the update; deduplication makes that safe.
			log.Error("telegram webhook", "workspace_id", ws, "channel_id", ch, "error", err)
			writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
		}
	})
}

// maxGitHubHookBody bounds a GitHub webhook; GitHub caps payloads at 25 MB,
// but an "issues" event is tens of kilobytes even with a long issue text.
const maxGitHubHookBody = 1 << 20

// mountGitHubHook serves the webhook of a workspace's GitHub App (GH-03,
// ADR-0012). The workspace in the path scopes the lookup (RLS), the
// signature under the integration's secret proves GitHub sent it. Answers
// carry no detail: an unknown integration and a bad signature say only 404
// and 401.
func mountGitHubHook(r chi.Router, integ *integrations.Service, log *slog.Logger) {
	r.Post("/hooks/github/{workspace}/{integration}", func(w http.ResponseWriter, r *http.Request) {
		ws, err1 := uuid.Parse(chi.URLParam(r, "workspace"))
		id, err2 := uuid.Parse(chi.URLParam(r, "integration"))
		if err1 != nil || err2 != nil {
			writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGitHubHookBody))
		if err != nil {
			writeProblem(w, r, Problem{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Title: "Request Entity Too Large"})
			return
		}
		err = integ.GitHubHook(r.Context(), ws, id, integrations.Hook{
			Event: r.Header.Get("X-GitHub-Event"), Delivery: r.Header.Get("X-GitHub-Delivery"),
			Signature: r.Header.Get("X-Hub-Signature-256"), Body: body,
		})
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, integrations.ErrNotFound):
			writeProblem(w, r, Problem{Status: http.StatusNotFound, Code: "not_found", Title: "Not Found"})
		case errors.Is(err, integrations.ErrHookSignature):
			writeProblem(w, r, Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Title: "Unauthorized"})
		case errors.Is(err, integrations.ErrInvalid):
			writeProblem(w, r, Problem{Status: http.StatusBadRequest, Code: "invalid_request", Title: "Bad Request"})
		default:
			// GitHub does not retry; the delivery can be redelivered from the
			// App's settings, deduplication makes that safe.
			log.Error("github webhook", "workspace_id", ws, "integration_id", id, "error", err)
			writeProblem(w, r, Problem{Status: http.StatusInternalServerError, Code: "internal", Title: "Internal Server Error"})
		}
	})
}
