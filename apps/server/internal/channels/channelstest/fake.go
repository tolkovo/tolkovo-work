// Package channelstest fakes Telegram for tests of the channels module and
// the API: a Bot API that knows a fixed set of tokens and an MTProto login
// with fixed codes and passwords.
package channelstest

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

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
	// Files serves downloads by file name; a missing name fails.
	Files   map[string][]byte
	revoked bool
	updates []telegram.BotUpdate
	nextID  int64
}

// Revoke makes GetUpdates answer as for a token revoked in @BotFather.
func (b *Bots) Revoke() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.revoked = true
}

// Push queues updates for getUpdates; each is a Bot API update JSON
// without update_id, which Push assigns.
func (b *Bots) Push(updates ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, raw := range updates {
		b.nextID++
		u, err := telegram.ParseBotUpdate([]byte(`{"update_id":` + itoa(b.nextID) + `,` + strings.TrimPrefix(raw, "{")))
		if err != nil {
			panic(err)
		}
		b.updates = append(b.updates, u)
	}
}

// GetUpdates returns the queued updates from offset, waiting briefly when
// there are none.
func (b *Bots) GetUpdates(ctx context.Context, token string, offset int64, _ int) ([]telegram.BotUpdate, error) {
	for i := 0; i < 20; i++ {
		b.mu.Lock()
		if b.revoked || (token != BotToken && token != OtherBotToken) {
			b.mu.Unlock()
			return nil, telegram.ErrBotToken
		}
		var out []telegram.BotUpdate
		for _, u := range b.updates {
			if u.ID >= offset {
				out = append(out, u)
			}
		}
		b.mu.Unlock()
		if len(out) > 0 {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil, nil
}

// Download serves Files[f.Name].
func (b *Bots) Download(_ context.Context, _ string, f telegram.File, w io.Writer, limit int64) error {
	b.mu.Lock()
	data, ok := b.Files[f.Name]
	b.mu.Unlock()
	return serve(data, ok, w, limit)
}

func serve(data []byte, ok bool, w io.Writer, limit int64) error {
	if !ok {
		return io.ErrUnexpectedEOF
	}
	if int64(len(data)) > limit {
		return telegram.ErrFileTooLarge
	}
	_, err := w.Write(data)
	return err
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
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
	// Feed delivers messages to a running Listen; Files serves their
	// downloads by name. Send ErrSessionRevoked (or any error) on Stop to
	// end Listen with it.
	Feed  chan telegram.Message
	Stop  chan error
	Files map[string][]byte
	// Listening counts the running Listen calls; Ready is signalled each
	// time one starts.
	Listening int
	Ready     chan struct{}
}

// Active is the number of running Listen calls.
func (u *Users) Active() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.Listening
}

// Listen passes Feed to the handler, saving the update state as a real
// account would, until ctx ends or Stop says otherwise.
func (u *Users) Listen(ctx context.Context, opts telegram.ListenOptions) error {
	u.record(opts.Session)
	st, found, err := opts.State.LoadState(ctx)
	if err != nil {
		return err
	}
	if !found {
		st = telegram.UpdateState{Pts: 1, Date: 1}
		if err := opts.State.SaveState(ctx, st); err != nil {
			return err
		}
	}
	u.mu.Lock()
	u.Listening++
	feed, stop, ready := u.Feed, u.Stop, u.Ready
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.Listening--
		u.mu.Unlock()
	}()
	if ready != nil {
		select {
		case ready <- struct{}{}:
		default:
		}
	}
	fetch := func(_ context.Context, f telegram.File, w io.Writer, limit int64) error {
		u.mu.Lock()
		data, ok := u.Files[f.Name]
		u.mu.Unlock()
		return serve(data, ok, w, limit)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-stop:
			return err
		case m := <-feed:
			if err := opts.Handle(ctx, m, fetch); err != nil {
				return err
			}
			st.Pts++
			if err := opts.State.SaveState(ctx, st); err != nil {
				return err
			}
		}
	}
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
