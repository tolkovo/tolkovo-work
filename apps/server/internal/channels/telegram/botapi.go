// Package telegram talks to Telegram for the channels module: the Bot API
// for bots (a thin net/http client, docs/02 §11.5) and MTProto for personal
// accounts (github.com/gotd/td, ADR-0014). It knows nothing of workspaces
// or storage: callers pass tokens and sessions in and keep what comes out.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBotAPIURL is the public Bot API. Self-host may point
// TOLKOVO_TELEGRAM_BOT_API_URL at a local telegram-bot-api server.
const DefaultBotAPIURL = "https://api.telegram.org"

// ErrBotToken means Telegram does not know the token: mistyped or revoked.
var ErrBotToken = errors.New("telegram: bot token is invalid or revoked")

// tokenRE is the shape of a token from @BotFather: "<bot id>:<secret>".
var tokenRE = regexp.MustCompile(`^[0-9]{5,16}:[A-Za-z0-9_-]{30,64}$`)

// ValidBotToken reports whether s looks like a bot token. It saves a round
// trip for obvious typos; only getMe proves the token works.
func ValidBotToken(s string) bool { return tokenRE.MatchString(s) }

// APIError is an error answer of the Bot API. Description comes from
// Telegram and never contains the token.
type APIError struct {
	Method      string
	Code        int
	Description string
	// RetryAfter is set on 429 Too Many Requests.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Bot is the result of getMe.
type Bot struct {
	ID        int64
	Username  string
	FirstName string
}

// BotAPI is a Bot API client. Tokens are part of request URLs, so errors
// never carry the URL.
type BotAPI struct {
	base string
	http *http.Client
	// files downloads media: longer than API calls.
	files *http.Client
}

// NewBotAPI returns a client for the Bot API at baseURL (DefaultBotAPIURL
// when empty). A nil client gets a 20 s timeout for calls (getUpdates
// waits less than that) and 2 minutes for file downloads.
func NewBotAPI(baseURL string, client *http.Client) *BotAPI {
	if baseURL == "" {
		baseURL = DefaultBotAPIURL
	}
	files := client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
		files = &http.Client{Timeout: 2 * time.Minute}
	}
	return &BotAPI{base: strings.TrimRight(baseURL, "/"), http: client, files: files}
}

// GetMe checks the token and returns the bot.
func (b *BotAPI) GetMe(ctx context.Context, token string) (Bot, error) {
	var res struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
	}
	if err := b.call(ctx, token, "getMe", nil, &res); err != nil {
		return Bot{}, err
	}
	if !res.IsBot || res.ID == 0 {
		return Bot{}, fmt.Errorf("telegram getMe: %w", ErrBotToken)
	}
	return Bot{ID: res.ID, Username: res.Username, FirstName: res.FirstName}, nil
}

// SetWebhook points the bot's updates at hookURL. Telegram sends secret in
// the X-Telegram-Bot-Api-Secret-Token header of every update.
func (b *BotAPI) SetWebhook(ctx context.Context, token, hookURL, secret string, allowedUpdates []string) error {
	return b.call(ctx, token, "setWebhook", map[string]any{
		"url":             hookURL,
		"secret_token":    secret,
		"allowed_updates": allowedUpdates,
		"max_connections": 40,
	}, nil)
}

// DeleteWebhook stops webhook delivery, so getUpdates works (polling).
// Pending updates are kept.
func (b *BotAPI) DeleteWebhook(ctx context.Context, token string) error {
	return b.call(ctx, token, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil)
}

// call posts params as JSON to the method and decodes result into out.
func (b *BotAPI) call(ctx context.Context, token, method string, params, out any) error {
	if !ValidBotToken(token) {
		return fmt.Errorf("telegram %s: %w", method, ErrBotToken)
	}
	body := []byte("{}")
	if params != nil {
		var err error
		if body, err = json.Marshal(params); err != nil {
			return fmt.Errorf("telegram %s: encode: %w", method, err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.base+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: build request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		// url.Error prints the URL, and the URL holds the token.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d, unreadable answer", method, resp.StatusCode)
	}
	if !env.OK {
		if env.ErrorCode == http.StatusUnauthorized || env.ErrorCode == http.StatusNotFound {
			return fmt.Errorf("telegram %s: %w", method, ErrBotToken)
		}
		return &APIError{
			Method: method, Code: env.ErrorCode, Description: env.Description,
			RetryAfter: time.Duration(env.Parameters.RetryAfter) * time.Second,
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("telegram %s: decode result: %w", method, err)
	}
	return nil
}
