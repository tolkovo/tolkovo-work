package ratelimit_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/ratelimit"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
)

func TestAllow_BlocksAfterLimitAndResetsAfterWindow(t *testing.T) {
	c, prefix := rdbtest.New(t)
	l := ratelimit.New(c, prefix)
	ctx := context.Background()
	rule := ratelimit.Rule{Limit: 3, Window: 300 * time.Millisecond}

	for i := range 3 {
		if ok, _, err := l.Allow(ctx, "k", rule); err != nil || !ok {
			t.Fatalf("attempt %d: want allowed, got %v %v", i+1, ok, err)
		}
	}
	ok, retry, err := l.Allow(ctx, "k", rule)
	if err != nil || ok || retry <= 0 || retry > rule.Window {
		t.Fatalf("4th attempt: want blocked with retry in (0, window], got %v %v %v", ok, retry, err)
	}
	if ok, _, _ := l.Allow(ctx, "other", rule); !ok {
		t.Fatal("keys must be counted separately")
	}

	time.Sleep(rule.Window + 100*time.Millisecond)
	if ok, _, err := l.Allow(ctx, "k", rule); err != nil || !ok {
		t.Fatalf("after the window: want allowed, got %v %v", ok, err)
	}
}

func TestAllow_ParallelBurstIsLimited(t *testing.T) {
	c, prefix := rdbtest.New(t)
	l := ratelimit.New(c, prefix)
	rule := ratelimit.Rule{Limit: 5, Window: time.Minute}

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			if ok, _, err := l.Allow(context.Background(), "burst", rule); err == nil && ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if n := allowed.Load(); n != 5 {
		t.Fatalf("want exactly 5 allowed, got %d", n)
	}
	ttl, err := c.PTTL(context.Background(), prefix+"rl:burst").Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("counter must have a TTL, got %v (%v)", ttl, err)
	}
}

func TestHit_ReportsRemainingAndReset(t *testing.T) {
	c, prefix := rdbtest.New(t)
	l := ratelimit.New(c, prefix)
	rule := ratelimit.Rule{Limit: 2, Window: time.Minute}
	for i, want := range []ratelimit.Result{
		{Allowed: true, Limit: 2, Remaining: 1},
		{Allowed: true, Limit: 2, Remaining: 0},
		{Allowed: false, Limit: 2, Remaining: 0},
	} {
		got, err := l.Hit(context.Background(), "h", rule)
		if err != nil || got.Allowed != want.Allowed || got.Limit != want.Limit || got.Remaining != want.Remaining ||
			got.Reset <= 0 || got.Reset > rule.Window {
			t.Fatalf("attempt %d: want %+v, got %+v (%v)", i+1, want, got, err)
		}
	}
}
