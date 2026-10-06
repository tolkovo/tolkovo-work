package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

func TestChannels_BotAndPersonalAccount(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("owner@example.test", "zerno")

	list := decode[oapi.ChannelList](t, c.do(http.MethodGet, "/api/v1/channels", nil))
	if len(list.Items) != 0 || !list.TelegramUserAvailable || !list.EncryptionAvailable {
		t.Fatalf("empty list: %+v", list)
	}

	rec := c.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": channelstest.BotToken})
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect bot: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), channelstest.BotToken) {
		t.Fatal("the token is returned")
	}
	bot := decode[oapi.Channel](t, rec)
	if bot.Type != oapi.ChannelTypeTelegramBot || bot.Status != oapi.ChannelStatusActive || deref(bot.Details.Username) != "shop_support_bot" ||
		bot.Details.Delivery == nil || *bot.Details.Delivery != oapi.Webhook || bot.ErrorCode != nil {
		t.Fatalf("bot: %+v", bot)
	}
	wantProblem(t, c.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": channelstest.BotToken}),
		http.StatusConflict, "channel_exists")
	wantProblem(t, c.do(http.MethodPost, "/api/v1/channels/telegram-bot",
		map[string]any{"token": "111111111:AAH-revoked-token-revoked-token-revoke"}), http.StatusUnprocessableEntity, "telegram_token_invalid")
	wantProblem(t, c.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": "nope"}),
		http.StatusBadRequest, "invalid_request")

	rec = c.do(http.MethodPost, "/api/v1/channels/telegram-user", map[string]any{"phone": "+7 900 123-45-67"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("start login: %d %s", rec.Code, rec.Body)
	}
	login := decode[oapi.TelegramLogin](t, rec)
	if login.Next != oapi.TelegramLoginNextCode || login.CodeVia == nil || *login.CodeVia != oapi.TelegramLoginCodeViaApp ||
		login.Channel.Status != oapi.ChannelStatusPending {
		t.Fatalf("login: %+v", login)
	}
	codePath := "/api/v1/channels/" + login.Channel.Id.String() + "/telegram-user/code"
	wantProblem(t, c.do(http.MethodPost, codePath, map[string]any{"code": "00000"}), http.StatusUnprocessableEntity, "telegram_code_invalid")
	wantProblem(t, c.do(http.MethodPost, "/api/v1/channels/"+login.Channel.Id.String()+"/telegram-user/password",
		map[string]any{"password": "x"}), http.StatusConflict, "login_step")
	rec = c.do(http.MethodPost, codePath, map[string]any{"code": channelstest.CodeOK})
	if rec.Code != http.StatusOK {
		t.Fatalf("code: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "9001234567") {
		t.Fatal("the full phone is returned")
	}
	done := decode[oapi.TelegramLogin](t, rec)
	if done.Next != oapi.TelegramLoginNextDone || done.Channel.Status != oapi.ChannelStatusActive || done.Channel.Name != "Марина Ковалёва" ||
		deref(done.Channel.Details.PhoneMasked) != "+79•••••••67" {
		t.Fatalf("done: %+v", done)
	}

	rec = c.do(http.MethodPatch, "/api/v1/channels/"+bot.Id.String(), map[string]any{"name": "Бот магазина"})
	if got := decode[oapi.Channel](t, rec); got.Name != "Бот магазина" {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if list := decode[oapi.ChannelList](t, c.do(http.MethodGet, "/api/v1/channels", nil)); len(list.Items) != 2 {
		t.Fatalf("list: %+v", list)
	}

	// An operator sees no channels and changes nothing.
	_, sop := e.register("operator@example.test", "operator-home")
	e.addMember(s.Workspace.Id, sop.User.Id, tenancy.RoleOperator)
	op, _ := e.login("operator@example.test")
	if rec := op.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": s.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("operator switch: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, op.do(http.MethodGet, "/api/v1/channels", nil), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodDelete, "/api/v1/channels/"+bot.Id.String(), nil), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/channels/telegram-user", map[string]any{"phone": "+79005550000"}),
		http.StatusForbidden, "forbidden")

	// Another workspace does not see them.
	other, _ := e.register("other@example.test", "other")
	wantProblem(t, other.do(http.MethodDelete, "/api/v1/channels/"+bot.Id.String(), nil), http.StatusNotFound, "not_found")

	if rec := c.do(http.MethodDelete, "/api/v1/channels/"+bot.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/channels/"+bot.Id.String(), nil), http.StatusNotFound, "not_found")
	c.csrf = ""
	wantProblem(t, c.do(http.MethodDelete, "/api/v1/channels/"+done.Channel.Id.String(), nil), http.StatusForbidden, "csrf_failed")
}

func TestHooks_TelegramBot(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("owner@example.test", "zerno")
	bot := decode[oapi.Channel](t, c.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": channelstest.BotToken}))
	secret := e.bots.Hooks[0].Secret
	path := "/hooks/telegram/" + s.Workspace.Id.String() + "/" + bot.Id.String()

	// Telegram's request: no session, no Origin, only the secret header.
	hook := func(path, secret, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	text := `{"update_id":1,"message":{"message_id":7,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":1,"text":"Здравствуйте"}}`
	sticker := `{"update_id":2,"message":{"message_id":8,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":2,"sticker":{"file_id":"s"}}}`

	if rec := hook(path, "", text); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no secret: %d", rec.Code)
	}
	if rec := hook(path, "wrong", text); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", rec.Code)
	}
	if rec := hook("/hooks/telegram/"+s.Workspace.Id.String()+"/"+s.Workspace.Id.String(), secret, text); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown channel: %d", rec.Code)
	}
	if rec := hook("/hooks/telegram/x/y", secret, text); rec.Code != http.StatusNotFound {
		t.Fatalf("bad ids: %d", rec.Code)
	}
	for _, u := range []string{text, text, sticker} {
		if rec := hook(path, secret, u); rec.Code != http.StatusOK {
			t.Fatalf("update: %d %s", rec.Code, rec.Body)
		}
	}

	convs := decode[oapi.ConversationPage](t, c.do(http.MethodGet, "/api/v1/conversations", nil))
	if len(convs.Items) != 1 || convs.Items[0].ChannelId == nil || *convs.Items[0].ChannelId != bot.Id {
		t.Fatalf("conversations: %+v", convs.Items)
	}
	msgs := decode[oapi.MessagePage](t, c.do(http.MethodGet, "/api/v1/conversations/"+convs.Items[0].Id.String()+"/messages", nil))
	if len(msgs.Items) != 2 || msgs.Items[1].Body != "Здравствуйте" || msgs.Items[0].Unsupported == nil ||
		*msgs.Items[0].Unsupported != oapi.MessageUnsupportedSticker || msgs.Items[1].Unsupported != nil || msgs.Items[1].SentOutsidePanel != nil {
		t.Fatalf("messages: %+v", msgs.Items)
	}
	list := decode[oapi.ChannelList](t, c.do(http.MethodGet, "/api/v1/channels", nil))
	if list.Items[0].LastInboundAt == nil {
		t.Fatalf("last_inbound_at: %+v", list.Items[0])
	}
}

func TestRetryMessageDelivery(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("owner@example.test", "zerno")
	bot := decode[oapi.Channel](t, c.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": channelstest.BotToken}))
	req := httptest.NewRequest(http.MethodPost, "/hooks/telegram/"+s.Workspace.Id.String()+"/"+bot.Id.String(),
		strings.NewReader(`{"update_id":1,"message":{"message_id":7,"from":{"id":70001,"is_bot":false,"first_name":"Пётр"},"chat":{"id":70001,"type":"private"},"date":1,"text":"Здравствуйте"}}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", e.bots.Hooks[0].Secret)
	e.h.ServeHTTP(httptest.NewRecorder(), req)
	conv := decode[oapi.ConversationPage](t, c.do(http.MethodGet, "/api/v1/conversations", nil)).Items[0]
	base := "/api/v1/conversations/" + conv.Id.String() + "/messages"

	m := decode[oapi.Message](t, c.do(http.MethodPost, base, map[string]any{"body": "Заказ в пути"}))
	if m.DeliveryStatus == nil || *m.DeliveryStatus != "pending" || m.DeliveryError != nil {
		t.Fatalf("a reply in a Telegram conversation is pending: %+v", m)
	}
	retry := base + "/" + m.Id.String() + "/retry"
	wantProblem(t, c.do(http.MethodPost, retry, nil), http.StatusConflict, "not_failed")

	e.ownerExec("UPDATE messages SET delivery_status = 'failed', delivery_error = 'blocked' WHERE id = $1", m.Id)
	feed := decode[oapi.MessagePage](t, c.do(http.MethodGet, base, nil))
	if feed.Items[0].DeliveryError == nil || *feed.Items[0].DeliveryError != oapi.MessageDeliveryErrorBlocked {
		t.Fatalf("the reason is shown: %+v", feed.Items[0])
	}
	again := decode[oapi.Message](t, c.do(http.MethodPost, retry, nil))
	if again.Id != m.Id || again.DeliveryStatus == nil || *again.DeliveryStatus != "pending" || again.DeliveryError != nil {
		t.Fatalf("retried: %+v", again)
	}
	wantProblem(t, c.do(http.MethodPost, base+"/"+conv.Id.String()+"/retry", nil), http.StatusNotFound, "not_found")
}
