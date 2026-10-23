package auth

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionIndex_ListsOnlyTheUsersLiveSessions(t *testing.T) {
	s, _ := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	alice, bob, ws := uuid.New(), uuid.New(), uuid.New()

	t1, s1, _ := s.create(ctx, alice, ws, "Firefox")
	_, s2, _ := s.create(ctx, alice, uuid.New(), "Safari") // another workspace, same user
	t3, s3, _ := s.create(ctx, alice, ws, "")
	if _, _, err := s.create(ctx, bob, ws, "Chrome"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if s1.ID == uuid.Nil || s1.ID == s2.ID || s1.ID.Version() != 4 {
		t.Fatalf("public ids must be distinct random UUIDs: %s %s", s1.ID, s2.ID)
	}

	got := ids(t, s, alice)
	if !slices.Equal(got, sortIDs(s1.ID, s2.ID, s3.ID)) {
		t.Fatalf("alice's sessions: %v", got)
	}
	// Logout removes the session from the index as well.
	if err := s.delete(ctx, t1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := ids(t, s, alice); !slices.Equal(got, sortIDs(s2.ID, s3.ID)) {
		t.Fatalf("after logout: %v", got)
	}
	if n := s.rdb.ZCard(ctx, s.indexKey(alice)).Val(); n != 2 {
		t.Fatalf("index size after logout: %d", n)
	}
	// revoke ends the session itself, not only the index entry.
	if err := s.revoke(ctx, alice, []string{tokenHash(t3)}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.get(ctx, t3); !errors.Is(err, ErrNoSession) {
		t.Fatalf("revoked session still works: %v", err)
	}
	if got := ids(t, s, alice); !slices.Equal(got, []uuid.UUID{s2.ID}) {
		t.Fatalf("after revoke: %v", got)
	}
	if len(ids(t, s, bob)) != 1 {
		t.Fatal("bob's session must be untouched")
	}
}

// A session that ended without logout (idle timeout) leaves an index entry
// behind; reading the index drops it, so the index does not grow forever.
func TestSessionIndex_DropsEndedSessionsOnRead(t *testing.T) {
	s, clk := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	uid := uuid.New()
	idle, _, _ := s.create(ctx, uid, uuid.New(), "")
	_, _, _ = s.create(ctx, uid, uuid.New(), "")
	s.rdb.Del(ctx, s.key(idle)) // what the idle TTL does

	if got := ids(t, s, uid); len(got) != 1 {
		t.Fatalf("want 1 live session, got %v", got)
	}
	if n := s.rdb.ZCard(ctx, s.indexKey(uid)).Val(); n != 1 {
		t.Fatalf("the dead entry must be dropped, index size %d", n)
	}
	// Past the absolute expiry every entry goes, even if the key lingers.
	clk.add(25 * time.Hour)
	if got := ids(t, s, uid); len(got) != 0 {
		t.Fatalf("expired sessions listed: %v", got)
	}
	if n := s.rdb.Exists(ctx, s.indexKey(uid)).Val(); n != 0 {
		t.Fatal("an empty index must not remain")
	}
}

// The index lives exactly as long as its longest-living session.
func TestSessionIndex_ExpiresWithLastSession(t *testing.T) {
	s, clk := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	uid := uuid.New()
	_, _, _ = s.create(ctx, uid, uuid.New(), "")
	// The deadline is absolute (PEXPIREAT) from this clock and PTTL counts on
	// the Redis clock, which may lag a few milliseconds (Redis in a VM).
	if ttl := s.rdb.PTTL(ctx, s.indexKey(uid)).Val(); ttl <= 23*time.Hour || ttl > 24*time.Hour+time.Second {
		t.Fatalf("index TTL %s, want ~24h", ttl)
	}
	clk.add(2 * time.Hour)
	_, _, _ = s.create(ctx, uid, uuid.New(), "")
	if ttl := s.rdb.PTTL(ctx, s.indexKey(uid)).Val(); ttl <= 25*time.Hour {
		t.Fatalf("index TTL %s must follow the newest session", ttl)
	}
}

func TestSessionStore_LastSeenIsThrottled(t *testing.T) {
	s, clk := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	token, created, _ := s.create(ctx, uuid.New(), uuid.New(), "")
	body := s.rdb.Get(ctx, s.key(token)).Val()

	clk.add(4 * time.Minute)
	got, err := s.get(ctx, token)
	if err != nil || !got.LastSeenAt.Equal(created.LastSeenAt) {
		t.Fatalf("last seen must not move within %s: %v %v", lastSeenEvery, got.LastSeenAt, err)
	}
	if s.rdb.Get(ctx, s.key(token)).Val() != body {
		t.Fatal("a request within the throttle must not rewrite the session")
	}

	clk.add(2 * time.Minute)
	if got, err = s.get(ctx, token); err != nil || !got.LastSeenAt.Equal(clk.now().UTC()) {
		t.Fatalf("last seen: %v %v", got.LastSeenAt, err)
	}
	again, _ := s.get(ctx, token)
	if !again.LastSeenAt.Equal(got.LastSeenAt) || again.CSRFToken != created.CSRFToken || again.ID != created.ID {
		t.Fatalf("stored session: %+v", again)
	}
	if ttl := s.rdb.PTTL(ctx, s.key(token)).Val(); ttl <= 59*time.Minute {
		t.Fatalf("the rewrite must keep the idle TTL, got %s", ttl)
	}
}

// Sessions stored before the index existed have no public ID: they cannot
// be listed or revoked remotely, so they end.
func TestSessionStore_RejectsSessionWithoutID(t *testing.T) {
	s, clk := newStore(t, time.Hour, 24*time.Hour)
	ctx := context.Background()
	legacy, _ := json.Marshal(map[string]any{
		"uid": uuid.New(), "wid": uuid.New(), "csrf": "x", "cat": clk.now(), "exp": clk.now().Add(time.Hour),
	})
	token, _ := newToken()
	s.rdb.Set(ctx, s.key(token), legacy, time.Hour)
	if _, err := s.get(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
	if s.rdb.Exists(ctx, s.key(token)).Val() != 0 {
		t.Fatal("the legacy session must be deleted")
	}
}

func TestShortUserAgent(t *testing.T) {
	long := strings.Repeat("a", 199) + "яя"
	tests := map[string]string{
		"":                       "",
		"  Mozilla/5.0  ":        "Mozilla/5.0",
		"Mozilla\x00/5.0\n\tX":   "Mozilla/5.0X",
		strings.Repeat("b", 500): strings.Repeat("b", MaxUserAgentLen),
		long:                     strings.Repeat("a", 199), // never cuts a character in half
	}
	for in, want := range tests {
		if got := shortUserAgent(in); got != want {
			t.Errorf("shortUserAgent(%q) = %q, want %q", in, got, want)
		}
	}
}

func ids(t *testing.T, s *sessionStore, uid uuid.UUID) []uuid.UUID {
	t.Helper()
	list, err := s.list(context.Background(), uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := make([]uuid.UUID, 0, len(list))
	for _, is := range list {
		out = append(out, is.ID)
	}
	return sortIDs(out...)
}

func sortIDs(ids ...uuid.UUID) []uuid.UUID {
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return ids
}
