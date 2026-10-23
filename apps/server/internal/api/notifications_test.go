package api

import (
	"net/http"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

func TestNotificationSettings_GetAndPatch(t *testing.T) {
	e := newEnv(t, envOptions{mail: true})
	c, _ := e.register("marina@example.test", "zerno")

	got := decode[oapi.NotificationSettings](t, c.do(http.MethodGet, "/api/v1/me/notifications", nil))
	if got != (oapi.NotificationSettings{
		EmailMissed: true, EmailDelayMinutes: 15, EmailAvailable: true, TelegramAssigned: true, TelegramMessages: true, TelegramQueue: true,
	}) {
		t.Fatalf("defaults: %+v", got)
	}
	rec := c.do(http.MethodPatch, "/api/v1/me/notifications", map[string]any{"email_delay_minutes": 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.NotificationSettings](t, rec); got.EmailDelayMinutes != 5 || !got.EmailMissed {
		t.Fatalf("after patch: %+v", got)
	}
	c.do(http.MethodPatch, "/api/v1/me/notifications", map[string]any{"email_missed": false})
	got = decode[oapi.NotificationSettings](t, c.do(http.MethodGet, "/api/v1/me/notifications", nil))
	if got.EmailMissed || got.EmailDelayMinutes != 5 {
		t.Fatalf("stored: %+v", got)
	}

	for name, body := range map[string]map[string]any{
		"delay not offered": {"email_delay_minutes": 7},
		"wrong type":        {"email_missed": "yes"},
		"unknown field":     {"user_id": "someone"},
	} {
		t.Run(name, func(t *testing.T) {
			wantProblem(t, c.do(http.MethodPatch, "/api/v1/me/notifications", body), http.StatusBadRequest, "invalid_request")
		})
	}
	c.csrf = ""
	wantProblem(t, c.do(http.MethodPatch, "/api/v1/me/notifications", map[string]any{"email_missed": true}),
		http.StatusForbidden, "csrf_failed")
}

func TestNotificationSettings_WithoutEmail(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, _ := e.register("marina@example.test", "zerno")
	got := decode[oapi.NotificationSettings](t, c.do(http.MethodGet, "/api/v1/me/notifications", nil))
	if got.EmailAvailable {
		t.Fatalf("email_available without SMTP: %+v", got)
	}
	anon := e.client()
	wantProblem(t, anon.do(http.MethodGet, "/api/v1/me/notifications", nil), http.StatusUnauthorized, "unauthenticated")
}
