package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
)

// clock is a settable time source for expiry tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newStore(t *testing.T, idle, maxAge time.Duration) (*sessionStore, *clock) {
	t.Helper()
	c, prefix := rdbtest.New(t)
	clk := &clock{t: time.Now()}
	return &sessionStore{rdb: c, prefix: prefix, idle: idle, maxAge: maxAge, now: clk.now}, clk
}

func TestSessionStore_CreateGetDelete(t *testing.T) {
	s, _ := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	uid, wid := uuid.New(), uuid.New()

	token, sess, err := s.create(ctx, uid, wid)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(token) < 43 || sess.CSRFToken == "" || sess.CSRFToken == token {
		t.Fatalf("weak tokens: %q %q", token, sess.CSRFToken)
	}
	got, err := s.get(ctx, token)
	if err != nil || got.UserID != uid || got.WorkspaceID != wid || got.CSRFToken != sess.CSRFToken {
		t.Fatalf("get: %+v %v", got, err)
	}
	if err := s.delete(ctx, token); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.get(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after delete: want ErrNoSession, got %v", err)
	}
	if err := s.delete(ctx, token); err != nil {
		t.Fatalf("delete must be idempotent: %v", err)
	}
}

// Redis stores only the SHA-256 of the token: a key listing or a dump does
// not reveal a usable session token.
func TestSessionStore_KeyIsTokenHash(t *testing.T) {
	s, _ := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	token, _, err := s.create(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	keys, err := s.rdb.Keys(ctx, s.prefix+"*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("want 1 key, got %v %v", keys, err)
	}
	sum := sha256.Sum256([]byte(token))
	if keys[0] != s.prefix+"sess:"+hex.EncodeToString(sum[:]) || strings.Contains(keys[0], token) {
		t.Fatalf("key must be the token hash, got %s", keys[0])
	}
	body, _ := s.rdb.Get(ctx, keys[0]).Result()
	if strings.Contains(body, token) {
		t.Fatal("the value must not contain the token")
	}
}

func TestSessionStore_RejectsUnknownAndOversizedTokens(t *testing.T) {
	s, _ := newStore(t, time.Hour, 24*time.Hour)
	for _, token := range []string{"", "nope", strings.Repeat("a", 129)} {
		if _, err := s.get(context.Background(), token); !errors.Is(err, ErrNoSession) {
			t.Errorf("token %q: want ErrNoSession, got %v", token, err)
		}
	}
}

func TestSessionStore_IdleTimeoutSlides(t *testing.T) {
	s, _ := newStore(t, 400*time.Millisecond, time.Hour)
	ctx := context.Background()
	token, _, err := s.create(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Activity keeps the session alive beyond the idle timeout...
	for range 3 {
		time.Sleep(200 * time.Millisecond)
		if _, err := s.get(ctx, token); err != nil {
			t.Fatalf("active session expired: %v", err)
		}
	}
	// ...and inactivity ends it.
	time.Sleep(600 * time.Millisecond)
	if _, err := s.get(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session: want ErrNoSession, got %v", err)
	}
}

func TestSessionStore_AbsoluteExpiry(t *testing.T) {
	s, clk := newStore(t, time.Hour, 2*time.Hour)
	ctx := context.Background()
	token, _, err := s.create(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	clk.add(90 * time.Minute)
	if _, err := s.get(ctx, token); err != nil {
		t.Fatalf("before max age: %v", err)
	}
	// The idle TTL never extends past the absolute expiry.
	ttl, _ := s.rdb.TTL(ctx, s.key(token)).Result()
	if ttl > 31*time.Minute {
		t.Fatalf("idle TTL %s exceeds the remaining lifetime", ttl)
	}
	clk.add(31 * time.Minute)
	if _, err := s.get(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after max age: want ErrNoSession, got %v", err)
	}
	if n, _ := s.rdb.Exists(ctx, s.key(token)).Result(); n != 0 {
		t.Fatal("an expired session must be deleted")
	}
}

func TestSession_CSRFMatches(t *testing.T) {
	s := Session{CSRFToken: "abc"}
	if !s.CSRFMatches("abc") || s.CSRFMatches("abd") || s.CSRFMatches("") || (Session{}).CSRFMatches("") {
		t.Fatal("CSRFMatches is wrong")
	}
}
