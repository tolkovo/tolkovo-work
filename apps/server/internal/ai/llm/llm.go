// Package llm talks to language models (ADR-0008, docs/03 §3): Anthropic's
// Messages API, OpenAI's Chat Completions, DeepSeek and any OpenAI-compatible
// server (Ollama, vLLM, LM Studio, OpenRouter). No SDK (owner's decision
// 2026-10-06): a few JSON calls over net/http, through httpx.SafeClient.
//
// Phase 1 needs a plain answer and an answer as a JSON object of a given
// schema. Tool loops, streaming and embeddings come with the bot (phase 2).
//
// A client never logs and never puts the key, the request or the answer
// into errors: an error names its kind, the HTTP status and the provider's
// error code, never the provider's message, which may quote the key.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// Providers.
const (
	Anthropic        = "anthropic"
	OpenAI           = "openai"
	DeepSeek         = "deepseek"
	OpenAICompatible = "openai_compatible"
)

// Official endpoints.
const (
	AnthropicURL = "https://api.anthropic.com"
	OpenAIURL    = "https://api.openai.com/v1"
	DeepSeekURL  = "https://api.deepseek.com"
)

// Kinds of errors; every error of a call wraps one of them (see Error).
var (
	// ErrAuth: the provider refused the key (401, 403).
	ErrAuth = errors.New("llm: the provider refused the key")
	// ErrRateLimited: too many requests for now (429); Error.RetryAfter may say how long to wait.
	ErrRateLimited = errors.New("llm: rate limited by the provider")
	// ErrUnavailable: the provider could not be reached, timed out or failed (5xx).
	ErrUnavailable = errors.New("llm: the provider is unavailable")
	// ErrRejected: the provider refused the request itself, e.g. an unknown
	// model, a request too large or the account out of credit.
	ErrRejected = errors.New("llm: the provider rejected the request")
	// ErrRefused: the model declined to answer.
	ErrRefused = errors.New("llm: the model declined to answer")
	// ErrTruncated: the answer ran out of tokens.
	ErrTruncated = errors.New("llm: the answer was cut off at the token limit")
	// ErrBadOutput: the answer is not the JSON object asked for.
	ErrBadOutput = errors.New("llm: the answer is not the JSON object asked for")
)

// Error is a failed call.
type Error struct {
	Kind error
	// Status is the HTTP status; 0 without an answer.
	Status int
	// Code is the provider's error type or code, e.g. not_found_error,
	// model_not_found, insufficient_quota.
	Code string
	// RetryAfter is the provider's Retry-After, when it sent one.
	RetryAfter time.Duration
	// cause is a network or decoding error.
	cause error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.Error())
	if e.Status != 0 {
		fmt.Fprintf(&b, ": status %d", e.Status)
	}
	if e.Code != "" {
		b.WriteString(" (" + e.Code + ")")
	}
	if e.cause != nil {
		b.WriteString(": " + e.cause.Error())
	}
	return b.String()
}

// Unwrap lets errors.Is match the kind and the cause (context.DeadlineExceeded).
func (e *Error) Unwrap() []error {
	if e.cause != nil {
		return []error{e.Kind, e.cause}
	}
	return []error{e.Kind}
}

// Role of a message.
type Role string

// Roles.
const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one turn of the conversation sent to the model.
type Message struct {
	Role Role   `json:"role"`
	Text string `json:"text"`
}

// Schema asks for the answer as one JSON object. JSON is a JSON Schema of
// an object; for OpenAI's strict mode every property is required and
// additionalProperties is false.
type Schema struct {
	// Name is [a-zA-Z0-9_-], at most 64 characters.
	Name        string
	Description string
	JSON        json.RawMessage
}

// Request is one call.
type Request struct {
	Model    string
	System   string
	Messages []Message
	// MaxTokens bounds the answer.
	MaxTokens int
	// Temperature is left to the model when nil; reasoning models refuse it.
	Temperature *float64
	// Schema, when set, makes Response.JSON hold the answer.
	Schema *Schema
	// CacheSystem asks to cache the system prompt where the provider needs
	// to be asked (Anthropic); OpenAI and DeepSeek cache long prefixes
	// themselves.
	CacheSystem bool
}

// Usage counts tokens. InputTokens includes CachedTokens.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CachedTokens int
}

// Response is a model's answer. Usage is filled in on ErrRefused,
// ErrTruncated and ErrBadOutput too: those tokens are paid for.
type Response struct {
	Text  string
	JSON  json.RawMessage
	Usage Usage
	// Model is the model that answered, as the provider names it.
	Model string
}

// Provider is a model provider.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req Request) (Response, error)
}

// Config selects and opens a provider.
type Config struct {
	Provider string
	// BaseURL is the server of openai_compatible, or overrides the
	// official endpoint (tests, a regional endpoint).
	BaseURL string
	// Key may be empty for an openai_compatible server without one.
	Key crypto.Secret
}

// HTTPClient sends HTTP requests; *http.Client is one (httpx.SafeClient).
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// New returns the provider of cfg over hc.
func New(cfg Config, hc HTTPClient) (Provider, error) {
	switch cfg.Provider {
	case Anthropic:
		return &anthropic{base: orDefault(cfg.BaseURL, AnthropicURL), key: cfg.Key, hc: hc}, nil
	case OpenAI:
		return &openai{name: OpenAI, base: orDefault(cfg.BaseURL, OpenAIURL), key: cfg.Key, hc: hc}, nil
	case DeepSeek:
		return &openai{name: DeepSeek, base: orDefault(cfg.BaseURL, DeepSeekURL), key: cfg.Key, hc: hc, compat: true, jsonObject: true}, nil
	case OpenAICompatible:
		if cfg.BaseURL == "" {
			return nil, errors.New("llm: openai_compatible needs a base URL")
		}
		return &openai{name: OpenAICompatible, base: cfg.BaseURL, key: cfg.Key, hc: hc, compat: true}, nil
	}
	return nil, fmt.Errorf("llm: unknown provider %q", cfg.Provider)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return strings.TrimRight(v, "/")
}

// object checks that raw is one JSON object, tolerating a Markdown fence
// around it (some local models add one), and returns it.
func object(raw string) (json.RawMessage, bool) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	if !strings.HasPrefix(s, "{") || !json.Valid([]byte(s)) {
		return nil, false
	}
	return json.RawMessage(s), true
}
