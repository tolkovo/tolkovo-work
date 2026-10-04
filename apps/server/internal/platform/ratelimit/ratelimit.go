// Package ratelimit counts attempts in Redis so that limits hold across all
// api replicas (docs/02 §13.4).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Rule is a fixed-window limit: at most Limit attempts per Window.
type Rule struct {
	Limit  int
	Window time.Duration
}

// Limiter checks rules against counters under one key prefix.
type Limiter struct {
	rdb    *redis.Client
	prefix string
}

// New returns a limiter whose keys start with prefix + "rl:".
func New(rdb *redis.Client, prefix string) *Limiter {
	return &Limiter{rdb: rdb, prefix: prefix + "rl:"}
}

// INCR and PEXPIRE run atomically, so parallel attempts cannot slip past the
// limit and a counter never lives without a TTL.
var hitScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return {n, redis.call('PTTL', KEYS[1])}
`)

// Allow counts one attempt for key under rule. When the limit is exceeded it
// returns false and how long until the window resets. The attempt is counted
// before any work is done, so a burst of parallel requests is limited too.
// key must not contain personal data in clear text: hash emails first.
func (l *Limiter) Allow(ctx context.Context, key string, rule Rule) (bool, time.Duration, error) {
	if rule.Limit <= 0 || rule.Window <= 0 {
		return false, 0, errors.New("ratelimit: invalid rule")
	}
	res, err := hitScript.Run(ctx, l.rdb, []string{l.prefix + key}, rule.Window.Milliseconds()).Int64Slice()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit: %w", err)
	}
	if len(res) != 2 {
		return false, 0, errors.New("ratelimit: unexpected script result")
	}
	if res[0] <= int64(rule.Limit) {
		return true, 0, nil
	}
	retry := time.Duration(res[1]) * time.Millisecond
	if retry <= 0 {
		retry = rule.Window
	}
	return false, retry, nil
}
