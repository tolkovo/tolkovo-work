// Package waitlist keeps the waitlist of the cloud beta with double opt-in
// (docs/05 §6, backlog L-01). An address is on the list only after its
// owner opens the link from the confirmation email and presses the button
// on the landing; unconfirmed entries are purged.
//
// Entries are a global table (migration 00005), reached through
// db.InGlobalTx. Emails are sent by a River job created in the same
// transaction as the entry, never from the request. HTTP lives in
// internal/api.
package waitlist

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	netmail "net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/waitlist/store"
)

// ConsentVersion identifies the consent text of the landing form
// (apps/landing/src/i18n, waitlist.consent). Change it together with the
// text, so every entry records what its owner agreed to.
const ConsentVersion = "2026-10-04"

// MaxSends caps confirmation emails per address, so the form cannot be used
// to flood someone's inbox over days.
const MaxSends = 5

// Channels lists the accepted channel values (api/openapi.yaml,
// WaitlistChannel; migration 00005 checks the same set).
var Channels = []string{"telegram", "web", "whatsapp", "vk_max", "email"}

// Domain errors. The api layer maps them to problem+json codes.
var (
	ErrInvalid      = errors.New("waitlist: invalid input")
	ErrTokenInvalid = errors.New("waitlist: unknown or expired token")
)

// RateLimitedError is returned when too many requests were made.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("waitlist: too many requests, retry after %s", e.RetryAfter)
}

// Rate limits (docs/02 §13.4). The email limit applies whether or not the
// address is known, so a 429 reveals nothing about the list.
var (
	joinPerIP    = ratelimit.Rule{Limit: 10, Window: time.Hour}
	joinPerEmail = ratelimit.Rule{Limit: 3, Window: time.Hour}
	tokenPerIP   = ratelimit.Rule{Limit: 30, Window: time.Hour}
)

// tokenByteSize is the entropy of confirmation and unsubscribe tokens.
const tokenByteSize = 32

// JobInserter inserts River jobs in a caller's transaction (jobs.Client).
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Options configure New.
type Options struct {
	// KeyPrefix is prepended to Redis keys; tests use a unique one.
	KeyPrefix string
	// Now defaults to time.Now.
	Now func() time.Time
}

// Service is the module facade for the HTTP layer.
type Service struct {
	db      *db.DB
	jobs    JobInserter
	limiter *ratelimit.Limiter
	now     func() time.Time
}

// New returns the service. jobs may be an insert-only River client.
func New(database *db.DB, jobs JobInserter, rdb *redis.Client, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{db: database, jobs: jobs, limiter: ratelimit.New(rdb, o.KeyPrefix), now: o.Now}
}

// JoinInput is one form submission.
type JoinInput struct {
	Email    string
	Locale   string
	Channels []string
	Consent  bool
	// Honeypot is a field hidden from people; bots fill it.
	Honeypot string
}

// Join saves a pending entry and queues the confirmation email in the same
// transaction. It returns nil for a new, a pending and a confirmed address
// alike; a confirmed entry is left unchanged and gets no email.
func (s *Service) Join(ctx context.Context, in JoinInput, clientIP string) error {
	if err := s.allow(ctx, "waitlist:ip:"+digest(clientIP), joinPerIP); err != nil {
		return err
	}
	if in.Honeypot != "" {
		return nil // look like success to the bot, store nothing
	}
	entry, err := validate(in)
	if err != nil {
		return err
	}
	if err := s.allow(ctx, "waitlist:email:"+digest(entry.Email), joinPerEmail); err != nil {
		return err
	}
	entry.ID = uuid.Must(uuid.NewV7())
	entry.ConsentVersion = ConsentVersion
	entry.Now = s.now().UTC()
	return s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		row, err := store.New(tx).UpsertPending(ctx, entry)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // confirmed: nothing to change, nothing to send
		}
		if err != nil {
			return fmt.Errorf("waitlist: save entry: %w", err)
		}
		if row.SendCount >= MaxSends {
			return nil
		}
		if _, err := s.jobs.InsertTx(ctx, tx, SendConfirmationArgs{EntryID: row.ID}, nil); err != nil {
			return fmt.Errorf("waitlist: queue confirmation of %s: %w", row.ID, err)
		}
		return nil
	})
}

// Confirm puts the entry of a valid confirmation token on the list.
// Confirming again with the same link while it is valid is not an error.
func (s *Service) Confirm(ctx context.Context, token, clientIP string) error {
	if err := s.allow(ctx, "waitlist:token:"+digest(clientIP), tokenPerIP); err != nil {
		return err
	}
	hash, err := hashToken(token)
	if err != nil {
		return err
	}
	return s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		_, err := store.New(tx).Confirm(ctx, store.ConfirmParams{TokenHash: hash, Now: s.now().UTC()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		}
		if err != nil {
			return fmt.Errorf("waitlist: confirm: %w", err)
		}
		return nil
	})
}

// Unsubscribe deletes the entry of an unsubscribe token (GDPR art. 17).
func (s *Service) Unsubscribe(ctx context.Context, token, clientIP string) error {
	if err := s.allow(ctx, "waitlist:token:"+digest(clientIP), tokenPerIP); err != nil {
		return err
	}
	hash, err := hashToken(token)
	if err != nil {
		return err
	}
	return s.db.InGlobalTx(ctx, func(tx pgx.Tx) error {
		n, err := store.New(tx).DeleteByUnsubscribeToken(ctx, hash)
		if err != nil {
			return fmt.Errorf("waitlist: unsubscribe: %w", err)
		}
		if n == 0 {
			return ErrTokenInvalid
		}
		return nil
	})
}

// validate checks a submission and normalises it into the row to upsert.
func validate(in JoinInput) (store.UpsertPendingParams, error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return store.UpsertPendingParams{}, err
	}
	if in.Locale != "ru" && in.Locale != "en" {
		return store.UpsertPendingParams{}, fmt.Errorf("%w: locale must be ru or en", ErrInvalid)
	}
	if !in.Consent {
		return store.UpsertPendingParams{}, fmt.Errorf("%w: consent must be true", ErrInvalid)
	}
	channels := make([]string, 0, len(in.Channels))
	for _, c := range in.Channels {
		if !slices.Contains(Channels, c) {
			return store.UpsertPendingParams{}, fmt.Errorf("%w: unknown channel", ErrInvalid)
		}
		if !slices.Contains(channels, c) {
			channels = append(channels, c)
		}
	}
	slices.Sort(channels)
	return store.UpsertPendingParams{Email: email, Locale: in.Locale, Channels: channels}, nil
}

// normalizeEmail trims and lower-cases a bare address. Lower-casing the
// local part is technically lossy but no real mailbox depends on it, and it
// keeps one entry per person.
func normalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if len(email) < 3 || len(email) > 254 {
		return "", fmt.Errorf("%w: email must be 3-254 characters", ErrInvalid)
	}
	addr, err := netmail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return "", fmt.Errorf("%w: email is not a valid address", ErrInvalid)
	}
	return strings.ToLower(email), nil
}

// newToken returns a random URL-safe token and the SHA-256 that is stored.
func newToken() (string, []byte, error) {
	b := make([]byte, tokenByteSize)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("waitlist: random token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, sum[:], nil
}

// hashToken checks the token shape and returns its stored hash. A malformed
// token is the same ErrTokenInvalid as an unknown one.
func hashToken(token string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != tokenByteSize {
		return nil, ErrTokenInvalid
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:], nil
}

func (s *Service) allow(ctx context.Context, key string, rule ratelimit.Rule) error {
	ok, retry, err := s.limiter.Allow(ctx, key, rule)
	if err != nil {
		return err // fail closed: without the limiter the form is a spam relay
	}
	if !ok {
		return &RateLimitedError{RetryAfter: retry}
	}
	return nil
}

// digest keeps emails and IPs out of Redis key names.
func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
