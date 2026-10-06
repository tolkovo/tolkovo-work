package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/ai/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/config"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

const anthropicKey = "sk-ant-api03-0123456789abcdef"

// fakeProvider plays Anthropic, OpenAI and DeepSeek: it answers with the
// next recorded answer and keeps the requests.
type fakeProvider struct {
	mu       sync.Mutex
	answers  []fakeAnswer
	requests []map[string]any
	headers  []http.Header
	paths    []string
}

type fakeAnswer struct {
	status int
	body   string
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.requests = append(f.requests, body)
	f.headers = append(f.headers, r.Header.Clone())
	f.paths = append(f.paths, r.URL.Path)
	a := fakeAnswer{status: 500, body: `{}`}
	if len(f.answers) > 0 {
		a, f.answers = f.answers[0], f.answers[1:]
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

func (f *fakeProvider) answer(a ...fakeAnswer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, a...)
}

func (f *fakeProvider) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func anthropicText(text string, in, out int) fakeAnswer {
	b, _ := json.Marshal(map[string]any{
		"type": "message", "model": "claude-haiku-4-5-20251001", "stop_reason": "end_turn",
		"content": []map[string]any{{"type": "text", "text": text}},
		"usage":   map[string]int{"input_tokens": in, "output_tokens": out},
	})
	return fakeAnswer{status: 200, body: string(b)}
}

type fixture struct {
	t        *testing.T
	ctx      context.Context
	app      *db.DB
	data     dbtest.Database
	svc      *Service
	provider *fakeProvider
	ws, ws2  uuid.UUID
	owner    uuid.UUID
}

func setup(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.New(t)
	app := d.OpenApp(t)
	keys, err := crypto.ParseLocalKeys(config.DevMasterKey, 1, "")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	sealer, err := crypto.NewSealer(keys)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	fake := &fakeProvider{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	svc := New(app, Options{
		Sealer: sealer, HTTP: http.DefaultClient, AnthropicURL: srv.URL, OpenAIURL: srv.URL + "/v1", DeepSeekURL: srv.URL + "/deepseek",
		AllowPrivate: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	})
	svc.retryWait = 10 * time.Millisecond
	f := &fixture{t: t, ctx: context.Background(), app: app, data: d, svc: svc, provider: fake}
	tenants := tenancy.New(app)
	f.owner = f.user("owner")
	for i, slug := range []string{"zerno", "drugie"} {
		ws, err := tenants.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: slug, Slug: slug, OwnerID: f.owner})
		if err != nil {
			t.Fatalf("create workspace: %v", err)
		}
		if i == 0 {
			f.ws = ws.ID
		} else {
			f.ws2 = ws.ID
		}
	}
	return f
}

func (f *fixture) user(name string) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	err := f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, "INSERT INTO users (id, email, name) VALUES ($1, $2, $3)", id, name+"@example.test", name)
		return err
	})
	if err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	return id
}

func key(v string) *crypto.Secret {
	s := crypto.NewSecret(v)
	return &s
}

func budget(usd float64) *int64 {
	v := int64(usd * 1e6)
	return &v
}

// haiku turns on Anthropic's Haiku at its list price in ws.
func (f *fixture) haiku(ws uuid.UUID, limit *int64) Settings {
	f.t.Helper()
	got, err := f.svc.UpdateSettings(f.ctx, ws, f.owner, SettingsUpdate{
		Enabled: true, Provider: llm.Anthropic, Model: "claude-haiku-4-5", Key: key(anthropicKey),
		Prices: Prices{InputMicros: 1_000_000, OutputMicros: 5_000_000}, MonthlyBudgetMicros: limit,
	})
	if err != nil {
		f.t.Fatalf("update settings: %v", err)
	}
	return got
}

func (f *fixture) loggedCalls(ws uuid.UUID) []store.AiCall {
	f.t.Helper()
	var out []store.AiCall
	err := f.app.InTenantTx(f.ctx, ws, func(tx pgx.Tx) error {
		var err error
		out, err = store.New(tx).ListCalls(f.ctx, store.ListCallsParams{WorkspaceID: ws, Lim: 100})
		return err
	})
	if err != nil {
		f.t.Fatalf("list calls: %v", err)
	}
	return out
}

func ask(text string) Call {
	return Call{Purpose: "test", System: "You help.", MaxTokens: 100, Messages: []llm.Message{{Role: llm.User, Text: text}}}
}

func TestSettings_KeyIsSealedAndHinted(t *testing.T) {
	f := setup(t)
	got, err := f.svc.Settings(f.ctx, f.ws)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if got.Enabled || got.Provider != "" || got.KeyHint != "" || got.SpentMicros != 0 || got.PeriodStart.Day() != 1 {
		t.Fatalf("defaults %+v", got)
	}
	got = f.haiku(f.ws, budget(10))
	if !got.Enabled || got.KeyHint != "…cdef" || got.KeySetAt == nil || *got.MonthlyBudgetMicros != 10_000_000 {
		t.Fatalf("saved %+v", got)
	}
	// The stored key is sealed: the database never holds it in the clear.
	err = f.app.InTenantTx(f.ctx, f.ws, func(tx pgx.Tx) error {
		var sealed []byte
		if err := tx.QueryRow(f.ctx, "SELECT sealed FROM ai_credentials").Scan(&sealed); err != nil {
			return err
		}
		if strings.Contains(string(sealed), "0123456789abcdef") {
			t.Error("the key is stored in the clear")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read key: %v", err)
	}

	// Saving without a key keeps it; another provider forgets it.
	got, err = f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{Provider: llm.Anthropic, Model: "claude-sonnet-4-5"})
	if err != nil || got.KeyHint != "…cdef" || got.Enabled {
		t.Fatalf("keep key: %+v %v", got, err)
	}
	_, err = f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{Enabled: true, Provider: llm.OpenAI, Model: "gpt-4.1-mini"})
	if !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("openai without a key: %v", err)
	}
	got, err = f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{Provider: llm.OpenAI, Model: "gpt-4.1-mini"})
	if err != nil || got.KeyHint != "" {
		t.Fatalf("switch provider: %+v %v", got, err)
	}
	// An OpenAI-compatible server may go without a key.
	got, err = f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{
		Enabled: true, Provider: llm.OpenAICompatible, Model: "qwen2.5:7b", BaseURL: "http://127.0.0.1:11434/v1/",
	})
	if err != nil || got.BaseURL != "http://127.0.0.1:11434/v1" || !got.Enabled {
		t.Fatalf("compatible: %+v %v", got, err)
	}
	// The other workspace sees none of it.
	other, err := f.svc.Settings(f.ctx, f.ws2)
	if err != nil || other.Provider != "" || other.KeyHint != "" {
		t.Fatalf("other workspace: %+v %v", other, err)
	}
}

func TestSettings_Validation(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		name string
		in   SettingsUpdate
	}{
		{"unknown provider", SettingsUpdate{Provider: "gemini", Model: "x"}},
		{"on without provider", SettingsUpdate{Enabled: true}},
		{"on without model", SettingsUpdate{Enabled: true, Provider: llm.Anthropic, Key: key(anthropicKey)}},
		{"bad model", SettingsUpdate{Provider: llm.Anthropic, Model: "claude haiku"}},
		{"base_url for anthropic", SettingsUpdate{Provider: llm.Anthropic, Model: "m", BaseURL: "https://example.com"}},
		{"base_url for deepseek", SettingsUpdate{Provider: llm.DeepSeek, Model: "deepseek-chat", BaseURL: "https://api.deepseek.com"}},
		{"compatible without base_url", SettingsUpdate{Provider: llm.OpenAICompatible, Model: "m"}},
		{"private base_url", SettingsUpdate{Provider: llm.OpenAICompatible, Model: "m", BaseURL: "http://10.0.0.5/v1"}},
		{"metadata base_url", SettingsUpdate{Provider: llm.OpenAICompatible, Model: "m", BaseURL: "http://169.254.169.254/v1"}},
		{"base_url with password", SettingsUpdate{Provider: llm.OpenAICompatible, Model: "m", BaseURL: "https://u:p@example.com/v1"}},
		{"key with a space", SettingsUpdate{Provider: llm.Anthropic, Model: "m", Key: key("sk ant")}},
		{"empty key", SettingsUpdate{Provider: llm.Anthropic, Model: "m", Key: key("")}},
		{"negative price", SettingsUpdate{Provider: llm.Anthropic, Model: "m", Prices: Prices{InputMicros: -1}}},
		{"huge price", SettingsUpdate{Provider: llm.Anthropic, Model: "m", Prices: Prices{OutputMicros: MaxPriceMicros + 1}}},
		{"negative budget", SettingsUpdate{Provider: llm.Anthropic, Model: "m", MonthlyBudgetMicros: budget(-1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.UpdateSettings(f.ctx, f.ws, f.owner, tc.in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestComplete_MasksPersonalDataAndRecordsTheCall(t *testing.T) {
	f := setup(t)
	f.haiku(f.ws, nil)
	f.provider.answer(anthropicText("Напишем на ⟨EMAIL_1⟩, карту ⟨CARD_1⟩ не просим", 1200, 300))
	res, err := f.svc.Complete(f.ctx, f.ws, ask("Я anna@example.com, карта 4111 1111 1111 1111, не работает вход"))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	sent := f.provider.requests[0]
	raw, _ := json.Marshal(sent)
	if strings.Contains(string(raw), "anna@example.com") || strings.Contains(string(raw), "4111") || !strings.Contains(string(raw), "⟨EMAIL_1⟩") {
		t.Fatalf("personal data reached the provider: %s", raw)
	}
	if f.provider.headers[0].Get("x-api-key") != anthropicKey {
		t.Fatal("the key was not sent")
	}
	if got := res.Unmask(res.Text); got != "Напишем на anna@example.com, карту ⟨CARD_1⟩ не просим" {
		t.Fatalf("unmasked %q", got)
	}
	// 1200 × $1 + 300 × $5 per million tokens = $0.0027.
	if res.CostMicros != 2700 || res.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("result %+v", res)
	}
	logged := f.loggedCalls(f.ws)
	if len(logged) != 1 {
		t.Fatalf("calls %d", len(logged))
	}
	c := logged[0]
	if c.Status != "ok" || c.Purpose != "test" || c.Provider != "anthropic" || c.InputTokens != 1200 || c.OutputTokens != 300 || c.CostMicros != 2700 {
		t.Fatalf("logged %+v", c)
	}
	if strings.Contains(string(c.RequestRedacted), "anna@") || strings.Contains(string(c.RequestRedacted), anthropicKey) || !strings.Contains(string(c.ResponseRedacted), "⟨EMAIL_1⟩") {
		t.Fatalf("logged texts %s / %s", c.RequestRedacted, c.ResponseRedacted)
	}
	settings, err := f.svc.Settings(f.ctx, f.ws)
	if err != nil || settings.SpentMicros != 2700 {
		t.Fatalf("spent %+v %v", settings, err)
	}
	if other := f.loggedCalls(f.ws2); len(other) != 0 {
		t.Fatalf("the other workspace sees %d calls", len(other))
	}
}

func TestComplete_StopsAtTheBudget(t *testing.T) {
	f := setup(t)
	f.haiku(f.ws, budget(0.002))
	f.provider.answer(anthropicText("ok", 1000, 200)) // $0.002
	if _, err := f.svc.Complete(f.ctx, f.ws, ask("one")); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := f.svc.Complete(f.ctx, f.ws, ask("two")); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("over budget: %v", err)
	}
	if n := f.provider.calls(); n != 1 {
		t.Fatalf("the provider got %d requests", n)
	}
	// The other workspace has its own budget.
	f.haiku(f.ws2, budget(0.002))
	f.provider.answer(anthropicText("ok", 1, 1))
	if _, err := f.svc.Complete(f.ctx, f.ws2, ask("three")); err != nil {
		t.Fatalf("other workspace: %v", err)
	}
}

func TestComplete_NeedsAISwitchedOn(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Complete(f.ctx, f.ws, ask("hi")); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nothing set up: %v", err)
	}
	if _, err := f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{Provider: llm.Anthropic, Model: "claude-haiku-4-5", Key: key(anthropicKey)}); err != nil {
		t.Fatalf("save off: %v", err)
	}
	if _, err := f.svc.Complete(f.ctx, f.ws, ask("hi")); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("switched off: %v", err)
	}
	// The check works before AI is switched on; a cut-off answer still proves the key.
	f.provider.answer(fakeAnswer{status: 200, body: `{"model":"claude-haiku-4-5","content":[{"type":"text","text":"O"}],"stop_reason":"max_tokens","usage":{"input_tokens":12,"output_tokens":16}}`})
	got, err := f.svc.Check(f.ctx, f.ws)
	if err != nil || got.Model != "claude-haiku-4-5" {
		t.Fatalf("check: %+v %v", got, err)
	}
	if c := f.loggedCalls(f.ws); len(c) != 1 || c[0].Purpose != PurposeConnectionCheck || c[0].Status != "truncated" {
		t.Fatalf("logged %+v", c)
	}
	if sys := f.provider.requests[0]["system"]; sys != "Reply with the single word OK.\n" {
		t.Fatalf("system %q", sys)
	}
}

func TestComplete_RetriesOnceOnOutage(t *testing.T) {
	f := setup(t)
	f.haiku(f.ws, nil)
	f.provider.answer(fakeAnswer{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`}, anthropicText("ok", 10, 2))
	if _, err := f.svc.Complete(f.ctx, f.ws, ask("hi")); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if n := f.provider.calls(); n != 2 {
		t.Fatalf("requests %d", n)
	}
	// A refused key is not retried and is logged by its kind and code only.
	f.provider.answer(fakeAnswer{status: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key sk-ant-api03-0123"}}`})
	_, err := f.svc.Complete(f.ctx, f.ws, ask("hi"))
	if !errors.Is(err, llm.ErrAuth) || f.provider.calls() != 3 {
		t.Fatalf("auth: %v after %d requests", err, f.provider.calls())
	}
	logged := f.loggedCalls(f.ws)
	if len(logged) != 2 || logged[0].Status != "error" || logged[0].ErrorCode == nil || *logged[0].ErrorCode != "auth:authentication_error" {
		t.Fatalf("logged %+v", logged)
	}
	// Two outages in a row give up.
	f.provider.answer(fakeAnswer{status: 503, body: `{}`}, fakeAnswer{status: 503, body: `{}`})
	if _, err := f.svc.Complete(f.ctx, f.ws, ask("hi")); !errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("outage: %v", err)
	}
}

func TestComplete_OpenAIWithSchema(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{
		Enabled: true, Provider: llm.OpenAI, Model: "gpt-4.1-mini", Key: key("sk-proj-abcdefghijklmnop"),
		Prices: Prices{InputMicros: 400_000, OutputMicros: 1_600_000},
	}); err != nil {
		t.Fatalf("settings: %v", err)
	}
	f.provider.answer(fakeAnswer{status: 200, body: `{"model":"gpt-4.1-mini","choices":[{"message":{"content":"{\"title\":\"Вход не работает\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`})
	call := ask("Не могу войти")
	call.Schema = &llm.Schema{Name: "draft", JSON: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)}
	res, err := f.svc.Complete(f.ctx, f.ws, call)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if string(res.JSON) != `{"title":"Вход не работает"}` || res.CostMicros != 56 {
		t.Fatalf("result %+v", res)
	}
	if f.provider.headers[0].Get("Authorization") != "Bearer sk-proj-abcdefghijklmnop" {
		t.Fatal("no bearer key")
	}
}

func TestComplete_DeepSeek(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{Enabled: true, Provider: llm.DeepSeek, Model: "deepseek-chat"}); !errors.Is(err, ErrKeyRequired) {
		t.Fatalf("on without a key: %v", err)
	}
	got, err := f.svc.UpdateSettings(f.ctx, f.ws, f.owner, SettingsUpdate{
		Enabled: true, Provider: llm.DeepSeek, Model: "deepseek-chat", Key: key("sk-0123456789abcdef0123456789abcdef"),
		Prices: Prices{InputMicros: 280_000, OutputMicros: 420_000},
	})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if got.Provider != llm.DeepSeek || got.KeyHint != "…cdef" {
		t.Fatalf("settings %+v", got)
	}
	f.provider.answer(fakeAnswer{status: 200, body: `{"model":"deepseek-chat","choices":[{"message":{"content":"{\"title\":\"Вход не работает\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":100,"prompt_cache_hit_tokens":600,"prompt_cache_miss_tokens":400}}`})
	call := ask("Не могу войти")
	call.Schema = &llm.Schema{Name: "draft", JSON: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)}
	res, err := f.svc.Complete(f.ctx, f.ws, call)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	// 1000 × $0.28 + 100 × $0.42 per million; cached tokens at the full price.
	if string(res.JSON) != `{"title":"Вход не работает"}` || res.CostMicros != 322 {
		t.Fatalf("result %+v", res)
	}
	if f.provider.paths[0] != "/deepseek/chat/completions" || f.provider.headers[0].Get("Authorization") != "Bearer sk-0123456789abcdef0123456789abcdef" {
		t.Fatalf("request %s %v", f.provider.paths[0], f.provider.headers[0])
	}
	if rf, _ := f.provider.requests[0]["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Fatalf("response_format %v", f.provider.requests[0]["response_format"])
	}
}

func TestClearOldTexts(t *testing.T) {
	f := setup(t)
	f.haiku(f.ws, nil)
	f.provider.answer(anthropicText("old", 1, 1), anthropicText("new", 1, 1))
	for _, text := range []string{"old", "new"} {
		if _, err := f.svc.Complete(f.ctx, f.ws, ask(text)); err != nil {
			t.Fatalf("complete: %v", err)
		}
	}
	// Age the first call past the retention, as the database owner.
	owner, err := pgx.Connect(f.ctx, f.data.OwnerURL)
	if err != nil {
		t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = owner.Close(f.ctx) }()
	if _, err := owner.Exec(f.ctx, `UPDATE ai_calls SET created_at = now() - interval '31 days' WHERE response_redacted->>'text' = 'old'`); err != nil {
		t.Fatalf("age call: %v", err)
	}
	n, err := ClearOldTexts(f.ctx, f.data.OpenSystem(t).Pool()) //nolint:forbidigo // the system role, as in cmd
	if err != nil || n != 1 {
		t.Fatalf("cleared %d: %v", n, err)
	}
	logged := f.loggedCalls(f.ws)
	if len(logged) != 2 {
		t.Fatalf("calls %d: rows stay for the budget", len(logged))
	}
	for _, c := range logged {
		old := c.CreatedAt.Before(time.Now().Add(-TextRetention))
		if old != (c.RequestRedacted == nil && c.ResponseRedacted == nil) {
			t.Fatalf("call of %v: texts %s / %s", c.CreatedAt, c.RequestRedacted, c.ResponseRedacted)
		}
	}
}
