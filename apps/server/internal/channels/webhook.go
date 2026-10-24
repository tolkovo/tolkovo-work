package channels

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// ErrHookSecret rejects a webhook request without the bot's secret token.
var ErrHookSecret = errors.New("channels: webhook secret does not match")

// BotWebhook handles an update Telegram posted to HookPath (docs/02 §11.2):
// the X-Telegram-Bot-Api-Secret-Token must match, then a private message
// is stored before the answer, so Telegram keeps the order within a chat
// and retries what failed; a press of a topic button picks the topic.
// Anything else in the update is acknowledged and dropped. ErrNotFound covers unknown, inactive and polling channels.
func (s *Service) BotWebhook(ctx context.Context, workspaceID, channelID uuid.UUID, secret string, body []byte) error {
	sec, err := s.activeBot(ctx, workspaceID, channelID)
	if err != nil {
		return err
	}
	if sec.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(sec.WebhookSecret)) != 1 {
		return ErrHookSecret
	}
	u, err := telegram.ParseBotUpdate(body)
	if err != nil {
		// A retry would bring the same bytes: acknowledge and move on.
		s.log.Warn("telegram update unreadable", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		return nil
	}
	if u.Callback != nil {
		s.callback(ctx, workspaceID, channelID, sec.Token, *u.Callback)
		return nil
	}
	if u.Message == nil {
		return nil
	}
	return s.deliver(ctx, workspaceID, channelID, *u.Message, s.botFetch(sec.Token), BotFileLimit, sec.Token)
}

// activeBot opens the secret of an active bot channel that gets webhooks.
func (s *Service) activeBot(ctx context.Context, workspaceID, channelID uuid.UUID) (botSecret, error) {
	ch, raw, err := s.loadActive(ctx, workspaceID, channelID)
	if err != nil {
		return botSecret{}, err
	}
	if ch.Type != TypeTelegramBot || ch.Details.Delivery != DeliveryWebhook {
		return botSecret{}, ErrNotFound
	}
	var sec botSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return botSecret{}, fmt.Errorf("decode bot secret: %w", err)
	}
	return sec, nil
}

// loadActive returns an active channel and its opened secret.
func (s *Service) loadActive(ctx context.Context, workspaceID, channelID uuid.UUID) (Channel, []byte, error) {
	if s.opts.Sealer == nil {
		return Channel{}, nil, ErrNotFound
	}
	var (
		row    store.Channel
		sealed []byte
	)
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if row, err = q.GetActiveChannel(ctx, store.GetActiveChannelParams{WorkspaceID: workspaceID, ID: channelID}); err != nil {
			return err
		}
		sealed, err = q.GetSecret(ctx, store.GetSecretParams{WorkspaceID: workspaceID, ChannelID: channelID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, nil, ErrNotFound
	}
	if err != nil {
		return Channel{}, nil, fmt.Errorf("load channel: %w", err)
	}
	sec, err := s.opts.Sealer.Open(ctx, sealed, crypto.AAD{WorkspaceID: workspaceID, SecretID: channelID})
	if err != nil {
		return Channel{}, nil, fmt.Errorf("open channel secret: %w", err)
	}
	return channelFrom(row), []byte(sec.Reveal()), nil
}

func (s *Service) botFetch(token string) fetchFunc {
	return func(ctx context.Context, f telegram.File, w io.Writer, limit int64) error {
		return s.opts.Bots.Download(ctx, token, f, w, limit)
	}
}
