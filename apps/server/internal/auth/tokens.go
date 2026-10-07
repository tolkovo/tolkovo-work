package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// Personal access tokens (ADR-0016): a member works with tasks from a
// script or an AI agent (MCP) on their own behalf. The token is
// tk_pat_ + base64url(32 random bytes ‖ workspace id): the workspace it
// names is where its hash is looked up, so a forged one finds nothing.

const (
	accessTokenPrefix = "tk_pat_"
	// tokenShownChars of the token after its prefix stay visible in lists.
	tokenShownChars = 6
	// tokenTouchEvery limits writes of last_used_at.
	tokenTouchEvery = time.Minute
)

// TokenExpiryDays are the lifetimes a token may be issued with; nil
// ExpiresInDays is a token without an end.
var TokenExpiryDays = []int{30, 90, 365}

// ErrInvalidAccessToken covers every token that does not let its holder
// in: malformed, unknown, expired, revoked, or of a member who left.
var ErrInvalidAccessToken = errors.New("auth: invalid access token")

var (
	// What a token may do in a minute: plenty for an agent, too little to
	// flood the server. Counted before the token is checked, per address,
	// and after it, per token.
	tokenRequestsPerIP    = ratelimit.Rule{Limit: 1200, Window: time.Minute}
	tokenRequestsPerToken = ratelimit.Rule{Limit: 600, Window: time.Minute}
	tokensIssuedPerUser   = ratelimit.Rule{Limit: 30, Window: time.Hour}
)

// IssueTokenInput is a new token of the caller.
type IssueTokenInput struct {
	Name          string
	Scopes        []string
	ExpiresInDays *int
}

// IssuedToken is a new token with its secret, shown to the member once.
type IssuedToken struct {
	Token  tenancy.AccessToken
	Secret crypto.Secret
}

// IssueAccessToken makes a token for an active member of the workspace.
// tasks:write includes tasks:read.
func (s *Service) IssueAccessToken(ctx context.Context, workspaceID, userID uuid.UUID, in IssueTokenInput) (IssuedToken, error) {
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); !utf8.ValidString(name) || n < 1 || n > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return IssuedToken{}, fmt.Errorf("%w: the name must be 1 to 80 characters", ErrInvalid)
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return IssuedToken{}, err
	}
	now := s.clock()
	var expires *time.Time
	if in.ExpiresInDays != nil {
		if !slices.Contains(TokenExpiryDays, *in.ExpiresInDays) {
			return IssuedToken{}, fmt.Errorf("%w: expires_in_days must be one of %v or null", ErrInvalid, TokenExpiryDays)
		}
		t := now.Add(time.Duration(*in.ExpiresInDays) * 24 * time.Hour)
		expires = &t
	}
	if err := s.allow(ctx, "pat:issue:"+userID.String(), tokensIssuedPerUser); err != nil {
		return IssuedToken{}, err
	}
	secret, err := newAccessToken(workspaceID)
	if err != nil {
		return IssuedToken{}, err
	}
	hash := sha256.Sum256([]byte(secret.Reveal()))
	tok, err := s.tenancy.AddAccessToken(ctx, workspaceID, tenancy.NewAccessToken{
		UserID: userID, Name: name, Scopes: scopes, Hash: hash[:],
		Prefix: secret.Reveal()[:len(accessTokenPrefix)+tokenShownChars], CreatedAt: now, ExpiresAt: expires,
	})
	if err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{Token: tok, Secret: secret}, nil
}

// AccessTokens lists live tokens: of one member when userID is set, of the
// whole workspace otherwise.
func (s *Service) AccessTokens(ctx context.Context, workspaceID uuid.UUID, userID *uuid.UUID) ([]tenancy.AccessToken, error) {
	return s.tenancy.AccessTokens(ctx, workspaceID, userID, s.clock())
}

// RevokeAccessToken deletes a token: one of userID's when it is set, any
// token of the workspace otherwise. It stops working at once.
func (s *Service) RevokeAccessToken(ctx context.Context, workspaceID, tokenID uuid.UUID, userID *uuid.UUID) error {
	return s.tenancy.DeleteAccessToken(ctx, workspaceID, tokenID, userID)
}

// TokenAuth is a checked token: its member, as for a session, and the
// permissions its scopes grant.
type TokenAuth struct {
	TokenID    uuid.UUID
	Membership tenancy.Membership
	Scopes     []authz.Permission
}

// AuthenticateToken checks a token on every request, like Authenticate
// does a session: the membership is read again, so a member who left or a
// disabled account loses access at once, and the role is the current one.
func (s *Service) AuthenticateToken(ctx context.Context, token string, c Client) (TokenAuth, error) {
	if err := s.allow(ctx, "pat:ip:"+digest(c.IP), tokenRequestsPerIP); err != nil {
		return TokenAuth{}, err
	}
	workspaceID, ok := parseAccessToken(token)
	if !ok {
		return TokenAuth{}, ErrInvalidAccessToken
	}
	hash := sha256.Sum256([]byte(token))
	grant, err := s.tenancy.AccessTokenGrant(ctx, workspaceID, hash[:], s.clock(), tokenTouchEvery)
	if errors.Is(err, tenancy.ErrNotFound) {
		return TokenAuth{}, ErrInvalidAccessToken
	}
	if err != nil {
		return TokenAuth{}, err
	}
	if err := s.allow(ctx, "pat:tok:"+grant.ID.String(), tokenRequestsPerToken); err != nil {
		return TokenAuth{}, err
	}
	perms, ok := authz.ScopePermissions(grant.Scopes)
	if !ok {
		return TokenAuth{}, ErrInvalidAccessToken
	}
	m, err := s.tenancy.Membership(ctx, workspaceID, grant.UserID)
	if errors.Is(err, tenancy.ErrNotFound) {
		return TokenAuth{}, ErrInvalidAccessToken
	}
	if err != nil {
		return TokenAuth{}, err
	}
	return TokenAuth{TokenID: grant.ID, Membership: m, Scopes: perms}, nil
}

// IsAccessToken reports whether a bearer credential claims to be a
// personal access token, so the api layer can tell it from other tokens.
func IsAccessToken(token string) bool { return strings.HasPrefix(token, accessTokenPrefix) }

// normalizeScopes checks the scopes and returns them sorted, without
// repeats, with tasks:read added to tasks:write.
func normalizeScopes(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > 8 {
		return nil, fmt.Errorf("%w: give one scope or more", ErrInvalid)
	}
	out := make([]string, 0, len(in)+1)
	for _, sc := range in {
		if _, ok := authz.ScopePermissions([]string{sc}); !ok {
			return nil, fmt.Errorf("%w: unknown scope %q", ErrInvalid, sc)
		}
		out = append(out, sc)
	}
	if slices.Contains(out, authz.ScopeTasksWrite) {
		out = append(out, authz.ScopeTasksRead)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// parseAccessToken checks the format and returns the workspace the token names.
func parseAccessToken(token string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(token, accessTokenPrefix)
	if !ok {
		return uuid.Nil, false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(rest)
	if err != nil || len(b) != 48 {
		return uuid.Nil, false
	}
	ws, err := uuid.FromBytes(b[32:])
	if err != nil || ws == uuid.Nil {
		return uuid.Nil, false
	}
	return ws, true
}

func newAccessToken(workspaceID uuid.UUID) (crypto.Secret, error) {
	b := make([]byte, 32, 48)
	if _, err := rand.Read(b); err != nil {
		return crypto.Secret{}, fmt.Errorf("random: %w", err)
	}
	b = append(b, workspaceID[:]...)
	return crypto.NewSecret(accessTokenPrefix + base64.RawURLEncoding.EncodeToString(b)), nil
}
