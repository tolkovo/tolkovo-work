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
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Session is a signed-in browser. It lives in Redis under the SHA-256 of
// its token, so a Redis dump or a key listing does not reveal usable tokens.
type Session struct {
	// ID is the public identifier shown in the session list and used to end
	// one session. It is random and tells nothing about the token.
	ID          uuid.UUID `json:"sid"`
	UserID      uuid.UUID `json:"uid"`
	WorkspaceID uuid.UUID `json:"wid"`
	CSRFToken   string    `json:"csrf"`
	CreatedAt   time.Time `json:"cat"`
	// LastSeenAt is refreshed at most every lastSeenEvery, so that
	// authentication does not rewrite the session on every request.
	LastSeenAt time.Time `json:"seen"`
	// UserAgent is the shortened User-Agent at sign-in. IP addresses are not
	// stored: the server does not yet know which proxies to trust.
	UserAgent string `json:"ua,omitempty"`
	// ExpiresAt is the absolute end of the session; activity extends the
	// idle TTL only up to this moment.
	ExpiresAt time.Time `json:"exp"`
}

// CSRFMatches compares a submitted CSRF token in constant time.
func (s Session) CSRFMatches(token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.CSRFToken)) == 1
}

const (
	tokenBytes = 32 // 256 bits from crypto/rand
	// lastSeenEvery bounds how often a request rewrites its session.
	lastSeenEvery = 5 * time.Minute
	// MaxUserAgentLen is how many bytes of the User-Agent a session keeps.
	MaxUserAgentLen = 200
)

// sessionStore keeps sessions in Redis.
//
// Keys: "sess:<sha256(token)>" holds the session JSON with the idle TTL;
// "usess:<user id>" is the user's index, a sorted set of token hashes scored
// by absolute expiry (unix ms). The index expires with its longest-living
// session; entries of sessions that ended early (idle timeout, logout) are
// dropped whenever the index is read or written, so it never grows forever.
type sessionStore struct {
	rdb    *redis.Client
	prefix string
	idle   time.Duration
	maxAge time.Duration
	now    func() time.Time
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// tokenHash names a session in Redis and in the user's index.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *sessionStore) sessionKey(hash string) string { return s.prefix + "sess:" + hash }

func (s *sessionStore) key(token string) string { return s.sessionKey(tokenHash(token)) }

func (s *sessionStore) indexKey(userID uuid.UUID) string {
	return s.prefix + "usess:" + userID.String()
}

// indexAdd adds a session to the user's index, drops entries past their
// absolute expiry and lets the index live as long as its last session. One
// key, so the script also works on Redis Cluster.
var indexAdd = redis.NewScript(`
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[3])
local last = redis.call('ZRANGE', KEYS[1], -1, -1, 'WITHSCORES')
if last[2] then redis.call('PEXPIREAT', KEYS[1], last[2]) end
return 1
`)

// create stores a new session and returns its token. A new session always
// gets a new token, a new CSRF token and a new public ID: login, workspace
// switch and password change never reuse an identifier the client already
// had (no session fixation).
func (s *sessionStore) create(ctx context.Context, userID, workspaceID uuid.UUID, userAgent string) (string, Session, error) {
	token, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	id, err := uuid.NewRandom() // v4: 122 random bits, no timestamp
	if err != nil {
		return "", Session{}, fmt.Errorf("session id: %w", err)
	}
	now := s.now().UTC()
	sess := Session{
		ID: id, UserID: userID, WorkspaceID: workspaceID, CSRFToken: csrf,
		CreatedAt: now, LastSeenAt: now, UserAgent: shortUserAgent(userAgent), ExpiresAt: now.Add(s.maxAge),
	}
	body, err := json.Marshal(sess)
	if err != nil {
		return "", Session{}, fmt.Errorf("encode session: %w", err)
	}
	hash := tokenHash(token)
	// NX: a collision of 256-bit tokens is not expected, but never overwrite.
	ok, err := s.rdb.SetNX(ctx, s.sessionKey(hash), body, min(s.idle, s.maxAge)).Result()
	if err != nil {
		return "", Session{}, fmt.Errorf("store session: %w", err)
	}
	if !ok {
		return "", Session{}, errors.New("store session: token collision")
	}
	// An unindexed session could not be listed or revoked by "sign out
	// everywhere", so a failed index write takes the session back.
	err = indexAdd.Run(ctx, s.rdb, []string{s.indexKey(userID)},
		sess.ExpiresAt.UnixMilli(), hash, now.UnixMilli()).Err()
	if err != nil {
		_ = s.rdb.Del(ctx, s.sessionKey(hash)).Err()
		return "", Session{}, fmt.Errorf("index session: %w", err)
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
	now := s.now()
	left := sess.ExpiresAt.Sub(now)
	// A session from before the per-user index has no ID: it cannot be listed
	// or revoked from another device, so it ends and the user signs in again.
	if left <= 0 || sess.ID == uuid.Nil {
		_ = s.rdb.Del(ctx, key).Err()
		return Session{}, ErrNoSession
	}
	ttl := min(s.idle, left)
	if now.Sub(sess.LastSeenAt) < lastSeenEvery {
		if err := s.rdb.PExpire(ctx, key, ttl).Err(); err != nil {
			return Session{}, fmt.Errorf("extend session: %w", err)
		}
		return sess, nil
	}
	sess.LastSeenAt = now.UTC()
	if body, err = json.Marshal(sess); err != nil {
		return Session{}, fmt.Errorf("encode session: %w", err)
	}
	// XX: a session revoked since the GET above must stay revoked.
	if err := s.rdb.SetXX(ctx, key, body, ttl).Err(); err != nil {
		return Session{}, fmt.Errorf("touch session: %w", err)
	}
	return sess, nil
}

// delete revokes the session of token. Deleting an unknown token is not an
// error: logout must be idempotent.
func (s *sessionStore) delete(ctx context.Context, token string) error {
	if token == "" || len(token) > 128 {
		return nil
	}
	hash := tokenHash(token)
	body, err := s.rdb.GetDel(ctx, s.sessionKey(hash)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(body, &sess); err != nil {
		return fmt.Errorf("decode deleted session: %w", err) // it is gone; the next index read drops the entry
	}
	if err := s.rdb.ZRem(ctx, s.indexKey(sess.UserID), hash).Err(); err != nil {
		return fmt.Errorf("unindex session: %w", err)
	}
	return nil
}

// indexedSession is a live session with its token hash.
type indexedSession struct {
	hash string
	Session
}

// list returns the live sessions of the user and drops index entries whose
// session has ended.
func (s *sessionStore) list(ctx context.Context, userID uuid.UUID) ([]indexedSession, error) {
	idx := s.indexKey(userID)
	now := strconv.FormatInt(s.now().UnixMilli(), 10)
	if err := s.rdb.ZRemRangeByScore(ctx, idx, "-inf", now).Err(); err != nil {
		return nil, fmt.Errorf("prune session index: %w", err)
	}
	hashes, err := s.rdb.ZRange(ctx, idx, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read session index: %w", err)
	}
	if len(hashes) == 0 {
		return nil, nil
	}
	keys := make([]string, len(hashes))
	for i, h := range hashes {
		keys[i] = s.sessionKey(h)
	}
	bodies, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("load sessions: %w", err)
	}
	out := make([]indexedSession, 0, len(hashes))
	var dead []any
	for i, b := range bodies {
		str, ok := b.(string)
		var sess Session
		if !ok || json.Unmarshal([]byte(str), &sess) != nil || sess.UserID != userID || sess.ID == uuid.Nil {
			dead = append(dead, hashes[i])
			continue
		}
		out = append(out, indexedSession{hash: hashes[i], Session: sess})
	}
	if len(dead) > 0 {
		if err := s.rdb.ZRem(ctx, idx, dead...).Err(); err != nil {
			return nil, fmt.Errorf("prune session index: %w", err)
		}
	}
	return out, nil
}

// revoke ends the given sessions of the user.
func (s *sessionStore) revoke(ctx context.Context, userID uuid.UUID, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}
	keys := make([]string, len(hashes))
	members := make([]any, len(hashes))
	for i, h := range hashes {
		keys[i] = s.sessionKey(h)
		members[i] = h
	}
	if err := s.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := s.rdb.ZRem(ctx, s.indexKey(userID), members...).Err(); err != nil {
		return fmt.Errorf("unindex sessions: %w", err)
	}
	return nil
}

// shortUserAgent keeps the User-Agent printable and at most MaxUserAgentLen
// bytes, cut on a character boundary.
func shortUserAgent(ua string) string {
	ua = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, ua))
	if len(ua) <= MaxUserAgentLen {
		return ua
	}
	cut := MaxUserAgentLen
	for cut > 0 && !utf8.RuneStart(ua[cut]) {
		cut--
	}
	return strings.TrimSpace(ua[:cut])
}
