package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

const aiTestKey = "sk-ant-api03-secret-0123456789abcdef"

func haikuSettings(extra map[string]any) map[string]any {
	body := map[string]any{
		"enabled": true, "provider": "anthropic", "model": "claude-haiku-4-5", "base_url": nil,
		"prices":                map[string]any{"input_micros": 1000000, "output_micros": 5000000},
		"monthly_budget_micros": 10000000,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// Owners and admins set the provider, model, key, prices and budget; the
// key never comes back; operators see nothing; each workspace has its own.
func TestAI_Settings(t *testing.T) {
	in := setupInbox(t)
	wantProblem(t, in.op.do(http.MethodGet, "/api/v1/ai/settings", nil), http.StatusForbidden, "forbidden")
	wantProblem(t, in.op.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(nil)), http.StatusForbidden, "forbidden")
	wantProblem(t, in.op.do(http.MethodPost, "/api/v1/ai/settings/check", nil), http.StatusForbidden, "forbidden")
	wantProblem(t, in.e.client().do(http.MethodGet, "/api/v1/ai/settings", nil), http.StatusUnauthorized, "unauthenticated")

	got := decode[oapi.AISettings](t, in.owner.do(http.MethodGet, "/api/v1/ai/settings", nil))
	if got.Enabled || got.Provider != nil || got.Key != nil || len(got.Models) == 0 || !got.EncryptionAvailable || got.PeriodStart.Day() != 1 {
		t.Fatalf("defaults %+v", got)
	}

	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(nil)), http.StatusUnprocessableEntity, "ai_key_required")
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"provider": "gemini"})), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"base_url": "http://10.0.0.1/v1"})), http.StatusBadRequest, "invalid_request")
	wantProblem(t, in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"workspace_id": in.sop.Workspace.Id})), http.StatusBadRequest, "invalid_request")

	rec := in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"api_key": aiTestKey}))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "0123456789") {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	got = decode[oapi.AISettings](t, rec)
	if !got.Enabled || got.Key == nil || got.Key.Hint != "…cdef" || *got.Provider != oapi.Anthropic || *got.MonthlyBudgetMicros != 10000000 {
		t.Fatalf("saved %+v", got)
	}
	rec = in.owner.do(http.MethodGet, "/api/v1/ai/settings", nil)
	if strings.Contains(rec.Body.String(), "0123456789") || decode[oapi.AISettings](t, rec).Key.Hint != "…cdef" {
		t.Fatalf("read back: %s", rec.Body)
	}

	// The operator's own workspace has none of it.
	home := in.e.client()
	home.cookie, home.csrf = in.op.cookie, in.op.csrf
	if rec := home.do(http.MethodPost, "/api/v1/auth/workspace", map[string]any{"workspace_id": in.sop.Workspace.Id}); rec.Code != http.StatusOK {
		t.Fatalf("switch: %d %s", rec.Code, rec.Body)
	}
	if other := decode[oapi.AISettings](t, home.do(http.MethodGet, "/api/v1/ai/settings", nil)); other.Provider != nil || other.Key != nil {
		t.Fatalf("other workspace %+v", other)
	}
}

// The check answers what went wrong in words the panel can show.
func TestAI_Check(t *testing.T) {
	in := setupInbox(t)
	check := func() *httptest.ResponseRecorder {
		return in.owner.do(http.MethodPost, "/api/v1/ai/settings/check", nil)
	}
	wantProblem(t, check(), http.StatusConflict, "ai_not_configured")
	// Saved but off: the check still runs.
	if rec := in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"enabled": false, "api_key": aiTestKey})); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	in.e.llm.answer(fakeLLMAnswer{status: 200, body: `{"model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":1}}`})
	rec := check()
	if rec.Code != http.StatusOK || decode[oapi.AICheck](t, rec).Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}

	in.e.llm.answer(fakeLLMAnswer{status: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`})
	wantProblem(t, check(), http.StatusUnprocessableEntity, "ai_key_refused")

	in.e.llm.answer(fakeLLMAnswer{status: 404, body: `{"type":"error","error":{"type":"not_found_error","message":"model: claude-haiku-4-5"}}`})
	rec = check()
	wantProblem(t, rec, http.StatusUnprocessableEntity, "ai_request_rejected")
	if !strings.Contains(rec.Body.String(), "not_found_error") {
		t.Fatalf("no provider code: %s", rec.Body)
	}

	// Longer than a request can wait: no retry, the wait is passed on.
	in.e.llm.answer(fakeLLMAnswer{status: 429, headers: map[string]string{"Retry-After": "30"}, body: `{"type":"error","error":{"type":"rate_limit_error","message":"slow"}}`})
	rec = check()
	wantProblem(t, rec, http.StatusServiceUnavailable, "ai_rate_limited")
	if rec.Header().Get("Retry-After") != "30" {
		t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
	}

	in.e.llm.answer(fakeLLMAnswer{status: 500, body: `{}`}, fakeLLMAnswer{status: 500, body: `{}`})
	wantProblem(t, check(), http.StatusBadGateway, "ai_provider_unavailable")

	// The checks so far cost $0.000017; a budget below that stops the next one
	// before it reaches the provider.
	if rec := in.owner.do(http.MethodPut, "/api/v1/ai/settings", haikuSettings(map[string]any{"enabled": false, "monthly_budget_micros": 10})); rec.Code != http.StatusOK {
		t.Fatalf("budget: %d %s", rec.Code, rec.Body)
	}
	before := in.e.llm.calls()
	wantProblem(t, check(), http.StatusConflict, "ai_budget_exceeded")
	if in.e.llm.calls() != before {
		t.Fatal("the provider was called over budget")
	}
	if got := decode[oapi.AISettings](t, in.owner.do(http.MethodGet, "/api/v1/ai/settings", nil)); got.SpentMicros != 17 {
		t.Fatalf("spent %d", got.SpentMicros)
	}
}
