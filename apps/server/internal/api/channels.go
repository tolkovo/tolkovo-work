package api

import (
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// Channels (docs/06 T-01, ADR-0014): a Telegram bot by token, a personal
// Telegram account by phone, code and cloud password.

// ListChannels implements GET /channels.
func (s *server) ListChannels(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ChannelsView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	list, err := s.channels.List(r.Context(), p.WorkspaceID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.Channel, 0, len(list))
	for _, ch := range list {
		items = append(items, channelBody(ch))
	}
	writeJSON(w, http.StatusOK, oapi.ChannelList{
		Items: items, TelegramUserAvailable: s.channels.UserAccountsAvailable(),
		EncryptionAvailable: s.channels.EncryptionAvailable(),
	})
}

// ConnectTelegramBot implements POST /channels/telegram-bot.
func (s *server) ConnectTelegramBot(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.ConnectTelegramBotRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	ch, err := s.channels.ConnectBot(r.Context(), p.WorkspaceID, p.UserID, req.Token, deref(req.Name))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, channelBody(ch))
}

// StartTelegramUserLogin implements POST /channels/telegram-user.
func (s *server) StartTelegramUserLogin(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.StartTelegramUserLoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	login, err := s.channels.StartUserLogin(r.Context(), p.WorkspaceID, p.UserID, req.Phone)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, loginBody(login))
}

// SubmitTelegramUserCode implements POST /channels/{id}/telegram-user/code.
func (s *server) SubmitTelegramUserCode(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.TelegramCodeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	login, err := s.channels.SubmitCode(r.Context(), p.WorkspaceID, id, req.Code)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, loginBody(login))
}

// SubmitTelegramUserPassword implements POST /channels/{id}/telegram-user/password.
func (s *server) SubmitTelegramUserPassword(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.TelegramPasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	login, err := s.channels.SubmitPassword(r.Context(), p.WorkspaceID, id, req.Password)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, loginBody(login))
}

// RenameChannel implements PATCH /channels/{id}.
func (s *server) RenameChannel(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.RenameChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	ch, err := s.channels.Rename(r.Context(), p.WorkspaceID, id, req.Name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, channelBody(ch))
}

// DisconnectChannel implements DELETE /channels/{id}.
func (s *server) DisconnectChannel(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.channels.Disconnect(r.Context(), p.WorkspaceID, id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func channelBody(ch channels.Channel) oapi.Channel {
	d := ch.Details
	body := oapi.Channel{
		Id: ch.ID, Type: oapi.ChannelType(ch.Type), Name: ch.Name, Status: oapi.ChannelStatus(ch.Status),
		ErrorCode: nonEmpty(ch.ErrorCode), CreatedAt: ch.CreatedAt,
		Details: oapi.ChannelDetails{
			Username: nonEmpty(d.Username), DisplayName: nonEmpty(d.DisplayName), PhoneMasked: nonEmpty(d.PhoneMasked),
		},
	}
	if d.Delivery != "" {
		v := oapi.ChannelDetailsDelivery(d.Delivery)
		body.Details.Delivery = &v
	}
	return body
}

func loginBody(l channels.Login) oapi.TelegramLogin {
	body := oapi.TelegramLogin{Channel: channelBody(l.Channel), Next: oapi.TelegramLoginNext(l.Next)}
	if l.CodeVia != "" {
		v := oapi.TelegramLoginCodeVia(l.CodeVia)
		body.CodeVia = &v
	}
	return body
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
