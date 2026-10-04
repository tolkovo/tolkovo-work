package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
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
