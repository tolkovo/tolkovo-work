package api

import (
	"errors"
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// Telegram notifications of members (TK-02, ADR-0015): the workspace's
// notifications bot and each member's link to it.

// notifyProblems are the Telegram errors of the notifications module with their answers.
var notifyProblems = []struct {
	err  error
	prob Problem
}{
	{notifications.ErrBotToken, Problem{
		Status: http.StatusUnprocessableEntity, Code: "telegram_token_invalid", Title: "Unprocessable Content",
		Detail: "Telegram does not know the token",
	}},
	{notifications.ErrNoEncryption, Problem{
		Status: http.StatusServiceUnavailable, Code: "encryption_unavailable", Title: "Service Unavailable",
		Detail: "TOLKOVO_MASTER_KEY is not set",
	}},
	{notifications.ErrNoBot, Problem{Status: http.StatusConflict, Code: "no_notify_bot", Title: "Conflict"}},
	{notifications.ErrBotBusy, Problem{
		Status: http.StatusConflict, Code: "notify_bot_busy", Title: "Conflict",
		Detail: "the bot's updates go to a webhook or another reader",
	}},
	{notifications.ErrTelegram, Problem{Status: http.StatusBadGateway, Code: "telegram_unavailable", Title: "Bad Gateway"}},
}

// writeNotifyError answers the Telegram errors of the notifications module; false for others.
func (s *server) writeNotifyError(w http.ResponseWriter, r *http.Request, err error) bool {
	for _, c := range notifyProblems {
		if errors.Is(err, c.err) {
			writeProblem(w, r, c.prob)
			return true
		}
	}
	return false
}

// GetTaskTelegramBot implements GET /task-settings/telegram-bot.
func (s *server) GetTaskTelegramBot(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	st, err := s.notifications.TelegramState(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, botStateBody(st))
}

// SetTaskTelegramBot implements PUT /task-settings/telegram-bot.
func (s *server) SetTaskTelegramBot(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	var req oapi.TelegramBotInput
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	if _, err := s.notifications.SetBot(r.Context(), p.WorkspaceID, p.UserID, deref(req.Token)); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.GetTaskTelegramBot(w, r)
}

// DeleteTaskTelegramBot implements DELETE /task-settings/telegram-bot.
func (s *server) DeleteTaskTelegramBot(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksManage)
	if !ok {
		return
	}
	if err := s.notifications.DeleteBot(r.Context(), p.WorkspaceID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetMyTelegram implements GET /me/telegram.
func (s *server) GetMyTelegram(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	got, err := s.notifications.MyTelegram(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, myTelegramBody(got))
}

// UnlinkMyTelegram implements DELETE /me/telegram.
func (s *server) UnlinkMyTelegram(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	if err := s.notifications.Unlink(r.Context(), p.WorkspaceID, p.UserID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StartMyTelegramLink implements POST /me/telegram/link.
func (s *server) StartMyTelegramLink(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	l, err := s.notifications.StartLink(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.TelegramLinkStart{Url: l.URL, ExpiresAt: l.ExpiresAt})
}

// CheckMyTelegramLink implements POST /me/telegram/check.
func (s *server) CheckMyTelegramLink(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r)
	if !ok {
		return
	}
	got, err := s.notifications.CheckLink(r.Context(), p.WorkspaceID, p.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, myTelegramBody(got))
}

// principal is the signed-in caller, for routes that need a session only.
func (s *server) principal(w http.ResponseWriter, r *http.Request) (authz.Principal, bool) {
	p, ok := authz.PrincipalFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
	}
	return p, ok
}

func botStateBody(st notifications.TelegramState) oapi.TelegramBotState {
	out := oapi.TelegramBotState{EncryptionAvailable: st.EncryptionAvailable}
	if st.Bot != nil {
		out.Bot = &struct {
			Username string `json:"username"`
		}{Username: st.Bot.Username}
	}
	return out
}

func myTelegramBody(m notifications.MyTelegram) oapi.MyTelegram {
	out := oapi.MyTelegram{Linked: m.Linked, LinkedAt: m.LinkedAt, Pending: m.Pending}
	if m.BotUsername != "" {
		out.BotUsername = &m.BotUsername
	}
	if m.Name != "" {
		out.Name = &m.Name
	}
	return out
}
