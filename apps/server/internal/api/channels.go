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

// SetWidgetAnalytics implements PUT /channels/{id}/widget-analytics.
func (s *server) SetWidgetAnalytics(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.WidgetAnalytics
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	ch, err := s.channels.SetWidgetAnalytics(r.Context(), p.WorkspaceID, id, channels.WidgetAnalytics{
		DataLayer: req.DataLayer, Gtag: req.Gtag, MetrikaCounter: deref(req.MetrikaCounter),
		Goals: channels.WidgetGoals{ChatStarted: req.Goals.ChatStarted, ContactCollected: req.Goals.ContactCollected, Rated: req.Goals.Rated},
	})
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
		ErrorCode: nonEmpty(ch.ErrorCode), LastInboundAt: ch.LastInboundAt, CreatedAt: ch.CreatedAt,
		Details: oapi.ChannelDetails{
			Username: nonEmpty(d.Username), DisplayName: nonEmpty(d.DisplayName), PhoneMasked: nonEmpty(d.PhoneMasked),
		},
	}
	if d.Delivery != "" {
		v := oapi.ChannelDetailsDelivery(d.Delivery)
		body.Details.Delivery = &v
	}
	if l := d.AppLimits; l != nil {
		body.Details.AppLimits = &oapi.AppLimits{
			MaxFileMb: l.MaxFileMB, MaxFiles: l.MaxFiles, MaxReportMb: l.MaxReportMB,
			RetentionDays: l.RetentionDays, DailyReports: l.DailyReports, DailyMb: l.DailyMB,
		}
	}
	if ch.Type == channels.TypeWidget {
		origins := d.AllowedOrigins
		if origins == nil {
			origins = []string{}
		}
		body.Details.WidgetKey, body.Details.AllowedOrigins = &d.WidgetKey, &origins
		if d.Analytics != nil {
			a := widgetAnalyticsBody(*d.Analytics)
			body.Details.Analytics = &a
		}
	}
	return body
}

func widgetAnalyticsBody(a channels.WidgetAnalytics) oapi.WidgetAnalytics {
	return oapi.WidgetAnalytics{
		DataLayer: a.DataLayer, Gtag: a.Gtag, MetrikaCounter: nonEmpty(a.MetrikaCounter),
		Goals: oapi.WidgetGoals{ChatStarted: a.Goals.ChatStarted, ContactCollected: a.Goals.ContactCollected, Rated: a.Goals.Rated},
	}
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

// App channel (AP-01, docs/02 §11.6): reports from a company app.

// ConnectAppChannel implements POST /channels/app.
func (s *server) ConnectAppChannel(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.ConnectAppChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	app, err := s.channels.ConnectApp(r.Context(), p.WorkspaceID, p.UserID, req.Name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.AppChannelCreated{
		Channel: channelBody(app.Channel), IngestKey: newKeyBody(app.Key), IdentitySecret: app.IdentitySecret.Reveal(),
	})
}

// ListIngestKeys implements GET /channels/{id}/ingest-keys.
func (s *server) ListIngestKeys(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsView)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	keys, err := s.channels.IngestKeys(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	items := make([]oapi.IngestKey, 0, len(keys))
	for _, k := range keys {
		items = append(items, keyBody(k))
	}
	writeJSON(w, http.StatusOK, oapi.IngestKeyList{Items: items})
}

// CreateIngestKey implements POST /channels/{id}/ingest-keys.
func (s *server) CreateIngestKey(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.CreateIngestKeyRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	key, err := s.channels.CreateIngestKey(r.Context(), p.WorkspaceID, p.UserID, id, req.Name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, newKeyBody(key))
}

// RevokeIngestKey implements DELETE /channels/{id}/ingest-keys/{key_id}.
func (s *server) RevokeIngestKey(w http.ResponseWriter, r *http.Request, id oapi.Id, keyID oapi.KeyId) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.channels.RevokeIngestKey(r.Context(), p.WorkspaceID, id, keyID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RotateIdentitySecret implements POST /channels/{id}/identity-secret.
func (s *server) RotateIdentitySecret(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	secret, err := s.channels.RotateIdentitySecret(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, oapi.IdentitySecret{IdentitySecret: secret.Reveal()})
}

// SetAppLimits implements PUT /channels/{id}/app-limits.
func (s *server) SetAppLimits(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.AppLimits
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	ch, err := s.channels.SetAppLimits(r.Context(), p.WorkspaceID, id, channels.AppLimits{
		MaxFileMB: req.MaxFileMb, MaxFiles: req.MaxFiles, MaxReportMB: req.MaxReportMb,
		RetentionDays: req.RetentionDays, DailyReports: req.DailyReports, DailyMB: req.DailyMb,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, channelBody(ch))
}

func keyBody(k channels.IngestKey) oapi.IngestKey {
	return oapi.IngestKey{Id: k.ID, Name: k.Name, Prefix: k.Prefix, CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt}
}

func newKeyBody(k channels.NewIngestKey) oapi.IngestKeyCreated {
	return oapi.IngestKeyCreated{Key: keyBody(k.Key), Secret: k.Secret.Reveal()}
}

// Widget channel (W-01, docs/04 §6): a chat on a site or in an app.

// ConnectWidgetChannel implements POST /channels/widget.
func (s *server) ConnectWidgetChannel(w http.ResponseWriter, r *http.Request) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.ConnectWidgetChannelRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	created, err := s.channels.ConnectWidget(r.Context(), p.WorkspaceID, p.UserID, req.Name, deref(req.AllowedOrigins))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.WidgetChannelCreated{
		Channel: channelBody(created.Channel), IdentitySecret: created.IdentitySecret.Reveal(),
	})
}

// SetWidgetOrigins implements PUT /channels/{id}/widget-origins.
func (s *server) SetWidgetOrigins(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, err := authz.Require(r.Context(), authz.ChannelsManage)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var req oapi.WidgetOriginsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	ch, err := s.channels.SetWidgetOrigins(r.Context(), p.WorkspaceID, id, req.AllowedOrigins)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, channelBody(ch))
}
