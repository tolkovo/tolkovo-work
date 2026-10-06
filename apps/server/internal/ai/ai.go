// Package ai is the AI base of phase 1 (docs/03 §8.1, ADR-0008, docs/06
// AI-01): a workspace brings its own key (BYOK) to Anthropic, OpenAI,
// DeepSeek or an OpenAI-compatible server and picks one model; modules ask the model
// through Complete. Every call is checked against the monthly budget, has
// its personal data masked (internal/ai/guard), runs with a timeout and
// one retry, and is written to ai_calls with its tokens and cost. The first
// feature is the issue draft from a user's report (issues.go, AI-02).
//
// The key is sealed with internal/platform/crypto and opened only for the
// call; it never leaves the module, and the panel sees only "…" and its
// last four characters. Calls to providers never run inside a database
// transaction (docs/00 §6.4).
//
// The knowledge base, the bot, handoff and the copilot come in phase 2
// (docs/03 §13); so do a fallback provider, streaming and tool loops.
package ai

import (
	"errors"
	"net/netip"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/ai/llm"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
)

// Errors of the module. A provider's errors pass through as *llm.Error:
// match them with errors.Is against llm.ErrAuth and the others.
var (
	ErrInvalid = errors.New("ai: invalid input")
	// ErrNotConfigured: AI is off, or has no provider, model or key.
	ErrNotConfigured = errors.New("ai: AI is not set up in this workspace")
	// ErrBudgetExceeded: the month's spending reached the budget.
	ErrBudgetExceeded = errors.New("ai: the monthly AI budget is spent")
	// ErrKeyRequired: the provider needs a key and there is none.
	ErrKeyRequired = errors.New("ai: the provider needs an API key")
	// ErrNoEncryption: TOLKOVO_MASTER_KEY is not set, the key cannot be stored or opened.
	ErrNoEncryption = errors.New("ai: TOLKOVO_MASTER_KEY is not set, the API key cannot be stored")
)

// Defaults of Options.
const (
	DefaultTimeout = 60 * time.Second
	// maxRetryWait bounds the wait before the one retry.
	maxRetryWait = 5 * time.Second
)

// Options configure the module.
type Options struct {
	// Sealer seals the key; nil refuses keys (ErrNoEncryption).
	Sealer *crypto.Sealer
	// HTTP sends the requests: an httpx.SafeClient.
	HTTP llm.HTTPClient
	// AllowPrivate are the operator's exceptions for the base URL of a
	// local server (TOLKOVO_EGRESS_ALLOW_PRIVATE); SafeClient enforces
	// them again when dialling.
	AllowPrivate []netip.Prefix
	// AnthropicURL, OpenAIURL and DeepSeekURL override the official endpoints (tests).
	AnthropicURL string
	OpenAIURL    string
	DeepSeekURL  string
	// Timeout bounds one call with its retry; DefaultTimeout when zero.
	Timeout time.Duration
}

// Service is the module facade.
type Service struct {
	db   *db.DB
	opts Options
	now  func() time.Time
	// retryWait is the pause before the retry when the provider names none.
	retryWait time.Duration
}

// New returns the service.
func New(database *db.DB, opts Options) *Service {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	return &Service{db: database, opts: opts, now: func() time.Time { return time.Now().UTC() }, retryWait: time.Second}
}

// EncryptionAvailable reports whether a key can be stored.
func (s *Service) EncryptionAvailable() bool { return s.opts.Sealer != nil }
