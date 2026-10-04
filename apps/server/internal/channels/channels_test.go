package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/config"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// fixture: a fresh database (application role, RLS on), two workspaces and
// the service over fake Telegram.
type fixture struct {
	t        *testing.T
	ctx      context.Context
	app      *db.DB
	sys      *db.DB
	ownerURL string
	bots     *channelstest.Bots
	users    *channelstest.Users
	sealer   *crypto.Sealer
	svc      *Service
	inbox    *conversations.Service
	files    *storage.FS
	wsA, wsB uuid.UUID
	ownerA   uuid.UUID
	ownerB   uuid.UUID
	rdb      *redis.Client // sendSetup only
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
	f := &fixture{
		t: t, ctx: context.Background(), app: app, sys: d.OpenSystem(t), ownerURL: d.OwnerURL,
		bots: &channelstest.Bots{}, users: &channelstest.Users{}, sealer: sealer,
	}
	files, err := storage.NewFS(storage.FSOptions{
		Root: t.TempDir(), LinkBaseURL: "http://localhost/api/v1/files",
		SigningKey: bytes.Repeat([]byte("k"), storage.MinSigningKeyLen),
	})
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = files.Close() })
	f.files, f.inbox = files, conversations.New(app, files)
	f.svc = New(app, Options{
		Sealer: sealer, Bots: f.bots, Users: f.users, HookBaseURL: "https://support.example.com",
		Inbox: f.inbox, Files: files, Log: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	tenants := tenancy.New(app)
	f.ownerA, f.wsA = f.workspace(tenants, "alpha")
	f.ownerB, f.wsB = f.workspace(tenants, "bravo")
	return f
}

func (f *fixture) workspace(tenants *tenancy.Service, slug string) (owner, ws uuid.UUID) {
	f.t.Helper()
	owner = uuid.Must(uuid.NewV7())
	err := f.app.InGlobalTx(f.ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, "INSERT INTO users (id, email, name) VALUES ($1, $2, $3)", owner, slug+"@example.test", slug)
		return err
	})
	if err != nil {
		f.t.Fatalf("user: %v", err)
	}
	w, err := tenants.CreateWorkspace(f.ctx, tenancy.CreateWorkspaceInput{Name: slug, Slug: slug, OwnerID: owner})
	if err != nil {
		f.t.Fatalf("workspace: %v", err)
	}
	return owner, w.ID
}

// sealed returns the stored secret of a channel as the database holds it.
func (f *fixture) sealed(ws, id uuid.UUID) []byte {
	f.t.Helper()
	conn, err := pgx.Connect(f.ctx, f.ownerURL)
	if err != nil {
		f.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(f.ctx) }()
	var b []byte
	err = conn.QueryRow(f.ctx, "SELECT sealed FROM channel_secrets WHERE workspace_id = $1 AND channel_id = $2", ws, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		f.t.Fatalf("read secret: %v", err)
	}
	return b
}

func (f *fixture) opened(ws, id uuid.UUID) map[string]any {
	f.t.Helper()
	sec, err := f.sealer.Open(f.ctx, f.sealed(ws, id), crypto.AAD{WorkspaceID: ws, SecretID: id})
	if err != nil {
		f.t.Fatalf("open secret: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(sec.Reveal()), &m); err != nil {
		f.t.Fatalf("decode secret: %v", err)
	}
	return m
}

func (f *fixture) list(ws uuid.UUID) []Channel {
	f.t.Helper()
	l, err := f.svc.List(f.ctx, ws)
	if err != nil {
		f.t.Fatalf("list: %v", err)
	}
	return l
}

func TestConnectBot_Webhook(t *testing.T) {
	f := setup(t)
	ch, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, " "+channelstest.BotToken+" ", "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if ch.Type != TypeTelegramBot || ch.Status != StatusActive || ch.Name != "Зерно · поддержка" {
		t.Fatalf("channel: %+v", ch)
	}
	if ch.Details != (Details{Username: "shop_support_bot", DisplayName: "Зерно · поддержка", Delivery: DeliveryWebhook}) {
		t.Fatalf("details: %+v", ch.Details)
	}
	if len(f.bots.Hooks) != 1 {
		t.Fatalf("hooks: %+v", f.bots.Hooks)
	}
	hook := f.bots.Hooks[0]
	if hook.URL != "https://support.example.com/hooks/telegram/"+f.wsA.String()+"/"+ch.ID.String() ||
		!regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hook.Secret) || !slices.Contains(hook.Allowed, "message") {
		t.Fatalf("hook: %+v", hook)
	}

	// The database holds the token sealed; it opens only for its place.
	if raw := f.sealed(f.wsA, ch.ID); bytes.Contains(raw, []byte(channelstest.BotToken)) || bytes.Contains(raw, []byte(hook.Secret)) {
		t.Fatal("secret stored in clear")
	}
	if sec := f.opened(f.wsA, ch.ID); sec["token"] != channelstest.BotToken || sec["webhook_secret"] != hook.Secret {
		t.Fatalf("opened secret: %v", sec)
	}
	if got := f.list(f.wsA); len(got) != 1 || got[0].ID != ch.ID {
		t.Fatalf("list A: %+v", got)
	}
	if got := f.list(f.wsB); len(got) != 0 {
		t.Fatalf("list B sees A: %+v", got)
	}
}

func TestConnectBot_PollingWithoutPublicHTTPS(t *testing.T) {
	f := setup(t)
	for _, base := range []string{"http://localhost:8080", "https://support.example.com:9443"} {
		svc := New(f.app, Options{Sealer: f.sealer, Bots: f.bots, HookBaseURL: base})
		ch, err := svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "Поддержка")
		if err != nil {
			t.Fatalf("%s: %v", base, err)
		}
		if ch.Details.Delivery != DeliveryPolling || ch.Name != "Поддержка" {
			t.Fatalf("%s: %+v", base, ch)
		}
		if err := svc.Disconnect(f.ctx, f.wsA, ch.ID); err != nil {
			t.Fatalf("disconnect: %v", err)
		}
	}
	if len(f.bots.Hooks) != 0 || f.bots.Deleted != 4 { // deleteWebhook on connect and on disconnect
		t.Fatalf("hooks %v, deleted %d", f.bots.Hooks, f.bots.Deleted)
	}
}

func TestConnectBot_OnceOnTheInstallation(t *testing.T) {
	f := setup(t)
	first, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	for _, ws := range []struct{ ws, actor uuid.UUID }{{f.wsA, f.ownerA}, {f.wsB, f.ownerB}} {
		if _, err := f.svc.ConnectBot(f.ctx, ws.ws, ws.actor, channelstest.BotToken, ""); !errors.Is(err, ErrAlreadyConnected) {
			t.Fatalf("second connect: %v", err)
		}
	}
	// The existing channel keeps its webhook: no second setWebhook.
	if len(f.bots.Hooks) != 1 {
		t.Fatalf("webhook re-pointed: %+v", f.bots.Hooks)
	}
	if err := f.svc.Disconnect(f.ctx, f.wsA, first.ID); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if _, err := f.svc.ConnectBot(f.ctx, f.wsB, f.ownerB, channelstest.BotToken, ""); err != nil {
		t.Fatalf("connect after disconnect: %v", err)
	}
}

func TestConnectBot_Rejections(t *testing.T) {
	f := setup(t)
	revoked := "111111111:AAH-revoked-token-revoked-token-revoke"
	for name, tc := range map[string]struct {
		token string
		want  error
	}{
		"malformed": {"123:abc", ErrInvalid},
		"unknown":   {revoked, ErrBotToken},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, tc.token, ""); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
	if _, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, strings.Repeat("я", 121)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("long name: %v", err)
	}

	f.bots.FailWebhook = errors.New("connection reset")
	_, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if !errors.Is(err, ErrTelegram) {
		t.Fatalf("webhook failure: %v", err)
	}
	if got := f.list(f.wsA); len(got) != 0 {
		t.Fatalf("a failed connect left a channel: %+v", got)
	}
	f.bots.FailWebhook = nil
	if _, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, ""); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}

	noKey := New(f.app, Options{Bots: f.bots, Users: f.users})
	if _, err := noKey.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.OtherBotToken, ""); !errors.Is(err, ErrNoEncryption) {
		t.Fatalf("without a key: %v", err)
	}
	if _, err := noKey.StartUserLogin(f.ctx, f.wsA, f.ownerA, channelstest.AccountPhone); !errors.Is(err, ErrNoEncryption) {
		t.Fatalf("login without a key: %v", err)
	}
}

func TestUserLogin_Code(t *testing.T) {
	f := setup(t)
	login, err := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, "+7 (900) 123-45-67")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if login.Next != StepCode || login.CodeVia != telegram.CodeViaApp || login.Channel.Status != StatusPending ||
		login.Channel.Details.PhoneMasked != "+79•••••••67" {
		t.Fatalf("start: %+v", login)
	}
	if got := f.list(f.wsA); len(got) != 1 || got[0].Status != StatusPending {
		t.Fatalf("pending not listed: %+v", got)
	}
	// The phone is in the sealed secret only.
	if raw := f.sealed(f.wsA, login.Channel.ID); bytes.Contains(raw, []byte("9001234567")) {
		t.Fatal("phone stored in clear")
	}

	if _, err := f.svc.SubmitCode(f.ctx, f.wsA, login.Channel.ID, "00000"); !errors.Is(err, telegram.ErrCodeInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	done, err := f.svc.SubmitCode(f.ctx, f.wsA, login.Channel.ID, " "+channelstest.CodeOK+" ")
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	ch := done.Channel
	if done.Next != StepDone || ch.Status != StatusActive || ch.Name != "Марина Ковалёва" ||
		ch.Details != (Details{Username: "marina_zerno", DisplayName: "Марина Ковалёва", PhoneMasked: "+79•••••••67"}) {
		t.Fatalf("done: %+v", done)
	}
	// After the login only the session is kept, no phone or code hash.
	sec := f.opened(f.wsA, ch.ID)
	if _, ok := sec["phone"]; ok || sec["code_hash"] != nil || sec["session"] == nil {
		t.Fatalf("secret after login: %v", sec)
	}
	if _, err := f.svc.SubmitCode(f.ctx, f.wsA, ch.ID, channelstest.CodeOK); !errors.Is(err, ErrLoginStep) {
		t.Fatalf("code after done: %v", err)
	}
}

func TestUserLogin_CloudPassword(t *testing.T) {
	f := setup(t)
	login, err := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, "79005550000")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	id := login.Channel.ID
	if _, err := f.svc.SubmitPassword(f.ctx, f.wsA, id, channelstest.Password); !errors.Is(err, ErrLoginStep) {
		t.Fatalf("password before code: %v", err)
	}
	step, err := f.svc.SubmitCode(f.ctx, f.wsA, id, channelstest.CodePassword)
	if err != nil || step.Next != StepPassword {
		t.Fatalf("code: %+v %v", step, err)
	}
	if _, err := f.svc.SubmitCode(f.ctx, f.wsA, id, channelstest.CodeOK); !errors.Is(err, ErrLoginStep) {
		t.Fatalf("code twice: %v", err)
	}
	if _, err := f.svc.SubmitPassword(f.ctx, f.wsA, id, "wrong"); !errors.Is(err, telegram.ErrPasswordInvalid) {
		t.Fatalf("wrong password: %v", err)
	}
	done, err := f.svc.SubmitPassword(f.ctx, f.wsA, id, channelstest.Password)
	if err != nil || done.Next != StepDone || done.Channel.Status != StatusActive || done.Channel.Name != "Новый" {
		t.Fatalf("password: %+v %v", done, err)
	}
	// Every step continued the session of the previous one.
	want := []string{"", "s:+79005550000", "s:+79005550000|code", "s:+79005550000|code"}
	if !slices.Equal(f.users.Sessions, want) {
		t.Fatalf("sessions: %q", f.users.Sessions)
	}
	if sess, _ := f.opened(f.wsA, id)["session"].(string); sess == "" {
		t.Fatal("no session stored")
	}
	// The password is never stored.
	if raw := f.sealed(f.wsA, id); bytes.Contains(raw, []byte(channelstest.Password)) {
		t.Fatal("password stored")
	}
}

func TestUserLogin_ExpiresAndIsPruned(t *testing.T) {
	f := setup(t)
	login, err := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, channelstest.AccountPhone)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	later := time.Now().UTC().Add(LoginTTL + time.Minute)
	f.svc.now = func() time.Time { return later }
	if _, err := f.svc.SubmitCode(f.ctx, f.wsA, login.Channel.ID, channelstest.CodeOK); !errors.Is(err, ErrLoginExpired) {
		t.Fatalf("expired: %v", err)
	}
	if got := f.list(f.wsA); len(got) != 0 {
		t.Fatalf("expired login listed: %+v", got)
	}
	// An active channel is not touched by pruning.
	if _, err := f.svc.ConnectBot(f.ctx, f.wsB, f.ownerB, channelstest.BotToken, ""); err != nil {
		t.Fatalf("bot: %v", err)
	}
	n, err := PruneStaleLogins(f.ctx, f.sys.Pool(), later) //nolint:forbidigo // the system role, as in cmd
	if err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if f.sealed(f.wsA, login.Channel.ID) != nil {
		t.Fatal("secret of a pruned login is left")
	}
	f.svc.now = func() time.Time { return time.Now().UTC() }
	if got := f.list(f.wsB); len(got) != 1 || got[0].Status != StatusActive {
		t.Fatalf("pruning touched an active channel: %+v", got)
	}
}

func TestUserLogin_AccountOnceOnTheInstallation(t *testing.T) {
	f := setup(t)
	finish := func(ws, actor uuid.UUID) (Login, error) {
		login, err := f.svc.StartUserLogin(f.ctx, ws, actor, channelstest.AccountPhone)
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		return f.svc.SubmitCode(f.ctx, ws, login.Channel.ID, channelstest.CodeOK)
	}
	if _, err := finish(f.wsA, f.ownerA); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := finish(f.wsB, f.ownerB); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("second: %v", err)
	}
	// The second session is ended at once and the attempt is gone.
	if len(f.users.LoggedOut) != 1 || !strings.HasSuffix(f.users.LoggedOut[0], "|code") {
		t.Fatalf("logged out: %q", f.users.LoggedOut)
	}
	if got := f.list(f.wsB); len(got) != 0 {
		t.Fatalf("attempt left: %+v", got)
	}
}

func TestUserLogin_Unavailable(t *testing.T) {
	f := setup(t)
	svc := New(f.app, Options{Sealer: f.sealer, Bots: f.bots})
	if svc.UserAccountsAvailable() {
		t.Fatal("available without a Telegram application")
	}
	if _, err := svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, channelstest.AccountPhone); !errors.Is(err, ErrUserUnavailable) {
		t.Fatalf("start: %v", err)
	}
	if _, err := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, "12"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short phone: %v", err)
	}
}

func TestDisconnect(t *testing.T) {
	f := setup(t)
	bot, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	login, _ := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, channelstest.AccountPhone)
	user, err := f.svc.SubmitCode(f.ctx, f.wsA, login.Channel.ID, channelstest.CodeOK)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	pending, _ := f.svc.StartUserLogin(f.ctx, f.wsA, f.ownerA, "+79005550000")

	// Another workspace cannot see, rename or remove them.
	if err := f.svc.Disconnect(f.ctx, f.wsB, bot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign disconnect: %v", err)
	}
	if _, err := f.svc.Rename(f.ctx, f.wsB, bot.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign rename: %v", err)
	}
	if ch, err := f.svc.Rename(f.ctx, f.wsA, bot.ID, "  Бот магазина "); err != nil || ch.Name != "Бот магазина" {
		t.Fatalf("rename: %+v %v", ch, err)
	}

	// Telegram's failure does not keep a channel the admin removes.
	f.bots.FailWebhook = telegram.ErrBotToken
	for _, id := range []uuid.UUID{bot.ID, user.Channel.ID, pending.Channel.ID} {
		if err := f.svc.Disconnect(f.ctx, f.wsA, id); err != nil {
			t.Fatalf("disconnect %s: %v", id, err)
		}
		if f.sealed(f.wsA, id) != nil {
			t.Fatalf("secret of %s is left", id)
		}
	}
	if got := f.list(f.wsA); len(got) != 0 {
		t.Fatalf("listed after disconnect: %+v", got)
	}
	if len(f.users.LoggedOut) != 1 || f.users.LoggedOut[0] != "s:"+channelstest.AccountPhone+"|code" {
		t.Fatalf("session not ended: %q", f.users.LoggedOut)
	}
	if err := f.svc.Disconnect(f.ctx, f.wsA, bot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("twice: %v", err)
	}
}
