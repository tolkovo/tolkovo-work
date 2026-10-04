package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
)

const token = "123456789:AAH-test-token-of-the-shop-support-bot0"

func botServer(t *testing.T, h func(method string, body map[string]any) (int, string)) *BotAPI {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/bot" + token + "/"
		if !strings.HasPrefix(r.URL.Path, prefix) || r.Method != http.MethodPost ||
			r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, `{"ok":false,"error_code":404,"description":"Not Found"}`, http.StatusNotFound)
			return
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		code, answer := h(strings.TrimPrefix(r.URL.Path, prefix), body)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return NewBotAPI(srv.URL+"/", srv.Client())
}

func TestBotAPI_GetMeAndWebhook(t *testing.T) {
	var hook map[string]any
	api := botServer(t, func(method string, body map[string]any) (int, string) {
		switch method {
		case "getMe":
			return 200, `{"ok":true,"result":{"id":123456789,"is_bot":true,"first_name":"Зерно","username":"shop_support_bot"}}`
		case "setWebhook":
			hook = body
			return 200, `{"ok":true,"result":true}`
		case "deleteWebhook":
			if body["drop_pending_updates"] != false {
				return 400, `{"ok":false,"error_code":400,"description":"bad"}`
			}
			return 200, `{"ok":true,"result":true}`
		}
		return 404, `{"ok":false,"error_code":404,"description":"Not Found"}`
	})
	ctx := context.Background()
	bot, err := api.GetMe(ctx, token)
	if err != nil || bot != (Bot{ID: 123456789, Username: "shop_support_bot", FirstName: "Зерно"}) {
		t.Fatalf("getMe: %+v %v", bot, err)
	}
	if err := api.SetWebhook(ctx, token, "https://x.test/hooks", "s3cr3t", []string{"message"}); err != nil {
		t.Fatalf("setWebhook: %v", err)
	}
	if hook["url"] != "https://x.test/hooks" || hook["secret_token"] != "s3cr3t" || hook["max_connections"] != float64(40) {
		t.Fatalf("setWebhook body: %v", hook)
	}
	if err := api.DeleteWebhook(ctx, token); err != nil {
		t.Fatalf("deleteWebhook: %v", err)
	}
}

func TestBotAPI_Errors(t *testing.T) {
	ctx := context.Background()
	unauthorized := botServer(t, func(string, map[string]any) (int, string) {
		return 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`
	})
	if _, err := unauthorized.GetMe(ctx, token); !errors.Is(err, ErrBotToken) {
		t.Fatalf("401: %v", err)
	}
	notBot := botServer(t, func(string, map[string]any) (int, string) {
		return 200, `{"ok":true,"result":{"id":1,"is_bot":false,"first_name":"Person"}}`
	})
	if _, err := notBot.GetMe(ctx, token); !errors.Is(err, ErrBotToken) {
		t.Fatalf("not a bot: %v", err)
	}
	limited := botServer(t, func(string, map[string]any) (int, string) {
		return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`
	})
	var apiErr *APIError
	if err := limited.DeleteWebhook(ctx, token); !errors.As(err, &apiErr) || apiErr.RetryAfter != 7*time.Second {
		t.Fatalf("429: %v", err)
	}

	// A malformed token never leaves the process.
	called := false
	guard := botServer(t, func(string, map[string]any) (int, string) { called = true; return 200, `{"ok":true}` })
	if _, err := guard.GetMe(ctx, "not-a-token"); !errors.Is(err, ErrBotToken) || called {
		t.Fatalf("malformed token: %v, called %v", err, called)
	}

	// Network errors do not print the URL, which holds the token.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := NewBotAPI(srv.URL, nil).GetMe(ctx, token)
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(fmt.Sprintf("%+v", err), "AAH-test") {
		t.Fatalf("network error leaks the token: %v", err)
	}
}

func TestValidBotToken(t *testing.T) {
	for s, want := range map[string]bool{
		token:                            true,
		"123:short":                      false,
		"abc:" + strings.Repeat("A", 35): false,
		"123456789:" + strings.Repeat("A", 35) + "/x": false,
	} {
		if ValidBotToken(s) != want {
			t.Errorf("ValidBotToken(%q) != %v", s, want)
		}
	}
}

func TestNormalizeAndMaskPhone(t *testing.T) {
	for in, want := range map[string]string{
		"+7 (900) 123-45-67": "+79001234567",
		"79001234567":        "+79001234567",
		"+44 20 7946 0958":   "+442079460958",
	} {
		if got, ok := NormalizePhone(in); !ok || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "12", "+0123456789", "+7 900 abc", "+1234567890123456"} {
		if _, ok := NormalizePhone(bad); ok {
			t.Errorf("NormalizePhone(%q) accepted", bad)
		}
	}
	if got := MaskPhone("+79001234567"); got != "+79•••••••67" {
		t.Errorf("MaskPhone: %q", got)
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		rpc  string
		want error
	}{
		{"PHONE_NUMBER_INVALID", ErrPhoneInvalid},
		{"PHONE_NUMBER_UNOCCUPIED", ErrNoAccount},
		{"PHONE_NUMBER_BANNED", ErrBanned},
		{"PHONE_CODE_INVALID", ErrCodeInvalid},
		{"PHONE_CODE_EXPIRED", ErrCodeExpired},
		{"PASSWORD_HASH_INVALID", ErrPasswordInvalid},
		{"API_ID_INVALID", ErrAppInvalid},
		{"AUTH_KEY_UNREGISTERED", ErrSessionRevoked},
		{"SESSION_REVOKED", ErrSessionRevoked},
	} {
		if got := classify(fmt.Errorf("call: %w", tgerr.New(400, tc.rpc))); !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v", tc.rpc, got)
		}
	}
	var flood *FloodWaitError
	if got := classify(tgerr.New(420, "FLOOD_WAIT_30")); !errors.As(got, &flood) || flood.Wait != 30*time.Second {
		t.Errorf("flood wait: %v", got)
	}
	other := errors.New("dial tcp: timeout")
	if got := classify(other); !errors.Is(got, other) {
		t.Errorf("unknown errors pass through: %v", got)
	}
	if classify(nil) != nil {
		t.Error("nil")
	}
}
