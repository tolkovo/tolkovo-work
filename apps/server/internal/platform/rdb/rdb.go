// Package rdb opens the Redis client. Redis holds sessions, rate limits and
// presence; it is never the source of truth for business data (docs/02 §5.3).
package rdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Open parses a redis:// or rediss:// URL, connects and pings.
func Open(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		// The parse error may echo the URL with its password.
		return nil, errors.New("parse redis url: invalid format")
	}
	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}
