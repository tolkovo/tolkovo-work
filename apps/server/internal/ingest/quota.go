package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels"
)

// The daily quota of an app channel (docs/02 §11.6): reports and declared
// bytes per UTC day, counted in Redis so that every api replica sees the
// same numbers. A report is counted when it is registered, completed or
// not: an app that never completes still spends the quota.

// quotaTTL keeps a day's counters a little past its end.
const quotaTTL = 48 * time.Hour

// reserveScript adds one report and its bytes only if both stay within the
// limits, atomically, so parallel reports cannot overshoot.
var reserveScript = redis.NewScript(`
local n = tonumber(redis.call('GET', KEYS[1]) or '0')
local b = tonumber(redis.call('GET', KEYS[2]) or '0')
if n + 1 > tonumber(ARGV[1]) or b + tonumber(ARGV[3]) > tonumber(ARGV[2]) then return 0 end
redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
redis.call('INCRBY', KEYS[2], ARGV[3])
redis.call('PEXPIRE', KEYS[2], ARGV[4])
return 1
`)

// quotaKeys share a hash tag, so the script works on Redis Cluster too.
func (s *Service) quotaKeys(app channels.IngestApp, day string) []string {
	base := s.opts.KeyPrefix + "ingest:quota:{" + app.ChannelID.String() + "}:" + day
	return []string{base + ":n", base + ":b"}
}

// reserve counts a report of size bytes against today's quota. When the
// quota is used up it returns how long until it resets (UTC midnight).
func (s *Service) reserve(ctx context.Context, app channels.IngestApp, day string, size int64) (time.Duration, error) {
	ok, err := reserveScript.Run(ctx, s.opts.Redis, s.quotaKeys(app, day),
		app.Limits.DailyReports, int64(app.Limits.DailyMB)*mb, size, quotaTTL.Milliseconds()).Int()
	if err != nil {
		return 0, fmt.Errorf("ingest quota: %w", err)
	}
	if ok == 1 {
		return 0, nil
	}
	now := s.now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return midnight.Sub(now), nil
}

// release gives back a reservation whose report was not stored. Best
// effort: a failure leaves the quota slightly overcounted for the day.
func (s *Service) release(ctx context.Context, app channels.IngestApp, day string, size int64) {
	keys := s.quotaKeys(app, day)
	pipe := s.opts.Redis.TxPipeline()
	pipe.Decr(ctx, keys[0])
	pipe.DecrBy(ctx, keys[1], size)
	_, _ = pipe.Exec(ctx)
}
