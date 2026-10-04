// Package auth signs users in and out: argon2id passwords, server-side
// sessions in Redis and brute-force limits (docs/06 A-01, A-02, docs/02 §13).
// It is the authentication half of the tenancy/auth module of docs/02 §3:
// accounts, workspaces and memberships stay in tenancy, which auth uses
// through its facade. HTTP (cookies, CSRF headers) lives in internal/api.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Domain errors. The api layer maps them to problem+json codes.
var (
	ErrInvalid            = errors.New("auth: invalid input")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrNoSession          = errors.New("auth: no valid session")
	ErrNoWorkspace        = errors.New("auth: user has no active workspace")
	ErrWorkspaceNotFound  = errors.New("auth: workspace not found")
	ErrWrongPassword      = errors.New("auth: current password does not match")
	ErrSessionNotFound    = errors.New("auth: session not found")
	ErrCurrentSession     = errors.New("auth: this is the current session")
)

// RateLimitedError is returned when too many attempts were made.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("auth: too many attempts, retry after %s", e.RetryAfter)
}

// Brute-force limits (docs/02 §13.4). Attempts are counted before the
// password is checked, so parallel guesses are limited as well.
var (
	loginPerIPAndEmail = ratelimit.Rule{Limit: 10, Window: time.Minute}
	loginPerEmail      = ratelimit.Rule{Limit: 50, Window: time.Hour} // guesses spread over many IPs
	registerPerIP      = ratelimit.Rule{Limit: 10, Window: time.Hour}
	// Someone with a stolen session cookie must not be able to guess the
	// password and take the account over.
	passwordChangePerUser = ratelimit.Rule{Limit: 10, Window: time.Hour}
)

// Client describes the browser that signs in. IP feeds the rate limits and
// is never stored; UserAgent is kept, shortened, in the session list.
type Client struct {
	IP        string
	UserAgent string
}

// Options configure the service.
type Options struct {
	// KeyPrefix is prepended to every Redis key; tests use a unique one.
	KeyPrefix string
	// SessionIdleTimeout ends a session after this long without requests.
	SessionIdleTimeout time.Duration
	// SessionMaxAge ends a session this long after sign-in, active or not.
	SessionMaxAge time.Duration
	// HashParams default to DefaultHashParams; tests use cheaper ones.
	HashParams HashParams
	// HashConcurrency bounds parallel argon2id computations (memory).
	HashConcurrency int
	// Now defaults to time.Now.
	Now func() time.Time
}

// Service is the module facade.
type Service struct {
	tenancy   *tenancy.Service
	sessions  *sessionStore
	limiter   *ratelimit.Limiter
	hasher    *hasher
	dummyHash string
}

// New returns the auth service.
func New(ctx context.Context, t *tenancy.Service, rdb *redis.Client, o Options) (*Service, error) {
	if o.SessionIdleTimeout <= 0 || o.SessionMaxAge <= 0 {
		return nil, errors.New("auth: session timeouts must be positive")
	}
	if o.HashParams == (HashParams{}) {
		o.HashParams = DefaultHashParams
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	h := newHasher(o.HashParams, o.HashConcurrency)
	// Login with an unknown email verifies against this hash, so it takes as
	// long as a wrong password and timing does not reveal registered emails.
	dummy, err := h.Hash(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &Service{
		tenancy: t,
		sessions: &sessionStore{
			rdb: rdb, prefix: o.KeyPrefix, idle: o.SessionIdleTimeout, maxAge: o.SessionMaxAge, now: o.Now,
		},
		limiter:   ratelimit.New(rdb, o.KeyPrefix),
		hasher:    h,
		dummyHash: dummy,
	}, nil
}

// SignIn is the result of register, login and workspace switch: the new
// session token for the cookie, the session and the membership it is for.
type SignIn struct {
	Token      string
	Session    Session
	Membership tenancy.Membership
}

// RegisterInput is a new account with its first workspace.
type RegisterInput struct {
	Email         string
	Password      string
	Name          string
	Locale        string
	WorkspaceName string
	WorkspaceSlug string
	Timezone      string
}

// Register creates the account, the workspace and the owner membership
// atomically and signs the owner in (docs/06 A-01).
func (s *Service) Register(ctx context.Context, in RegisterInput, c Client) (SignIn, error) {
	if err := s.allow(ctx, "register:ip:"+digest(c.IP), registerPerIP); err != nil {
		return SignIn{}, err
	}
	if err := validatePassword(in.Password); err != nil {
		return SignIn{}, err
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return SignIn{}, err
	}
	m, err := s.tenancy.Register(ctx, tenancy.RegisterInput{
		Email: in.Email, Name: in.Name, Locale: in.Locale, PasswordHash: hash,
		WorkspaceName: in.WorkspaceName, WorkspaceSlug: in.WorkspaceSlug, Timezone: in.Timezone,
	})
	if err != nil {
		return SignIn{}, err // tenancy errors are part of this contract
	}
	return s.start(ctx, m, c.UserAgent)
}

// Login checks the password and starts a session in the user's first
// workspace. Unknown email, wrong password and disabled account all give
// ErrInvalidCredentials after the same amount of work.
func (s *Service) Login(ctx context.Context, email, password string, c Client) (SignIn, error) {
	email = strings.TrimSpace(email)
	emailKey := digest(strings.ToLower(email))
	if err := s.allow(ctx, "login:ie:"+digest(c.IP+"|"+emailKey), loginPerIPAndEmail); err != nil {
		return SignIn{}, err
	}
	if err := s.allow(ctx, "login:e:"+emailKey, loginPerEmail); err != nil {
		return SignIn{}, err
	}

	creds, err := s.tenancy.Credentials(ctx, email)
	switch {
	case errors.Is(err, tenancy.ErrNotFound):
		if _, err := s.hasher.Verify(ctx, password, s.dummyHash); err != nil {
			return SignIn{}, err
		}
		return SignIn{}, ErrInvalidCredentials
	case err != nil:
		return SignIn{}, err
	}
	ok, err := s.hasher.Verify(ctx, password, creds.PasswordHash)
	if err != nil {
		return SignIn{}, fmt.Errorf("verify password of %s: %w", creds.UserID, err)
	}
	if !ok || creds.Disabled {
		return SignIn{}, ErrInvalidCredentials
	}

	workspaces, err := s.tenancy.UserWorkspaces(ctx, creds.UserID)
	if err != nil {
		return SignIn{}, err
	}
	if len(workspaces) == 0 {
		return SignIn{}, ErrNoWorkspace
	}
	m, err := s.tenancy.Membership(ctx, workspaces[0].ID, creds.UserID)
	if err != nil {
		return SignIn{}, err
	}
	if err := s.tenancy.TouchLastLogin(ctx, creds.UserID); err != nil {
		return SignIn{}, err
	}
	return s.start(ctx, m, c.UserAgent)
}

// Authenticate resolves a session token and re-checks the membership on
// every call, so a deactivated member, a disabled account or a deleted
// workspace loses access at once. A session whose membership is gone is
// revoked.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, tenancy.Membership, error) {
	sess, err := s.sessions.get(ctx, token)
	if err != nil {
		return Session{}, tenancy.Membership{}, err
	}
	m, err := s.tenancy.Membership(ctx, sess.WorkspaceID, sess.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		if err := s.sessions.delete(ctx, token); err != nil {
			return Session{}, tenancy.Membership{}, err
		}
		return Session{}, tenancy.Membership{}, ErrNoSession
	}
	if err != nil {
		return Session{}, tenancy.Membership{}, err
	}
	return sess, m, nil
}

// SwitchWorkspace moves the user to another workspace they are an active
// member of. The old session is revoked and a new one is issued, because
// the role and permissions change. Not a member: ErrWorkspaceNotFound.
func (s *Service) SwitchWorkspace(ctx context.Context, token string, cur Session, workspaceID uuid.UUID) (SignIn, error) {
	m, err := s.tenancy.Membership(ctx, workspaceID, cur.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return SignIn{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return SignIn{}, err
	}
	in, err := s.start(ctx, m, cur.UserAgent) // the same browser
	if err != nil {
		return SignIn{}, err
	}
	if err := s.sessions.delete(ctx, token); err != nil {
		return SignIn{}, err
	}
	return in, nil
}

// Logout revokes the session. Unknown tokens are not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.sessions.delete(ctx, token)
}

func (s *Service) start(ctx context.Context, m tenancy.Membership, userAgent string) (SignIn, error) {
	token, sess, err := s.sessions.create(ctx, m.User.ID, m.Workspace.ID, userAgent)
	if err != nil {
		return SignIn{}, err
	}
	return SignIn{Token: token, Session: sess, Membership: m}, nil
}

func (s *Service) allow(ctx context.Context, key string, rule ratelimit.Rule) error {
	ok, retry, err := s.limiter.Allow(ctx, key, rule)
	if err != nil {
		return err // fail closed: no limiter, no login
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
