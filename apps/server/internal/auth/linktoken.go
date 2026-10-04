package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Emailed links (invitation, password reset, email confirmation) carry a
// token of 16 bytes of an owner ID (the workspace of an invitation, the user
// otherwise) and 32 random bytes, base64url without padding. The owner ID
// tells where to look the token up; only the SHA-256 of the whole token is
// stored. Links put the token in the URL fragment, which browsers send
// neither to servers nor in Referer.

const (
	linkTokenIDLen     = 16
	linkTokenSecretLen = 32
)

// newLinkToken returns a token for owner and its SHA-256.
func newLinkToken(owner uuid.UUID) (string, []byte, error) {
	raw := make([]byte, linkTokenIDLen+linkTokenSecretLen)
	copy(raw, owner[:])
	if _, err := rand.Read(raw[linkTokenIDLen:]); err != nil {
		return "", nil, fmt.Errorf("auth: generate link token: %w", err)
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// parseLinkToken returns the owner ID and the SHA-256 of a token, or
// ErrInvalidToken when it is malformed.
func parseLinkToken(token string) (uuid.UUID, []byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != linkTokenIDLen+linkTokenSecretLen {
		return uuid.Nil, nil, ErrInvalidToken
	}
	owner, err := uuid.FromBytes(raw[:linkTokenIDLen])
	if err != nil || owner == uuid.Nil {
		return uuid.Nil, nil, ErrInvalidToken
	}
	sum := sha256.Sum256(raw)
	return owner, sum[:], nil
}

// userTokens keeps one live single-use token per user and purpose in Redis:
// <prefix><purpose>:<user_id> holds the hex SHA-256 with the link's TTL.
// Issuing a new token replaces the previous one.
type userTokens struct {
	rdb     *redis.Client
	prefix  string
	purpose string
	ttl     time.Duration
}

func (t userTokens) key(userID uuid.UUID) string {
	return t.prefix + t.purpose + ":" + userID.String()
}

// issue stores a new token for the user and returns it.
func (t userTokens) issue(ctx context.Context, userID uuid.UUID) (string, error) {
	token, hash, err := newLinkToken(userID)
	if err != nil {
		return "", err
	}
	if err := t.rdb.Set(ctx, t.key(userID), hex.EncodeToString(hash), t.ttl).Err(); err != nil {
		return "", fmt.Errorf("auth: store %s token: %w", t.purpose, err)
	}
	return token, nil
}

// compareAndDelete deletes the key only if it still holds this hash, so of
// two parallel uses one wins and a token issued meanwhile survives. The
// compared values are hashes of secrets: comparing them in Lua leaks nothing
// a caller can use.
var compareAndDelete = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0`)

// consume checks the token and deletes it atomically.
func (t userTokens) consume(ctx context.Context, token string) (uuid.UUID, error) {
	userID, hash, err := parseLinkToken(token)
	if err != nil {
		return uuid.Nil, err
	}
	n, err := compareAndDelete.Run(ctx, t.rdb, []string{t.key(userID)}, hex.EncodeToString(hash)).Int()
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth: use %s token: %w", t.purpose, err)
	}
	if n == 0 {
		return uuid.Nil, ErrInvalidToken
	}
	return userID, nil
}
