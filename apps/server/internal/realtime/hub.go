package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// errTooManyConnections is returned by register over the per-user limit.
var errTooManyConnections = errors.New("realtime: too many connections for this user")

// subscriber is a local connection the hub fans events out to: an
// operator's panel (conn) or a widget visitor (visitorConn).
type subscriber interface {
	// workspace is the tenant whose events the connection gets.
	workspace() uuid.UUID
	// owner is whose connections the per-owner limit counts: the user, or
	// the visitor's session.
	owner() uuid.UUID
	// deliverLive queues an event; it must not block.
	deliverLive(channel string, msg []byte, seq int64)
	// requestResync tells the connection events may have been missed.
	requestResync()
	fail(code websocket.StatusCode, reason string)
	// cancelNow cuts the connection off without a close handshake.
	cancelNow()
}

// hub is the local index of one replica: which connections belong to which
// workspace. It subscribes once to every ws:* channel and hands each event
// to the local connections of its workspace. No sticky sessions: any
// replica can serve any client.
//
// One pattern subscription per replica is the simplest correct choice for
// phase 1; subscribing per workspace with local connections (docs/02 §10.5)
// is an optimisation for when the k6 test (X-02) asks for it.
type hub struct {
	rdb    *redis.Client
	prefix string
	log    *slog.Logger
	// maxPerUser caps connections of one user on this replica.
	maxPerUser int

	mu      sync.RWMutex
	conns   map[uuid.UUID]map[subscriber]struct{} // by workspace
	perUser map[uuid.UUID]int

	ready     chan struct{}
	readyOnce sync.Once
}

func newHub(rdb *redis.Client, prefix string, maxPerUser int, log *slog.Logger) *hub {
	return &hub{
		rdb: rdb, prefix: prefix, log: log, maxPerUser: maxPerUser,
		conns: map[uuid.UUID]map[subscriber]struct{}{}, perUser: map[uuid.UUID]int{},
		ready: make(chan struct{}),
	}
}

// isReady reports whether the Redis subscription is up; until then the
// gateway refuses connections, which would otherwise miss events.
func (h *hub) isReady() bool {
	select {
	case <-h.ready:
		return true
	default:
		return false
	}
}

// run subscribes and dispatches until ctx is done. A failed first
// subscription is retried; later outages are handled by go-redis, which
// reconnects and resubscribes on its own.
func (h *hub) run(ctx context.Context) error {
	for {
		err := h.subscribe(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is the normal way to stop
		}
		h.log.Warn("realtime subscription failed, retrying", "err", err)
		t := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

func (h *hub) subscribe(ctx context.Context) error {
	ps := h.rdb.PSubscribe(ctx, h.prefix+"ws:*")
	defer func() { _ = ps.Close() }()
	// Wait for the confirmation: events published before it would be lost.
	if _, err := ps.Receive(ctx); err != nil {
		return fmt.Errorf("psubscribe: %w", err)
	}
	h.readyOnce.Do(func() { close(h.ready) })
	h.log.Info("realtime subscribed to fan-out channels")

	ch := ps.ChannelWithSubscriptions(redis.WithChannelSize(1024))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case m, ok := <-ch:
			if !ok {
				return errors.New("subscription channel closed")
			}
			switch v := m.(type) {
			case *redis.Message:
				h.dispatch(v.Channel, []byte(v.Payload))
			case *redis.Subscription:
				// Only a reconnect produces another confirmation. Events
				// published while the link was down never arrived here:
				// every connection replays from its last seq.
				if v.Kind == "psubscribe" {
					h.log.Warn("realtime resubscribed after a Redis reconnect, replaying from Postgres")
					h.resyncAll()
				}
			}
		}
	}
}

// dispatch hands one fan-out message to the local connections of its
// workspace that may see its channel. It never blocks: a connection that
// cannot keep up is closed as a slow consumer.
func (h *hub) dispatch(channelName string, msg []byte) {
	ws, channel, ok := parseChannel(h.prefix, channelName)
	if !ok {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	targets := h.conns[ws]
	if len(targets) == 0 {
		return
	}
	seq := eventSeq(msg)
	for c := range targets {
		c.deliverLive(channel, msg, seq)
	}
}

func (h *hub) resyncAll() {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, set := range h.conns {
		for c := range set {
			c.requestResync()
		}
	}
}

func (h *hub) register(c subscriber) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perUser[c.owner()] >= h.maxPerUser {
		return errTooManyConnections
	}
	h.perUser[c.owner()]++
	set := h.conns[c.workspace()]
	if set == nil {
		set = map[subscriber]struct{}{}
		h.conns[c.workspace()] = set
	}
	set[c] = struct{}{}
	return nil
}

func (h *hub) unregister(c subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.conns[c.workspace()]
	if _, ok := set[c]; !ok {
		return
	}
	delete(set, c)
	if len(set) == 0 {
		delete(h.conns, c.workspace())
	}
	if h.perUser[c.owner()]--; h.perUser[c.owner()] <= 0 {
		delete(h.perUser, c.owner())
	}
}

// all returns a snapshot of local connections, for shutdown.
func (h *hub) all() []subscriber {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []subscriber
	for _, set := range h.conns {
		for c := range set {
			out = append(out, c)
		}
	}
	return out
}
