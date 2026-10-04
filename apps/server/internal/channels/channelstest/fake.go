// Package channelstest fakes Telegram for tests of the channels module and
// the API: a Bot API that knows a fixed set of tokens and an MTProto login
// with fixed codes and passwords.
package channelstest

import (
	"context"
	"strings"
	"sync"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
)

// Tokens the fake Bot API knows. Other well-formed tokens are revoked.
const (
	BotToken      = "123456789:AAH-test-token-of-the-shop-support-bot0"
	OtherBotToken = "987654321:AAH-test-token-of-another-bot-000000000"
)

// Bots fakes the Bot API.
type Bots struct {
	mu sync.Mutex
	// FailWebhook makes SetWebhook and DeleteWebhook fail.
	FailWebhook error
	// Hooks records SetWebhook calls; Deleted counts DeleteWebhook.
	Hooks   []Hook
	Deleted int
}

// Hook is one SetWebhook call.
type Hook struct {
	Token, URL, Secret string
	Allowed            []string
}

// GetMe knows BotToken (@shop_support_bot) and OtherBotToken.
func (b *Bots) GetMe(_ context.Context, token string) (telegram.Bot, error) {
	switch token {
	case BotToken:
		return telegram.Bot{ID: 123456789, Username: "shop_support_bot", FirstName: "Зерно · поддержка"}, nil
	case OtherBotToken:
		return telegram.Bot{ID: 987654321, Username: "other_bot", FirstName: "Other"}, nil
	}
	return telegram.Bot{}, telegram.ErrBotToken
}

// SetWebhook records the call.
func (b *Bots) SetWebhook(_ context.Context, token, hookURL, secret string, allowed []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.FailWebhook != nil {
		return b.FailWebhook
	}
	b.Hooks = append(b.Hooks, Hook{Token: token, URL: hookURL, Secret: secret, Allowed: allowed})
	return nil
}

// DeleteWebhook counts the call.
func (b *Bots) DeleteWebhook(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.FailWebhook != nil {
		return b.FailWebhook
	}
	b.Deleted++
	return nil
}

// Login codes and the cloud password of the fake MTProto.
const (
	CodeOK       = "11111" // signs in
	CodePassword = "22222" // needs the cloud password
	Password     = "correct horse"
	// AccountPhone signs in as Account; any other valid phone as a new account.
	AccountPhone = "+79001234567"
)

// Account is the account behind AccountPhone.
var Account = telegram.Account{ID: 5550001, FirstName: "Марина", LastName: "Ковалёва", Username: "marina_zerno"}

// Users fakes the MTProto login. Sessions are strings that grow with
// every step ("s:+7900…|code|password"), so tests can check each step got
// the session of the previous one.
type Users struct {
	mu sync.Mutex
	// LoggedOut holds the sessions passed to LogOut.
	LoggedOut []string
	// Sessions holds the session each call received.
	Sessions []string
}

func (u *Users) record(sess []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Sessions = append(u.Sessions, string(sess))
}

// SendCode accepts any phone; the code goes to the app.
func (u *Users) SendCode(_ context.Context, sess []byte, phone string) ([]byte, telegram.SentCode, error) {
	u.record(sess)
	return []byte("s:" + phone), telegram.SentCode{Hash: "hash-" + phone, Via: telegram.CodeViaApp}, nil
}

// SignIn accepts CodeOK and asks for the password on CodePassword.
func (u *Users) SignIn(_ context.Context, sess []byte, phone, hash, code string) ([]byte, telegram.Account, error) {
	u.record(sess)
	if hash != "hash-"+phone {
		return sess, telegram.Account{}, telegram.ErrCodeExpired
	}
	next := []byte(string(sess) + "|code")
	switch code {
	case CodeOK:
		return next, accountFor(string(sess)), nil
	case CodePassword:
		return next, telegram.Account{}, telegram.ErrPasswordNeeded
	}
	return sess, telegram.Account{}, telegram.ErrCodeInvalid
}

// Password accepts Password.
func (u *Users) Password(_ context.Context, sess []byte, password string) ([]byte, telegram.Account, error) {
	u.record(sess)
	if password != Password {
		return sess, telegram.Account{}, telegram.ErrPasswordInvalid
	}
	return []byte(string(sess) + "|password"), accountFor(string(sess)), nil
}

// LogOut records the session.
func (u *Users) LogOut(_ context.Context, sess []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.LoggedOut = append(u.LoggedOut, string(sess))
	return nil
}

func accountFor(sess string) telegram.Account {
	if strings.HasPrefix(sess, "s:"+AccountPhone) {
		return Account
	}
	return telegram.Account{ID: 5550002, FirstName: "Новый", Username: ""}
}
