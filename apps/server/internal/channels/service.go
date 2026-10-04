// Package channels connects the places customers write from (docs/02 §11,
// docs/06 T-01): a Telegram bot by its token, or a personal Telegram
// account signed in by phone over MTProto (ADR-0014). Secrets — the bot
// token, the account session — are sealed with internal/platform/crypto
// and never leave the module; the API sees only names, usernames and a
// masked phone.
//
// Network calls to Telegram never run inside a database transaction
// (docs/00 §6.4): every operation talks to Telegram first, then records
// the result.
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/jobs"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
)

// Channel types (migration 00010).
const (
	TypeTelegramBot  = "telegram_bot"
	TypeTelegramUser = "telegram_user"
)

// Channel statuses.
const (
	StatusPending = "pending"
	StatusActive  = "active"
	StatusError   = "error"
)

// Delivery modes of a bot: Telegram pushes updates to a webhook (needs a
// public https URL) or the worker polls getUpdates.
const (
	DeliveryWebhook = "webhook"
	DeliveryPolling = "polling"
)

// LoginTTL bounds a personal-account login from the phone to the last
// step; unfinished logins are pruned after it.
const LoginTTL = 15 * time.Minute

// Login steps of a personal account.
const (
	StepCode     = "code"
	StepPassword = "password"
	StepDone     = "done"
)

// Errors of the module. Telegram's login errors (telegram.ErrCodeInvalid
// and friends, *telegram.FloodWaitError) pass through wrapped.
var (
	ErrInvalid  = errors.New("channels: invalid input")
	ErrNotFound = errors.New("channels: not found")
	// ErrBotToken: Telegram does not know the token.
	ErrBotToken = errors.New("channels: bot token is invalid or revoked")
	// ErrAlreadyConnected: the bot or account is a channel already, here or
	// in another workspace of the installation.
	ErrAlreadyConnected = errors.New("channels: already connected")
	// ErrUserUnavailable: no Telegram application is configured for
	// personal accounts (TOLKOVO_TELEGRAM_API_ID/API_HASH).
	ErrUserUnavailable = errors.New("channels: personal Telegram accounts are not configured on this server")
	// ErrNoEncryption: TOLKOVO_MASTER_KEY is not set, secrets cannot be stored.
	ErrNoEncryption = errors.New("channels: TOLKOVO_MASTER_KEY is not set, secrets cannot be stored")
	// ErrLoginStep: the login is not waiting for this step (finished,
	// expired or at another step).
	ErrLoginStep = errors.New("channels: the login is not waiting for this step")
	// ErrLoginExpired: more than LoginTTL since the phone was entered.
	ErrLoginExpired = errors.New("channels: the login has expired, start again")
	// ErrTelegram: Telegram could not be reached or answered unexpectedly.
	ErrTelegram = errors.New("channels: Telegram is unavailable")
)

// Channel is what the panel may see of a channel.
type Channel struct {
	ID        uuid.UUID
	Type      string
	Name      string
	Status    string
	ErrorCode string
	Details   Details
	CreatedAt time.Time
	// LastInboundAt: when a message last came in (to the minute); nil
	// before the first one.
	LastInboundAt *time.Time
}

// Details are the non-secret facts shown in the panel (channels.config).
type Details struct {
	// Username without "@": the bot's or the account's.
	Username string `json:"username,omitempty"`
	// DisplayName: the bot's first name or the account's full name.
	DisplayName string `json:"display_name,omitempty"`
	// PhoneMasked of a personal account, e.g. "+79•••••••67".
	PhoneMasked string `json:"phone_masked,omitempty"`
	// Delivery of a bot: webhook or polling.
	Delivery string `json:"delivery,omitempty"`
}

// Login is the state of a personal-account login after a step.
type Login struct {
	Channel Channel
	// Next is StepCode, StepPassword or StepDone.
	Next string
	// CodeVia says where the code went: telegram.CodeViaApp, …SMS, …Call.
	CodeVia string
}

// BotClient is the part of the Bot API the module uses.
type BotClient interface {
	GetMe(ctx context.Context, token string) (telegram.Bot, error)
	SetWebhook(ctx context.Context, token, hookURL, secret string, allowedUpdates []string) error
	DeleteWebhook(ctx context.Context, token string) error
	GetUpdates(ctx context.Context, token string, offset int64, timeout int) ([]telegram.BotUpdate, error)
	Download(ctx context.Context, token string, f telegram.File, w io.Writer, limit int64) error
	// Sending (T-03).
	SendMessage(ctx context.Context, token string, chatID int64, text string) (int64, error)
	SendFile(ctx context.Context, token string, chatID int64, f telegram.OutFile, r io.Reader) (int64, error)
	SendChatAction(ctx context.Context, token string, chatID int64) error
}

// UserLogin signs personal accounts in and out (telegram.UserAuth).
type UserLogin interface {
	SendCode(ctx context.Context, sess []byte, phone string) ([]byte, telegram.SentCode, error)
	SignIn(ctx context.Context, sess []byte, phone, hash, code string) ([]byte, telegram.Account, error)
	Password(ctx context.Context, sess []byte, password string) ([]byte, telegram.Account, error)
	LogOut(ctx context.Context, sess []byte) error
	// Listen keeps the account online and passes its messages on (T-02).
	Listen(ctx context.Context, opts telegram.ListenOptions) error
	// Connect opens a connection that only sends, for a worker that does
	// not hold the listening one (T-03).
	Connect(ctx context.Context, sess []byte, fn func(ctx context.Context, c telegram.Conn) error) error
}

// Options configure the service.
type Options struct {
	// Sealer encrypts secrets; nil when TOLKOVO_MASTER_KEY is not set, and
	// then nothing can be connected.
	Sealer *crypto.Sealer
	Bots   BotClient
	// Users is nil when personal accounts are not configured.
	Users UserLogin
	// HookBaseURL is the public URL Telegram posts bot updates to
	// (TOLKOVO_PUBLIC_URL). Bots fall back to polling unless it is https
	// on a port Telegram accepts.
	HookBaseURL string
	// Inbox stores received messages and Files their media (T-02). The
	// api role needs them for webhooks, the worker for polling and
	// personal accounts.
	Inbox Inbox
	Files Files
	Log   *slog.Logger
	// Jobs queues replies for delivery (T-03): the api role inserts, the
	// worker runs them.
	Jobs *jobs.Client
	// Redis holds the send limits, the marks against echoes of a personal
	// account and the operators' actions (typing, read) for the worker.
	Redis *redis.Client
}

// Service is the module facade.
type Service struct {
	db      *db.DB
	opts    Options
	log     *slog.Logger
	limiter *ratelimit.Limiter
	limits  sendLimits
	now     func() time.Time

	// live are the listening connections of personal accounts this process
	// holds; replies and actions go through them.
	liveMu sync.Mutex
	live   map[uuid.UUID]telegram.Conn
}

// New returns the channels service.
func New(d *db.DB, opts Options) *Service {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Service{db: d, opts: opts, log: log, now: func() time.Time { return time.Now().UTC() },
		limits: defaultLimits, live: map[uuid.UUID]telegram.Conn{},
	}
	if opts.Redis != nil {
		s.limiter = ratelimit.New(opts.Redis, "")
	}
	return s
}

// UserAccountsAvailable reports whether personal accounts can be connected.
func (s *Service) UserAccountsAvailable() bool { return s.opts.Users != nil && s.opts.Sealer != nil }

// EncryptionAvailable reports whether secrets can be stored at all.
func (s *Service) EncryptionAvailable() bool { return s.opts.Sealer != nil }

// List returns the workspace's channels, oldest first.
func (s *Service) List(ctx context.Context, workspaceID uuid.UUID) ([]Channel, error) {
	var out []Channel
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).ListChannels(ctx, workspaceID)
		if err != nil {
			return err
		}
		out = make([]Channel, 0, len(rows))
		for _, r := range rows {
			// Abandoned logins are not channels yet; the worker prunes them.
			if r.Status == StatusPending && s.now().Sub(r.CreatedAt) > LoginTTL {
				continue
			}
			out = append(out, channelFrom(r))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return out, nil
}

// Rename changes the channel's name in the panel.
func (s *Service) Rename(ctx context.Context, workspaceID, id uuid.UUID, name string) (Channel, error) {
	name, err := validName(name)
	if err != nil {
		return Channel{}, err
	}
	var ch Channel
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).RenameChannel(ctx, store.RenameChannelParams{
			WorkspaceID: workspaceID, ID: id, Name: name, UpdatedAt: s.now(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		ch = channelFrom(row)
		return err
	})
	if err != nil {
		return Channel{}, fmt.Errorf("rename channel: %w", err)
	}
	return ch, nil
}

// Disconnect removes the channel: a bot stops sending updates here
// (deleteWebhook), a personal account's session is ended in Telegram
// (auth.logOut). Telegram's answer does not block the removal — a revoked
// token or an already ended session must not leave a channel the admin
// cannot remove. The secret is deleted; the row stays, soft-deleted, for
// the conversations that reference it. An unfinished login is deleted.
func (s *Service) Disconnect(ctx context.Context, workspaceID, id uuid.UUID) error {
	ch, raw, err := s.load(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if raw != nil && ch.Status != StatusPending {
		s.release(ctx, ch, raw)
	}
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if ch.Status == StatusPending {
			return q.DeletePendingChannel(ctx, store.DeletePendingChannelParams{WorkspaceID: workspaceID, ID: id})
		}
		if err := q.DeleteSecret(ctx, store.DeleteSecretParams{WorkspaceID: workspaceID, ChannelID: id}); err != nil {
			return err
		}
		if err := q.DeleteUpdateState(ctx, store.DeleteUpdateStateParams{WorkspaceID: workspaceID, ChannelID: id}); err != nil {
			return err
		}
		if err := q.DeletePeers(ctx, store.DeletePeersParams{WorkspaceID: workspaceID, ChannelID: id}); err != nil {
			return err
		}
		now := s.now()
		return q.SoftDeleteChannel(ctx, store.SoftDeleteChannelParams{WorkspaceID: workspaceID, ID: id, DeletedAt: &now})
	})
	if err != nil {
		return fmt.Errorf("disconnect channel: %w", err)
	}
	return nil
}

// release tells Telegram the channel is gone, best effort.
func (s *Service) release(ctx context.Context, ch Channel, raw []byte) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch ch.Type {
	case TypeTelegramBot:
		var sec botSecret
		if json.Unmarshal(raw, &sec) == nil && s.opts.Bots != nil {
			_ = s.opts.Bots.DeleteWebhook(ctx, sec.Token)
		}
	case TypeTelegramUser:
		var sec userSecret
		if json.Unmarshal(raw, &sec) == nil && s.opts.Users != nil && len(sec.Session) > 0 {
			_ = s.opts.Users.LogOut(ctx, sec.Session)
		}
	}
}

// load returns the channel and its opened secret (nil when it has none).
func (s *Service) load(ctx context.Context, workspaceID, id uuid.UUID) (Channel, []byte, error) {
	var (
		row    store.Channel
		sealed []byte
	)
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		row, err = q.GetChannel(ctx, store.GetChannelParams{WorkspaceID: workspaceID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		sealed, err = q.GetSecret(ctx, store.GetSecretParams{WorkspaceID: workspaceID, ChannelID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			sealed = nil
			return nil
		}
		return err
	})
	if err != nil {
		return Channel{}, nil, fmt.Errorf("load channel: %w", err)
	}
	ch := channelFrom(row)
	if sealed == nil || s.opts.Sealer == nil {
		return ch, nil, nil
	}
	sec, err := s.opts.Sealer.Open(ctx, sealed, crypto.AAD{WorkspaceID: workspaceID, SecretID: id})
	if err != nil {
		return Channel{}, nil, fmt.Errorf("open channel secret: %w", err)
	}
	return ch, []byte(sec.Reveal()), nil
}

// seal encrypts a secret for the channel row.
func (s *Service) seal(ctx context.Context, workspaceID, id uuid.UUID, v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode channel secret: %w", err)
	}
	return s.opts.Sealer.Seal(ctx, raw, crypto.AAD{WorkspaceID: workspaceID, SecretID: id})
}

func channelFrom(r store.Channel) Channel {
	ch := Channel{ID: r.ID, Type: r.Type, Name: r.Name, Status: r.Status, CreatedAt: r.CreatedAt, LastInboundAt: r.LastInboundAt}
	if r.ErrorCode != nil {
		ch.ErrorCode = *r.ErrorCode
	}
	_ = json.Unmarshal(r.Config, &ch.Details) // written only by this module
	return ch
}

func validName(name string) (string, error) {
	n := []rune(strings.TrimSpace(name))
	if len(n) == 0 || len(n) > 120 {
		return "", fmt.Errorf("%w: name must be 1 to 120 characters", ErrInvalid)
	}
	return string(n), nil
}

// isUniqueViolation reports a duplicate bot or account (channels_external_uq).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// telegramErr keeps the module's and Telegram's known errors and hides
// the rest (network failures, unexpected answers) behind ErrTelegram.
func telegramErr(err error) error {
	var flood *telegram.FloodWaitError
	switch {
	case errors.As(err, &flood):
		return err
	case errors.Is(err, telegram.ErrBotToken):
		return ErrBotToken
	case errors.Is(err, telegram.ErrPhoneInvalid), errors.Is(err, telegram.ErrNoAccount), errors.Is(err, telegram.ErrBanned),
		errors.Is(err, telegram.ErrCodeInvalid), errors.Is(err, telegram.ErrCodeExpired),
		errors.Is(err, telegram.ErrPasswordInvalid), errors.Is(err, telegram.ErrPasswordNeeded):
		return err
	case errors.Is(err, context.Canceled):
		return err
	}
	return fmt.Errorf("%w: %w", ErrTelegram, err)
}
