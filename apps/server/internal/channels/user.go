package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
)

// userSecret is the sealed secret of a personal-account channel. While the
// login runs it also holds the phone and the code hash; after it, only
// the session. The cloud password is never stored.
type userSecret struct {
	Session  []byte `json:"session"`
	Phone    string `json:"phone,omitempty"`
	CodeHash string `json:"code_hash,omitempty"`
	Step     string `json:"step,omitempty"`
}

// StartUserLogin begins connecting a personal account: Telegram sends a
// login code to the account (usually in the Telegram app). The channel is
// pending until SubmitCode or SubmitPassword finishes the login, at most
// LoginTTL.
func (s *Service) StartUserLogin(ctx context.Context, workspaceID, actorID uuid.UUID, phone string) (Login, error) {
	if !s.UserAccountsAvailable() {
		if s.opts.Sealer == nil {
			return Login{}, ErrNoEncryption
		}
		return Login{}, ErrUserUnavailable
	}
	phone, ok := telegram.NormalizePhone(phone)
	if !ok {
		return Login{}, fmt.Errorf("%w: enter the number in international format, e.g. +7 900 123-45-67", ErrInvalid)
	}
	sess, sent, err := s.opts.Users.SendCode(ctx, nil, phone)
	if err != nil {
		return Login{}, fmt.Errorf("start login: %w", telegramErr(err))
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Login{}, err
	}
	masked := telegram.MaskPhone(phone)
	name, _ := validName("Telegram " + masked)
	if err := s.insertPending(ctx, workspaceID, actorID, id, TypeTelegramUser, name, "", Details{PhoneMasked: masked},
		userSecret{Session: sess, Phone: phone, CodeHash: sent.Hash, Step: StepCode}); err != nil {
		return Login{}, fmt.Errorf("start login: %w", err)
	}
	ch, err := s.pendingChannel(ctx, workspaceID, id)
	if err != nil {
		return Login{}, err
	}
	return Login{Channel: ch, Next: StepCode, CodeVia: sent.Via}, nil
}

// SubmitCode checks the login code. The login is done, or needs the cloud
// password (Next = StepPassword). A wrong code may be retried; an expired
// one needs a new login.
func (s *Service) SubmitCode(ctx context.Context, workspaceID, id uuid.UUID, code string) (Login, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 16 {
		return Login{}, fmt.Errorf("%w: enter the code from Telegram", ErrInvalid)
	}
	ch, sec, err := s.loginState(ctx, workspaceID, id, StepCode)
	if err != nil {
		return Login{}, err
	}
	sess, acc, err := s.opts.Users.SignIn(ctx, sec.Session, sec.Phone, sec.CodeHash, code)
	if errors.Is(err, telegram.ErrPasswordNeeded) {
		sec.Session, sec.Step = sess, StepPassword
		if err := s.saveLoginSecret(ctx, workspaceID, id, sec); err != nil {
			return Login{}, err
		}
		return Login{Channel: ch, Next: StepPassword}, nil
	}
	if err != nil {
		return Login{}, fmt.Errorf("submit code: %w", telegramErr(err))
	}
	return s.finishLogin(ctx, workspaceID, ch, sec.Phone, sess, acc)
}

// SubmitPassword completes a login of an account with a cloud password.
func (s *Service) SubmitPassword(ctx context.Context, workspaceID, id uuid.UUID, password string) (Login, error) {
	if password == "" || len(password) > 256 {
		return Login{}, fmt.Errorf("%w: enter the cloud password of the account", ErrInvalid)
	}
	ch, sec, err := s.loginState(ctx, workspaceID, id, StepPassword)
	if err != nil {
		return Login{}, err
	}
	sess, acc, err := s.opts.Users.Password(ctx, sec.Session, password)
	if err != nil {
		return Login{}, fmt.Errorf("submit password: %w", telegramErr(err))
	}
	return s.finishLogin(ctx, workspaceID, ch, sec.Phone, sess, acc)
}

// finishLogin activates the channel with the signed-in session. If the
// account is a channel already, the new session is ended at once: it
// would compete with the existing one for updates.
func (s *Service) finishLogin(
	ctx context.Context, workspaceID uuid.UUID, pending Channel, phone string, sess []byte, acc telegram.Account,
) (Login, error) {
	ext := strconv.FormatInt(acc.ID, 10)
	details := Details{Username: acc.Username, DisplayName: acc.Name(), PhoneMasked: telegram.MaskPhone(phone)}
	ch, err := s.activate(ctx, workspaceID, pending.ID, func(row store.Channel) (string, *string, Details, any) {
		name := row.Name
		// Still the placeholder from StartUserLogin: use the account's name.
		if name == "Telegram "+details.PhoneMasked && acc.Name() != "" {
			name, _ = validName(acc.Name())
		}
		return name, &ext, details, userSecret{Session: sess}
	})
	if errors.Is(err, ErrAlreadyConnected) {
		_ = s.opts.Users.LogOut(context.WithoutCancel(ctx), sess)
		s.dropPending(ctx, workspaceID, pending.ID)
		return Login{}, fmt.Errorf("finish login: %w", err)
	}
	if err != nil {
		return Login{}, fmt.Errorf("finish login: %w", err)
	}
	return Login{Channel: ch, Next: StepDone}, nil
}

// loginState returns a pending personal-account login waiting for step.
func (s *Service) loginState(ctx context.Context, workspaceID, id uuid.UUID, step string) (Channel, userSecret, error) {
	if !s.UserAccountsAvailable() {
		return Channel{}, userSecret{}, ErrUserUnavailable
	}
	ch, raw, err := s.load(ctx, workspaceID, id)
	if err != nil {
		return Channel{}, userSecret{}, err
	}
	if ch.Type != TypeTelegramUser || ch.Status != StatusPending || raw == nil {
		return Channel{}, userSecret{}, ErrLoginStep
	}
	if s.now().Sub(ch.CreatedAt) > LoginTTL {
		return Channel{}, userSecret{}, ErrLoginExpired
	}
	var sec userSecret
	if err := json.Unmarshal(raw, &sec); err != nil {
		return Channel{}, userSecret{}, err
	}
	if sec.Step != step {
		return Channel{}, userSecret{}, ErrLoginStep
	}
	return ch, sec, nil
}

func (s *Service) saveLoginSecret(ctx context.Context, workspaceID, id uuid.UUID, sec userSecret) error {
	sealed, err := s.seal(ctx, workspaceID, id, sec)
	if err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		return store.New(tx).PutSecret(ctx, store.PutSecretParams{
			WorkspaceID: workspaceID, ChannelID: id, Sealed: sealed, UpdatedAt: s.now(),
		})
	})
}

func (s *Service) pendingChannel(ctx context.Context, workspaceID, id uuid.UUID) (Channel, error) {
	var ch Channel
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetChannel(ctx, store.GetChannelParams{WorkspaceID: workspaceID, ID: id})
		ch = channelFrom(row)
		return err
	})
	return ch, err
}
