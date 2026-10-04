// Package rdbtest gives integration tests a Redis client and a unique key
// prefix. Tests share one Redis database, so they never flush it: every key
// a test writes starts with its prefix and is deleted at cleanup.
package rdbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdb"
)

// EnvURL names the variable with the Redis URL for tests. Without it
// integration tests are skipped locally and fail in CI.
const EnvURL = "TOLKOVO_TEST_REDIS_URL"

// New returns a client and a key prefix unique to the test.
func New(t testing.TB) (*redis.Client, string) {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s must be set in CI", EnvURL)
		}
		t.Skipf("integration test: set %s to run it", EnvURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := rdb.Open(ctx, url)
	if err != nil {
		t.Fatalf("open redis: %v", err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	prefix := "tolkovo_t_" + hex.EncodeToString(b) + ":"
	t.Cleanup(func() {
		deletePrefix(t, c, prefix)
		_ = c.Close()
	})
	return c, prefix
}

func deletePrefix(t testing.TB, c *redis.Client, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	iter := c.Scan(ctx, 0, prefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		if err := c.Del(ctx, iter.Val()).Err(); err != nil {
			t.Errorf("delete test key: %v", err)
			return
		}
	}
	if err := iter.Err(); err != nil {
		t.Errorf("scan test keys: %v", err)
	}
}
