package channels

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

// The «Приложение» channel (AP-01, docs/02 §11.6): a company's own app sends
// problem reports with logs. The app carries an ingest key that only
// creates reports; the company's backend keeps the secret that signs the
// app user's identity.

// TypeApp is a channel of reports from a company app.
const TypeApp = "app"

// MaxIngestKeys bounds the keys of a channel that are not revoked.
const MaxIngestKeys = 20

// Key formats. An ingest key is the prefix and base64url of 32 random bytes
// followed by the workspace id: the ingest API (AP-02) learns the tenant
// from the key itself and looks the hash up inside that tenant's
// transaction, so no policy has to see keys across workspaces (as with
// invitation tokens, docs/02 §13.1).
const (
	ingestKeyPrefix      = "tk_ingest_"
	identitySecretPrefix = "tk_identity_"
	// keyShownChars of the random part stay visible in the panel.
	keyShownChars = 6
)

// ErrTooManyKeys means MaxIngestKeys keys are in use.
var ErrTooManyKeys = errors.New("channels: too many ingest keys, revoke one first")

// AppLimits are the limits of an app channel, set by the admin within the
// platform's maximums (docs/02 §11.6).
type AppLimits struct {
	MaxFileMB     int `json:"max_file_mb"`
	MaxFiles      int `json:"max_files"`
	MaxReportMB   int `json:"max_report_mb"`
	RetentionDays int `json:"retention_days"`
	DailyReports  int `json:"daily_reports"`
	DailyMB       int `json:"daily_mb"`
}

// DefaultAppLimits apply to a new app channel.
var DefaultAppLimits = AppLimits{MaxFileMB: 25, MaxFiles: 5, MaxReportMB: 50, RetentionDays: 30, DailyReports: 500, DailyMB: 2000}

// maxAppLimits are the platform's maximums.
var maxAppLimits = AppLimits{MaxFileMB: 25, MaxFiles: 5, MaxReportMB: 50, RetentionDays: 365, DailyReports: 10000, DailyMB: 100000}

func (l AppLimits) validate() error {
	for _, f := range []struct {
		name     string
		val, max int
	}{
		{"max_file_mb", l.MaxFileMB, maxAppLimits.MaxFileMB},
		{"max_files", l.MaxFiles, maxAppLimits.MaxFiles},
		{"max_report_mb", l.MaxReportMB, maxAppLimits.MaxReportMB},
		{"retention_days", l.RetentionDays, maxAppLimits.RetentionDays},
		{"daily_reports", l.DailyReports, maxAppLimits.DailyReports},
		{"daily_mb", l.DailyMB, maxAppLimits.DailyMB},
	} {
		if f.val < 1 || f.val > f.max {
			return fmt.Errorf("%w: %s must be 1 to %d", ErrInvalid, f.name, f.max)
		}
	}
	return nil
}

// appSecret is the sealed secret of an app channel.
type appSecret struct {
	IdentitySecret string `json:"identity_secret"`
}

// IngestKey is what the panel may see of a key.
type IngestKey struct {
	ID         uuid.UUID
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// NewIngestKey is a key just issued: Secret is shown once and never stored.
type NewIngestKey struct {
	Key    IngestKey
	Secret crypto.Secret
}

// AppChannel is a new app channel with what is shown only once.
type AppChannel struct {
	Channel        Channel
	Key            NewIngestKey
	IdentitySecret crypto.Secret
}

// ConnectApp creates an app channel with its first ingest key and identity
// secret. Nothing is asked of a third party, so the channel is active at once.
func (s *Service) ConnectApp(ctx context.Context, workspaceID, actorID uuid.UUID, name string) (AppChannel, error) {
	if s.opts.Sealer == nil {
		return AppChannel{}, ErrNoEncryption
	}
	name, err := validName(name)
	if err != nil {
		return AppChannel{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return AppChannel{}, err
	}
	identity, err := newIdentitySecret()
	if err != nil {
		return AppChannel{}, err
	}
	sealed, err := s.seal(ctx, workspaceID, id, appSecret{IdentitySecret: identity.Reveal()})
	if err != nil {
		return AppChannel{}, err
	}
	limits := DefaultAppLimits
	cfg, err := json.Marshal(Details{AppLimits: &limits})
	if err != nil {
		return AppChannel{}, err
	}
	var out AppChannel
	now := s.now()
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := q.InsertChannel(ctx, store.InsertChannelParams{
			ID: id, WorkspaceID: workspaceID, Type: TypeApp, Name: name, Status: StatusActive,
			Config: cfg, CreatedBy: &actorID, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if err := q.PutSecret(ctx, store.PutSecretParams{WorkspaceID: workspaceID, ChannelID: id, Sealed: sealed, UpdatedAt: now}); err != nil {
			return err
		}
		// The first key is named after the channel; more keys get their own names.
		key, err := insertIngestKey(ctx, q, workspaceID, id, actorID, name, now)
		if err != nil {
			return err
		}
		out = AppChannel{Channel: channelFrom(row), Key: key, IdentitySecret: identity}
		return nil
	})
	if err != nil {
		return AppChannel{}, fmt.Errorf("connect app: %w", err)
	}
	return out, nil
}

// IngestKeys returns the keys of an app channel, newest first.
func (s *Service) IngestKeys(ctx context.Context, workspaceID, channelID uuid.UUID) ([]IngestKey, error) {
	var out []IngestKey
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := appChannel(ctx, q, workspaceID, channelID); err != nil {
			return err
		}
		rows, err := q.ListIngestKeys(ctx, store.ListIngestKeysParams{WorkspaceID: workspaceID, ChannelID: channelID})
		if err != nil {
			return err
		}
		out = make([]IngestKey, 0, len(rows))
		for _, r := range rows {
			out = append(out, ingestKeyFrom(r))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	return out, nil
}

// CreateIngestKey issues one more key for an app channel.
func (s *Service) CreateIngestKey(ctx context.Context, workspaceID, actorID, channelID uuid.UUID, name string) (NewIngestKey, error) {
	name, err := validName(name)
	if err != nil {
		return NewIngestKey{}, err
	}
	var out NewIngestKey
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		// The channel row is locked: two requests at once must not both pass
		// the count.
		ch, err := q.GetChannelForUpdate(ctx, store.GetChannelForUpdateParams{WorkspaceID: workspaceID, ID: channelID})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && ch.Type != TypeApp) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		n, err := q.CountActiveIngestKeys(ctx, store.CountActiveIngestKeysParams{WorkspaceID: workspaceID, ChannelID: channelID})
		if err != nil {
			return err
		}
		if n >= MaxIngestKeys {
			return ErrTooManyKeys
		}
		out, err = insertIngestKey(ctx, q, workspaceID, channelID, actorID, name, s.now())
		return err
	})
	if err != nil {
		return NewIngestKey{}, fmt.Errorf("create ingest key: %w", err)
	}
	return out, nil
}

// RevokeIngestKey stops a key; reports already sent with it stay.
func (s *Service) RevokeIngestKey(ctx context.Context, workspaceID, channelID, keyID uuid.UUID) error {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := appChannel(ctx, q, workspaceID, channelID); err != nil {
			return err
		}
		_, err := q.RevokeIngestKey(ctx, store.RevokeIngestKeyParams{
			WorkspaceID: workspaceID, ChannelID: channelID, ID: keyID, RevokedAt: ptr(s.now()),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("revoke ingest key: %w", err)
	}
	return nil
}

// RotateIdentitySecret replaces the secret that signs the app user's
// identity; the old one stops working at once.
func (s *Service) RotateIdentitySecret(ctx context.Context, workspaceID, channelID uuid.UUID) (crypto.Secret, error) {
	if s.opts.Sealer == nil {
		return crypto.Secret{}, ErrNoEncryption
	}
	identity, err := newIdentitySecret()
	if err != nil {
		return crypto.Secret{}, err
	}
	sealed, err := s.seal(ctx, workspaceID, channelID, appSecret{IdentitySecret: identity.Reveal()})
	if err != nil {
		return crypto.Secret{}, err
	}
	err = s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := appChannel(ctx, q, workspaceID, channelID); err != nil {
			return err
		}
		return q.PutSecret(ctx, store.PutSecretParams{WorkspaceID: workspaceID, ChannelID: channelID, Sealed: sealed, UpdatedAt: s.now()})
	})
	if err != nil {
		return crypto.Secret{}, fmt.Errorf("rotate identity secret: %w", err)
	}
	return identity, nil
}

// SetAppLimits changes the limits of an app channel.
func (s *Service) SetAppLimits(ctx context.Context, workspaceID, channelID uuid.UUID, limits AppLimits) (Channel, error) {
	if err := limits.validate(); err != nil {
		return Channel{}, err
	}
	var ch Channel
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		q := store.New(tx)
		row, err := appChannel(ctx, q, workspaceID, channelID)
		if err != nil {
			return err
		}
		details := channelFrom(row).Details
		details.AppLimits = &limits
		cfg, err := json.Marshal(details)
		if err != nil {
			return err
		}
		row, err = q.UpdateChannelConfig(ctx, store.UpdateChannelConfigParams{
			WorkspaceID: workspaceID, ID: channelID, Config: cfg, UpdatedAt: s.now(),
		})
		ch = channelFrom(row)
		return err
	})
	if err != nil {
		return Channel{}, fmt.Errorf("set app limits: %w", err)
	}
	return ch, nil
}

// appChannel returns the app channel or ErrNotFound, also for channels of
// another type.
func appChannel(ctx context.Context, q *store.Queries, workspaceID, id uuid.UUID) (store.Channel, error) {
	row, err := q.GetChannel(ctx, store.GetChannelParams{WorkspaceID: workspaceID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.Type != TypeApp) {
		return store.Channel{}, ErrNotFound
	}
	return row, err
}

func insertIngestKey(
	ctx context.Context, q *store.Queries, workspaceID, channelID, actorID uuid.UUID, name string, now time.Time,
) (NewIngestKey, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return NewIngestKey{}, err
	}
	secret, err := newIngestKey(workspaceID)
	if err != nil {
		return NewIngestKey{}, err
	}
	hash := sha256.Sum256([]byte(secret.Reveal()))
	row, err := q.InsertIngestKey(ctx, store.InsertIngestKeyParams{
		ID: id, WorkspaceID: workspaceID, ChannelID: channelID, Name: name, KeyHash: hash[:],
		Prefix: secret.Reveal()[:len(ingestKeyPrefix)+keyShownChars], CreatedBy: &actorID, CreatedAt: now,
	})
	if err != nil {
		return NewIngestKey{}, err
	}
	return NewIngestKey{Key: ingestKeyFrom(row), Secret: secret}, nil
}

func ingestKeyFrom(r store.IngestKey) IngestKey {
	return IngestKey{ID: r.ID, Name: r.Name, Prefix: r.Prefix, CreatedAt: r.CreatedAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt}
}

// newIngestKey returns tk_ingest_ and base64url(32 random bytes ‖ workspace id).
func newIngestKey(workspaceID uuid.UUID) (crypto.Secret, error) {
	b := make([]byte, 32, 48)
	if _, err := rand.Read(b); err != nil {
		return crypto.Secret{}, fmt.Errorf("random: %w", err)
	}
	b = append(b, workspaceID[:]...)
	return crypto.NewSecret(ingestKeyPrefix + base64.RawURLEncoding.EncodeToString(b)), nil
}

func newIdentitySecret() (crypto.Secret, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return crypto.Secret{}, fmt.Errorf("random: %w", err)
	}
	return crypto.NewSecret(identitySecretPrefix + base64.RawURLEncoding.EncodeToString(b)), nil
}

func ptr[T any](v T) *T { return &v }
