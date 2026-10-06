package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// The answers below follow the providers' documented shapes (Anthropic
// Messages API 2023-06-01, OpenAI Chat Completions), trimmed to what the
// clients read plus a few fields they must ignore.

const testKey = "sk-test-0123456789abcdef"

type recorded struct {
	status  int
	headers map[string]string
	body    string
}

// server answers every request with rec and keeps the last request.
func server(t *testing.T, rec recorded) (*httptest.Server, *http.Request, *map[string]any) {
	t.Helper()
	got := &http.Request{}
	body := &map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(context.Background())
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		for k, v := range rec.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.status)
		_, _ = io.WriteString(w, rec.body)
	}))
	t.Cleanup(srv.Close)
	return srv, got, body
}

func provider(t *testing.T, name, base string, key string) Provider {
	t.Helper()
	p, err := New(Config{Provider: name, BaseURL: base, Key: crypto.NewSecret(key)}, http.DefaultClient)
	if err != nil {
		t.Fatalf("new %s: %v", name, err)
	}
	return p
}

var draftSchema = &Schema{
	Name: "issue_draft", Description: "A GitHub issue draft",
	JSON: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
}

func ask(schema *Schema) Request {
	return Request{
		Model: "model-x", System: "You draft issues.", MaxTokens: 256, Schema: schema,
		Messages: []Message{{Role: User, Text: "The app crashes on start."}},
	}
}

func TestAnthropic_Answers(t *testing.T) {
	srv, got, body := server(t, recorded{status: 200, body: `{
		"id": "msg_01", "type": "message", "role": "assistant", "model": "claude-haiku-4-5-20251001",
		"content": [{"type": "text", "text": "Hello"}, {"type": "text", "text": ", world"}],
		"stop_reason": "end_turn", "stop_sequence": null,
		"usage": {"input_tokens": 10, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 5, "output_tokens": 7}
	}`})
	resp, err := provider(t, Anthropic, srv.URL, testKey).Chat(context.Background(), ask(nil))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Text != "Hello, world" || resp.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("answer %+v", resp)
	}
	if resp.Usage != (Usage{InputTokens: 18, OutputTokens: 7, CachedTokens: 5}) {
		t.Fatalf("usage %+v", resp.Usage)
	}
	if got.URL.Path != "/v1/messages" || got.Header.Get("x-api-key") != testKey || got.Header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	b := *body
	if b["system"] != "You draft issues." || b["max_tokens"] != float64(256) || b["tools"] != nil || b["temperature"] != nil {
		t.Fatalf("body %v", b)
	}
	msgs := b["messages"].([]any)
	if m := msgs[0].(map[string]any); len(msgs) != 1 || m["role"] != "user" || m["content"] != "The app crashes on start." {
		t.Fatalf("messages %v", msgs)
	}
}

func TestAnthropic_SchemaIsAForcedTool(t *testing.T) {
	srv, _, body := server(t, recorded{status: 200, body: `{
		"type": "message", "model": "claude-haiku-4-5",
		"content": [{"type": "tool_use", "id": "toolu_01", "name": "issue_draft", "input": {"title": "Crash on start"}}],
		"stop_reason": "tool_use", "usage": {"input_tokens": 40, "output_tokens": 12}
	}`})
	resp, err := provider(t, Anthropic, srv.URL, testKey).Chat(context.Background(), ask(draftSchema))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if string(resp.JSON) != `{"title": "Crash on start"}` {
		t.Fatalf("json %s", resp.JSON)
	}
	b := *body
	tool := b["tools"].([]any)[0].(map[string]any)
	choice := b["tool_choice"].(map[string]any)
	if tool["name"] != "issue_draft" || tool["input_schema"] == nil || choice["type"] != "tool" || choice["name"] != "issue_draft" {
		t.Fatalf("tools %v choice %v", tool, choice)
	}
}

func TestAnthropic_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rec    recorded
		schema *Schema
		kind   error
		code   string
		usage  int
	}{
		{"cut off", recorded{status: 200, body: `{"content":[{"type":"text","text":"Hel"}],"stop_reason":"max_tokens","usage":{"input_tokens":5,"output_tokens":256}}`}, nil, ErrTruncated, "", 256},
		{"refusal", recorded{status: 200, body: `{"content":[],"stop_reason":"refusal","usage":{"input_tokens":5,"output_tokens":1}}`}, nil, ErrRefused, "", 1},
		{"no tool call", recorded{status: 200, body: `{"content":[{"type":"text","text":"Sure!"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`}, draftSchema, ErrBadOutput, "", 2},
		{"bad key", recorded{status: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`}, nil, ErrAuth, "authentication_error", 0},
		{"unknown model", recorded{status: 404, body: `{"type":"error","error":{"type":"not_found_error","message":"model: model-x"}}`}, nil, ErrRejected, "not_found_error", 0},
		{"overloaded", recorded{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`}, nil, ErrUnavailable, "overloaded_error", 0},
		{"server error, not JSON", recorded{status: 502, body: `<html>Bad Gateway</html>`}, nil, ErrUnavailable, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := server(t, tc.rec)
			resp, err := provider(t, Anthropic, srv.URL, testKey).Chat(context.Background(), ask(tc.schema))
			if !errors.Is(err, tc.kind) {
				t.Fatalf("err %v, want %v", err, tc.kind)
			}
			var e *Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("error %#v, want code %q", err, tc.code)
			}
			if resp.Usage.OutputTokens != tc.usage {
				t.Fatalf("usage %+v, the tokens are paid for", resp.Usage)
			}
			if strings.Contains(err.Error(), "invalid x-api-key") || strings.Contains(err.Error(), "model-x") {
				t.Fatalf("the provider's message leaked: %v", err)
			}
		})
	}
}

func TestAnthropic_RateLimitCarriesRetryAfter(t *testing.T) {
	srv, _, _ := server(t, recorded{status: 429, headers: map[string]string{"Retry-After": "7"},
		body: `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`})
	_, err := provider(t, Anthropic, srv.URL, testKey).Chat(context.Background(), ask(nil))
	var e *Error
	if !errors.Is(err, ErrRateLimited) || !errors.As(err, &e) || e.RetryAfter != 7*time.Second || e.Status != 429 {
		t.Fatalf("err %#v", err)
	}
}

func TestOpenAI_Answers(t *testing.T) {
	srv, got, body := server(t, recorded{status: 200, body: `{
		"id": "chatcmpl-1", "object": "chat.completion", "created": 1759700000, "model": "gpt-4.1-mini-2025-04-14",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "OK", "refusal": null}, "logprobs": null, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 20, "completion_tokens": 1, "total_tokens": 21, "prompt_tokens_details": {"cached_tokens": 4}}
	}`})
	temp := 0.2
	req := ask(nil)
	req.Temperature = &temp
	resp, err := provider(t, OpenAI, srv.URL, testKey).Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Text != "OK" || resp.Model != "gpt-4.1-mini-2025-04-14" || resp.Usage != (Usage{InputTokens: 20, OutputTokens: 1, CachedTokens: 4}) {
		t.Fatalf("answer %+v", resp)
	}
	if got.URL.Path != "/chat/completions" || got.Header.Get("Authorization") != "Bearer "+testKey {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	b := *body
	if b["max_completion_tokens"] != float64(256) || b["max_tokens"] != nil || b["temperature"] != 0.2 || b["response_format"] != nil {
		t.Fatalf("body %v", b)
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["role"] != "user" {
		t.Fatalf("messages %v", msgs)
	}
}

func TestOpenAI_SchemaIsResponseFormat(t *testing.T) {
	srv, _, body := server(t, recorded{status: 200, body: `{
		"model": "gpt-4.1-mini",
		"choices": [{"message": {"role": "assistant", "content": "{\"title\":\"Crash on start\"}"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 50, "completion_tokens": 9}
	}`})
	resp, err := provider(t, OpenAI, srv.URL, testKey).Chat(context.Background(), ask(draftSchema))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if string(resp.JSON) != `{"title":"Crash on start"}` || resp.Text != "" {
		t.Fatalf("answer %+v", resp)
	}
	b := *body
	rf := b["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["name"] != "issue_draft" || js["strict"] != true || js["schema"] == nil {
		t.Fatalf("response_format %v", rf)
	}
	if sys := b["messages"].([]any)[0].(map[string]any)["content"]; sys != "You draft issues." {
		t.Fatalf("OpenAI itself gets the schema only in response_format: %v", sys)
	}
}

func TestOpenAI_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rec    recorded
		schema *Schema
		kind   error
		code   string
	}{
		{"cut off", recorded{status: 200, body: `{"choices":[{"message":{"content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":5,"completion_tokens":256}}`}, nil, ErrTruncated, ""},
		{"refusal", recorded{status: 200, body: `{"choices":[{"message":{"content":null,"refusal":"I can't help with that."},"finish_reason":"stop"}],"usage":{}}`}, draftSchema, ErrRefused, ""},
		{"filtered", recorded{status: 200, body: `{"choices":[{"message":{"content":""},"finish_reason":"content_filter"}],"usage":{}}`}, nil, ErrRefused, ""},
		{"not JSON", recorded{status: 200, body: `{"choices":[{"message":{"content":"Here you go: title"},"finish_reason":"stop"}],"usage":{}}`}, draftSchema, ErrBadOutput, ""},
		{"no choices", recorded{status: 200, body: `{"choices":[],"usage":{}}`}, nil, ErrBadOutput, ""},
		{"bad key", recorded{status: 401, body: `{"error":{"message":"Incorrect API key provided: sk-test-****cdef.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`}, nil, ErrAuth, "invalid_api_key"},
		{"unknown model", recorded{status: 404, body: `{"error":{"message":"The model model-x does not exist","type":"invalid_request_error","code":"model_not_found"}}`}, nil, ErrRejected, "model_not_found"},
		{"out of credit", recorded{status: 429, body: `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`}, nil, ErrRejected, "insufficient_quota"},
		{"rate limited", recorded{status: 429, body: `{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`}, nil, ErrRateLimited, "rate_limit_exceeded"},
		{"plain string error", recorded{status: 400, body: `{"error":"model \"x\" not found, try pulling it first"}`}, nil, ErrRejected, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := server(t, tc.rec)
			_, err := provider(t, OpenAI, srv.URL, testKey).Chat(context.Background(), ask(tc.schema))
			var e *Error
			if !errors.Is(err, tc.kind) || !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("err %#v, want %v (%q)", err, tc.kind, tc.code)
			}
			if strings.Contains(err.Error(), "sk-") || strings.Contains(err.Error(), "quota") && tc.code == "" {
				t.Fatalf("the provider's message leaked: %v", err)
			}
		})
	}
}

// An OpenAI-compatible server (Ollama, vLLM) may run without a key, gets
// max_tokens and finds the schema in the system prompt as well; a fenced
// JSON answer is accepted.
func TestOpenAICompatible(t *testing.T) {
	srv, got, body := server(t, recorded{status: 200, body: `{
		"model": "qwen2.5:7b",
		"choices": [{"message": {"role": "assistant", "content": "` + "```json\\n{\\\"title\\\": \\\"Crash\\\"}\\n```" + `"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 30, "completion_tokens": 8}
	}`})
	resp, err := provider(t, OpenAICompatible, srv.URL+"/v1", "").Chat(context.Background(), ask(draftSchema))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if string(resp.JSON) != `{"title": "Crash"}` {
		t.Fatalf("json %q", resp.JSON)
	}
	if got.URL.Path != "/v1/chat/completions" || got.Header.Get("Authorization") != "" {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	b := *body
	sys, _ := b["messages"].([]any)[0].(map[string]any)["content"].(string)
	if b["max_tokens"] != float64(256) || b["max_completion_tokens"] != nil || !strings.Contains(sys, `"required":["title"]`) {
		t.Fatalf("body %v", b)
	}
	if _, err := New(Config{Provider: OpenAICompatible}, http.DefaultClient); err == nil {
		t.Fatal("openai_compatible without a base URL")
	}
	if _, err := New(Config{Provider: "gemini"}, http.DefaultClient); err == nil {
		t.Fatal("unknown provider")
	}
}

// DeepSeek knows response_format json_object only: the schema goes into the
// system prompt, the answer limit is max_tokens, cached tokens are
// prompt_cache_hit_tokens.
func TestDeepSeek(t *testing.T) {
	srv, got, body := server(t, recorded{status: 200, body: `{
		"id": "930c60df", "object": "chat.completion", "model": "deepseek-chat",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "{\"title\": \"Crash on start\"}"}, "logprobs": null, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 40, "completion_tokens": 9, "total_tokens": 49, "prompt_tokens_details": {"cached_tokens": 0},
			"prompt_cache_hit_tokens": 32, "prompt_cache_miss_tokens": 8}
	}`})
	resp, err := provider(t, DeepSeek, srv.URL, testKey).Chat(context.Background(), ask(draftSchema))
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if string(resp.JSON) != `{"title": "Crash on start"}` || resp.Usage != (Usage{InputTokens: 40, OutputTokens: 9, CachedTokens: 32}) {
		t.Fatalf("answer %+v", resp)
	}
	if got.URL.Path != "/chat/completions" || got.Header.Get("Authorization") != "Bearer "+testKey {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	b := *body
	rf, _ := b["response_format"].(map[string]any)
	sys, _ := b["messages"].([]any)[0].(map[string]any)["content"].(string)
	if rf["type"] != "json_object" || rf["json_schema"] != nil || b["max_tokens"] != float64(256) || b["max_completion_tokens"] != nil {
		t.Fatalf("body %v", b)
	}
	if !strings.Contains(sys, "JSON") || !strings.Contains(sys, `"required":["title"]`) {
		t.Fatalf("system prompt %q", sys)
	}
	p, _ := New(Config{Provider: DeepSeek}, http.DefaultClient)
	if p.(*openai).base != DeepSeekURL {
		t.Fatalf("default base %q", p.(*openai).base)
	}
}

func TestDeepSeek_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  recorded
		kind error
		code string
	}{
		{"busy", recorded{status: 200, body: `{"choices":[{"message":{"content":"{\"ti"},"finish_reason":"insufficient_system_resource"}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`}, ErrUnavailable, "insufficient_system_resource"},
		{"out of balance", recorded{status: 402, body: `{"error":{"message":"Insufficient Balance","type":"unknown_error","param":null,"code":"invalid_request_error"}}`}, ErrRejected, "invalid_request_error"},
		{"overloaded", recorded{status: 503, body: `{"error":{"message":"Server overloaded, please try again later","type":"service_unavailable_error"}}`}, ErrUnavailable, "service_unavailable_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := server(t, tc.rec)
			_, err := provider(t, DeepSeek, srv.URL, testKey).Chat(context.Background(), ask(draftSchema))
			var e *Error
			if !errors.Is(err, tc.kind) || !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("err %#v, want %v (%q)", err, tc.kind, tc.code)
			}
			if strings.Contains(err.Error(), "Balance") || strings.Contains(err.Error(), "overloaded") {
				t.Fatalf("the provider's message leaked: %v", err)
			}
		})
	}
}

func TestChat_TimeoutIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := provider(t, OpenAI, srv.URL, testKey).Chat(ctx, ask(nil))
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}
