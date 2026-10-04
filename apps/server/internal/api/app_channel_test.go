package api

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

func TestChannels_App(t *testing.T) {
	e := newEnv(t, envOptions{})
	c, s := e.register("owner@example.test", "zerno")

	rec := c.do(http.MethodPost, "/api/v1/channels/app", map[string]any{"name": "Зерно для Windows"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect app: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("secrets may be cached")
	}
	app := decode[oapi.AppChannelCreated](t, rec)
	ch, key := app.Channel, app.IngestKey
	if ch.Type != oapi.ChannelTypeApp || ch.Status != oapi.ChannelStatusActive || ch.Name != "Зерно для Windows" {
		t.Fatalf("channel: %+v", ch)
	}
	if l := ch.Details.AppLimits; l == nil || *l != (oapi.AppLimits{MaxFileMb: 25, MaxFiles: 5, MaxReportMb: 50, RetentionDays: 30, DailyReports: 500, DailyMb: 2000}) {
		t.Fatalf("default limits: %+v", ch.Details.AppLimits)
	}
	if !strings.HasPrefix(key.Secret, "tk_ingest_") || len(key.Secret) != 74 || !strings.HasPrefix(key.Secret, key.Key.Prefix) ||
		len(key.Key.Prefix) != 16 || key.Key.Name != "Зерно для Windows" || key.Key.RevokedAt != nil || key.Key.LastUsedAt != nil {
		t.Fatalf("first key: %+v", key)
	}
	if !strings.HasPrefix(app.IdentitySecret, "tk_identity_") || len(app.IdentitySecret) != 55 {
		t.Fatalf("identity secret: %q", app.IdentitySecret)
	}
	// Only the hash of the key is stored; the identity secret only sealed.
	hash := sha256.Sum256([]byte(key.Secret))
	if n := e.ownerCount("SELECT count(*) FROM ingest_keys WHERE key_hash = $1", hash[:]); n != 1 {
		t.Fatalf("hash rows: %d", n)
	}
	if n := e.ownerCount("SELECT count(*) FROM channel_secrets WHERE position($1::bytea IN sealed) > 0", []byte(app.IdentitySecret)); n != 0 {
		t.Fatal("the identity secret is stored in the clear")
	}

	keysPath := "/api/v1/channels/" + ch.Id.String() + "/ingest-keys"
	rec = c.do(http.MethodPost, keysPath, map[string]any{"name": "Сборка 2.4"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", rec.Code, rec.Body)
	}
	second := decode[oapi.IngestKeyCreated](t, rec)
	if second.Secret == key.Secret || second.Key.Name != "Сборка 2.4" {
		t.Fatalf("second key: %+v", second)
	}
	rec = c.do(http.MethodGet, keysPath, nil)
	if strings.Contains(rec.Body.String(), key.Secret) || strings.Contains(rec.Body.String(), second.Secret) {
		t.Fatal("the list returns keys")
	}
	keys := decode[oapi.IngestKeyList](t, rec)
	if len(keys.Items) != 2 || keys.Items[0].Id != second.Key.Id {
		t.Fatalf("keys, newest first: %+v", keys.Items)
	}

	if rec := c.do(http.MethodDelete, keysPath+"/"+key.Key.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := c.do(http.MethodDelete, keysPath+"/"+key.Key.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke again: %d %s", rec.Code, rec.Body)
	}
	keys = decode[oapi.IngestKeyList](t, c.do(http.MethodGet, keysPath, nil))
	if keys.Items[1].RevokedAt == nil || keys.Items[0].RevokedAt != nil {
		t.Fatalf("revoked: %+v", keys.Items)
	}
	wantProblem(t, c.do(http.MethodDelete, keysPath+"/"+ch.Id.String(), nil), http.StatusNotFound, "not_found")

	// Up to 20 keys in use; a revoked one does not count.
	for i := 2; i <= 20; i++ {
		if rec := c.do(http.MethodPost, keysPath, map[string]any{"name": "k"}); rec.Code != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	wantProblem(t, c.do(http.MethodPost, keysPath, map[string]any{"name": "k"}), http.StatusConflict, "too_many_keys")
	wantProblem(t, c.do(http.MethodPost, keysPath, map[string]any{"name": " "}), http.StatusBadRequest, "invalid_request")

	rec = c.do(http.MethodPost, "/api/v1/channels/"+ch.Id.String()+"/identity-secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	if got := decode[oapi.IdentitySecret](t, rec); got.IdentitySecret == app.IdentitySecret || !strings.HasPrefix(got.IdentitySecret, "tk_identity_") {
		t.Fatalf("rotated: %q", got.IdentitySecret)
	}

	limitsPath := "/api/v1/channels/" + ch.Id.String() + "/app-limits"
	limits := map[string]any{"max_file_mb": 10, "max_files": 3, "max_report_mb": 20, "retention_days": 90, "daily_reports": 100, "daily_mb": 500}
	rec = c.do(http.MethodPut, limitsPath, limits)
	if got := decode[oapi.Channel](t, rec); got.Details.AppLimits == nil || got.Details.AppLimits.RetentionDays != 90 || got.Details.AppLimits.MaxFiles != 3 {
		t.Fatalf("limits: %d %s", rec.Code, rec.Body)
	}
	limits["max_file_mb"] = 26
	wantProblem(t, c.do(http.MethodPut, limitsPath, limits), http.StatusBadRequest, "invalid_request")
	delete(limits, "max_file_mb")
	wantProblem(t, c.do(http.MethodPut, limitsPath, limits), http.StatusBadRequest, "invalid_request")
	if list := decode[oapi.ChannelList](t, c.do(http.MethodGet, "/api/v1/channels", nil)); list.Items[0].Details.AppLimits.MaxFileMb != 10 {
		t.Fatalf("limits kept: %+v", list.Items[0].Details.AppLimits)
	}

	// Key and limit endpoints are for app channels only.
	bot := decode[oapi.Channel](t, c.do(http.MethodPost, "/api/v1/channels/telegram-bot", map[string]any{"token": channelstest.BotToken}))
	wantProblem(t, c.do(http.MethodGet, "/api/v1/channels/"+bot.Id.String()+"/ingest-keys", nil), http.StatusNotFound, "not_found")
	wantProblem(t, c.do(http.MethodPost, "/api/v1/channels/"+bot.Id.String()+"/identity-secret", nil), http.StatusNotFound, "not_found")
	limits["max_file_mb"] = 10
	wantProblem(t, c.do(http.MethodPut, "/api/v1/channels/"+bot.Id.String()+"/app-limits", limits), http.StatusNotFound, "not_found")

	// An operator sees no keys and changes nothing.
	_, sop := e.register("operator@example.test", "operator-home")
	e.addMember(s.Workspace.Id, sop.User.Id, tenancy.RoleOperator)
	op, _ := e.login("operator@example.test")
	if rec := op.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": s.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("operator switch: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, op.do(http.MethodGet, keysPath, nil), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodPost, keysPath, map[string]any{"name": "k"}), http.StatusForbidden, "forbidden")
	wantProblem(t, op.do(http.MethodPost, "/api/v1/channels/app", map[string]any{"name": "x"}), http.StatusForbidden, "forbidden")

	// Another workspace does not see them.
	other, _ := e.register("other@example.test", "other")
	wantProblem(t, other.do(http.MethodGet, keysPath, nil), http.StatusNotFound, "not_found")
	wantProblem(t, other.do(http.MethodDelete, keysPath+"/"+second.Key.Id.String(), nil), http.StatusNotFound, "not_found")
	wantProblem(t, other.do(http.MethodPost, "/api/v1/channels/"+ch.Id.String()+"/identity-secret", nil), http.StatusNotFound, "not_found")

	// Disconnecting revokes every key.
	if rec := c.do(http.MethodDelete, "/api/v1/channels/"+ch.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
	if n := e.ownerCount("SELECT count(*) FROM ingest_keys WHERE channel_id = $1 AND revoked_at IS NULL", ch.Id); n != 0 {
		t.Fatalf("keys still in use: %d", n)
	}
	if n := e.ownerCount("SELECT count(*) FROM channel_secrets WHERE channel_id = $1", ch.Id); n != 0 {
		t.Fatal("the secret is kept")
	}
	wantProblem(t, c.do(http.MethodGet, keysPath, nil), http.StatusNotFound, "not_found")
}
