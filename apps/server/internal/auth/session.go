package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Session is a signed-in browser. It lives in Redis under the SHA-256 of
// its token, so a Redis dump or a key listing does not reveal usable tokens.
type Session struct {
	UserID      uuid.UUID `json:"uid"`
	WorkspaceID uuid.UUID `json:"wid"`
	CSRFToken   string    `json:"csrf"`
	CreatedAt   time.Time `json:"cat"`
	// ExpiresAt is the absolute end of the session; activity extends the
	// idle TTL only up to this moment.
	ExpiresAt time.Time `json:"exp"`
}

// CSRFMatches compares a submitted CSRF token in constant time.
func (s Session) CSRFMatches(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) == 1
}

// sessionStore keeps sessions in Redis.
type sessionStore struct {
	rdb    *redis.Client
	prefix string
	idle   time.Duration
	maxAge time.Duration
	now    func() time.Time
}

const tokenBytes = 32 // 256 bits from crypto/rand

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *sessionStore) key(token string) string {
	sum := sha256.Sum256([]byte(token))
	return s.prefix + "sess:" + hex.EncodeToString(sum[:])
}

// create stores a new session and returns its token. A new session always
// gets a new token and a new CSRF token: login and workspace switch never
// reuse an identifier the client already had (no session fixation).
func (s *sessionStore) create(ctx context.Context, userID, workspaceID uuid.UUID) (string, Session, error) {
	token, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	now := s.now().UTC()
	sess := Session{
		UserID: userID, WorkspaceID: workspaceID, CSRFToken: csrf,
		CreatedAt: now, ExpiresAt: now.Add(s.maxAge),
	}
	body, err := json.Marshal(sess)
	if err != nil {
		return "", Session{}, fmt.Errorf("encode session: %w", err)
	}
	// NX: a collision of 256-bit tokens is not expected, but never overwrite.
	ok, err := s.rdb.SetNX(ctx, s.key(token), body, min(s.idle, s.maxAge)).Result()
	if err != nil {
		return "", Session{}, fmt.Errorf("store session: %w", err)
	}
	if !ok {
		return "", Session{}, errors.New("store session: token collision")
	}
	return token, sess, nil
}

// get returns the session of token and extends its idle TTL, capped by the
// absolute expiry. Unknown, malformed and expired tokens give ErrNoSession.
func (s *sessionStore) get(ctx context.Context, token string) (Session, error) {
	if token == "" || len(token) > 128 {
		return Session{}, ErrNoSession
	}
	key := s.key(token)
	body, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("load session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(body, &sess); err != nil {
		return Session{}, fmt.Errorf("decode session: %w", err)
	}
	left := sess.ExpiresAt.Sub(s.now())
	if left <= 0 {
		_ = s.rdb.Del(ctx, key).Err()
		return Session{}, ErrNoSession
	}
	if err := s.rdb.PExpire(ctx, key, min(s.idle, left)).Err(); err != nil {
		return Session{}, fmt.Errorf("extend session: %w", err)
	}
	return sess, nil
}

// delete revokes the session of token. Deleting an unknown token is not an
// error: logout must be idempotent.
func (s *sessionStore) delete(ctx context.Context, token string) error {
	if token == "" || len(token) > 128 {
		return nil
	}
	if err := s.rdb.Del(ctx, s.key(token)).Err(); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
