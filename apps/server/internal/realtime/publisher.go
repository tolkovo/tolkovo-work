package realtime

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Publisher is the outbox.Publisher of the relay: it fans published events
// out to every realtime replica through Redis pub/sub (ADR-0005). The relay
// calls it after reading the batch and outside any transaction, and marks
// the batch published only when Publish succeeds, so a Redis outage delays
// events instead of losing them.
type Publisher struct {
	rdb    *redis.Client
	prefix string
}

// NewPublisher builds the publisher. prefix must equal Options.ChannelPrefix
// of the gateways; production leaves both empty.
func NewPublisher(rdb *redis.Client, prefix string) *Publisher {
	return &Publisher{rdb: rdb, prefix: prefix}
}

var _ outbox.Publisher = (*Publisher)(nil)

// Publish implements outbox.Publisher. Events without a route (see routes)
// are not sent to clients; widget.* events go to their visitor (visitor.go). One pipeline keeps the batch in seq order.
func (p *Publisher) Publish(ctx context.Context, events []outbox.Event) error {
	pipe := p.rdb.Pipeline()
	queued := 0
	for _, e := range events {
		channel, ok := channelFor(e.Topic)
		encode := encodeEvent
		if !ok {
			channel, ok = widgetRoute(e)
			encode = encodeWidgetEvent
		}
		if !ok {
			continue
		}
		msg, err := encode(e)
		if err != nil {
			return err
		}
		pipe.Publish(ctx, redisChannel(p.prefix, e.WorkspaceID, channel), msg)
		queued++
	}
	if queued == 0 {
		return nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("realtime: publish %d event(s): %w", queued, err)
	}
	return nil
}
