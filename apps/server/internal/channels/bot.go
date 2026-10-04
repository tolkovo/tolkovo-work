package channels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
)

// botSecret is the sealed secret of a bot channel.
type botSecret struct {
	Token string `json:"token"`
	// WebhookSecret comes back in X-Telegram-Bot-Api-Secret-Token with
	// every update (docs/02 §11.3).
	WebhookSecret string `json:"webhook_secret"`
}

// BotUpdates are the updates a bot channel asks Telegram for (docs/02 §11.5).
var BotUpdates = []string{"message", "edited_message", "callback_query", "my_chat_member"}

// HookPath is where Telegram posts a bot's updates, under HookBaseURL. The
// workspace is part of the path: the handler needs it for the tenant
// transaction before it can read the channel.
func HookPath(workspaceID, channelID uuid.UUID) string {
	return "/hooks/telegram/" + workspaceID.String() + "/" + channelID.String()
}

// ConnectBot connects a bot by its token from @BotFather: getMe proves the
// token, then updates are pointed here — a webhook when the server has a
// public https URL, otherwise polling by the worker. name may be empty:
// the bot's name is used.
func (s *Service) ConnectBot(ctx context.Context, workspaceID, actorID uuid.UUID, token, name string) (Channel, error) {
	token = strings.TrimSpace(token)
	if s.opts.Sealer == nil {
		return Channel{}, ErrNoEncryption
	}
	if !telegram.ValidBotToken(token) {
		return Channel{}, fmt.Errorf("%w: the token must look like 123456789:AA… (copy it from @BotFather)", ErrInvalid)
	}
	if strings.TrimSpace(name) != "" {
		var err error
		if name, err = validName(name); err != nil {
			return Channel{}, err
		}
	}
	bot, err := s.opts.Bots.GetMe(ctx, token)
	if err != nil {
		return Channel{}, fmt.Errorf("connect bot: %w", telegramErr(err))
	}
	if name == "" {
		name = bot.FirstName
		if name == "" {
			name = "@" + bot.Username
		}
		name, _ = validName(name)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Channel{}, err
	}
	hookSecret, err := randomHex(32)
	if err != nil {
		return Channel{}, err
	}
	delivery := s.botDelivery()
	details := Details{Username: bot.Username, DisplayName: bot.FirstName, Delivery: delivery}

	// The row is claimed first, pending: a bot that is a channel already
	// must not get its webhook re-pointed by a second attempt.
	if err := s.insertPending(ctx, workspaceID, actorID, id, TypeTelegramBot, name, strconv.FormatInt(bot.ID, 10), details,
		botSecret{Token: token, WebhookSecret: hookSecret}); err != nil {
		return Channel{}, fmt.Errorf("connect bot: %w", err)
	}
	if delivery == DeliveryWebhook {
		hook := strings.TrimRight(s.opts.HookBaseURL, "/") + HookPath(workspaceID, id)
		err = s.opts.Bots.SetWebhook(ctx, token, hook, hookSecret, BotUpdates)
	} else {
		err = s.opts.Bots.DeleteWebhook(ctx, token)
	}
	if err != nil {
		s.dropPending(ctx, workspaceID, id)
		return Channel{}, fmt.Errorf("connect bot: %w", telegramErr(err))
	}
	ch, err := s.activate(ctx, workspaceID, id, func(_ store.Channel) (string, *string, Details, any) {
		return name, nil, details, nil
	})
	if err != nil {
		return Channel{}, fmt.Errorf("connect bot: %w", err)
	}
	return ch, nil
}

// botDelivery picks webhook when Telegram can reach HookBaseURL: https on
// port 443, 80, 88 or 8443 (Bot API requirement).
func (s *Service) botDelivery() string {
	u, err := url.Parse(s.opts.HookBaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return DeliveryPolling
	}
	switch u.Port() {
	case "", "443", "80", "88", "8443":
		return DeliveryWebhook
	}
	return DeliveryPolling
}

// insertPending inserts a pending channel with its sealed secret. A
// duplicate bot or account is ErrAlreadyConnected.
func (s *Service) insertPending(
	ctx context.Context, workspaceID, actorID, id uuid.UUID, typ, name, externalID string, details Details, secret any,
) error {
	sealed, err := s.seal(ctx, workspaceID, id, secret)
	if err != nil {
		return err
	}
	cfg, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var ext *string
	if externalID != "" {
		ext = &externalID
	}
	now := s.now()
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.InsertChannel(ctx, store.InsertChannelParams{
			ID: id, WorkspaceID: workspaceID, Type: typ, Name: name, Status: StatusPending,
			ExternalID: ext, Config: cfg, CreatedBy: &actorID, CreatedAt: now,
		}); err != nil {
			return err
		}
		return q.PutSecret(ctx, store.PutSecretParams{WorkspaceID: workspaceID, ChannelID: id, Sealed: sealed, UpdatedAt: now})
	})
	if isUniqueViolation(err) {
		return ErrAlreadyConnected
	}
	return err
}

// activate turns a pending channel active. next computes the final name,
// external id, details and, when not nil, a new secret, from the locked
// row. A channel that is no longer pending (removed or finished by a
// concurrent request) is ErrLoginStep.
func (s *Service) activate(
	ctx context.Context, workspaceID, id uuid.UUID, next func(store.Channel) (string, *string, Details, any),
) (Channel, error) {
	var ch Channel
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.GetChannelForUpdate(ctx, store.GetChannelForUpdateParams{WorkspaceID: workspaceID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Status != StatusPending) {
			return ErrLoginStep
		}
		if err != nil {
			return err
		}
		name, ext, details, secret := next(row)
		if ext == nil {
			ext = row.ExternalID
		}
		cfg, err := json.Marshal(details)
		if err != nil {
			return err
		}
		now := s.now()
		if secret != nil {
			sealed, err := s.seal(ctx, workspaceID, id, secret)
			if err != nil {
				return err
			}
			if err := q.PutSecret(ctx, store.PutSecretParams{WorkspaceID: workspaceID, ChannelID: id, Sealed: sealed, UpdatedAt: now}); err != nil {
				return err
			}
		}
		if name != row.Name {
			if _, err := q.RenameChannel(ctx, store.RenameChannelParams{WorkspaceID: workspaceID, ID: id, Name: name, UpdatedAt: now}); err != nil {
				return err
			}
		}
		updated, err := q.UpdateChannelState(ctx, store.UpdateChannelStateParams{
			WorkspaceID: workspaceID, ID: id, Status: StatusActive, ErrorCode: nil, ExternalID: ext, Config: cfg, UpdatedAt: now,
		})
		ch = channelFrom(updated)
		return err
	})
	if isUniqueViolation(err) {
		return Channel{}, ErrAlreadyConnected
	}
	return ch, err
}

// dropPending deletes a pending channel after a failed step, best effort:
// the worker prunes what is left.
func (s *Service) dropPending(ctx context.Context, workspaceID, id uuid.UUID) {
	ctx = context.WithoutCancel(ctx)
	_ = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).DeletePendingChannel(ctx, store.DeletePendingChannelParams{WorkspaceID: workspaceID, ID: id})
	})
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
