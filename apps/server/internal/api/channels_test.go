package api

import (
	"net/http"
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
	if bot.Type != oapi.TelegramBot || bot.Status != oapi.ChannelStatusActive || deref(bot.Details.Username) != "shop_support_bot" ||
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
	if login.Next != oapi.Code || login.CodeVia == nil || *login.CodeVia != oapi.App ||
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
	if done.Next != oapi.Done || done.Channel.Status != oapi.ChannelStatusActive || done.Channel.Name != "Марина Ковалёва" ||
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
