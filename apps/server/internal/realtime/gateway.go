// Package realtime is the WebSocket gateway of the operator panel (docs/02
// §10, docs/adr/0005-realtime-transport.md, backlog R-01–R-03).
//
// Domain events are written to the outbox in the business transaction; the
// relay publishes them through Publisher to Redis pub/sub channels
// ws:{workspace}:{topic}; every realtime replica relays them to its local
// connections of that workspace. Redis does not guarantee delivery, so
// Postgres does: each event has a monotonic seq, and a client that
// reconnects sends resume with the last seq it saw and gets the missed
// events from outbox_events (as the application role, RLS applies).
//
// The protocol is api/ws/*.schema.json. Clients authenticate with the
// panel's session cookie; the caller (internal/api) resolves it and passes
// a Client. This package is the WebSocket transport, so unlike business
// modules it works with net/http.
package realtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Errors returned by Serve before the upgrade; the caller answers them.
var (
	// ErrOriginNotAllowed: the handshake did not come from a trusted origin.
	ErrOriginNotAllowed = errors.New("realtime: origin not allowed")
	// ErrUnavailable: shutting down or not subscribed to Redis yet.
	ErrUnavailable = errors.New("realtime: not accepting connections")
	// ErrSessionEnded is what Client.Recheck returns for a dead session.
	ErrSessionEnded = errors.New("realtime: session ended")
)

// Defaults of Options (docs/02 §10.1). Owners tune them in code, not in
// configuration: they are protocol behaviour, not deployment settings.
const (
	DefaultHeartbeatInterval  = 25 * time.Second
	DefaultPingTimeout        = 15 * time.Second
	DefaultWriteTimeout       = 10 * time.Second
	DefaultRecheckInterval    = 30 * time.Second
	DefaultReplayRecheckDelay = 2 * time.Second
	DefaultSendBuffer         = 256
	DefaultMaxConnsPerUser    = 10
	DefaultReplayLimit        = 500
)

// Options configure the gateway; zero values take the defaults above.
type Options struct {
	// ChannelPrefix namespaces the Redis channels; tests use a unique one.
	// Must equal the prefix given to NewPublisher.
	ChannelPrefix string
	// TrustedOrigins may open connections besides the server's own host;
	// the same list as for CSRF (TOLKOVO_PUBLIC_URL + TOLKOVO_TRUSTED_ORIGINS).
	TrustedOrigins []string

	HeartbeatInterval  time.Duration // heartbeat message and ping period
	PingTimeout        time.Duration // no pong within it: the peer is gone
	WriteTimeout       time.Duration // one frame must be written within it
	RecheckInterval    time.Duration // session and role re-check period
	ReplayRecheckDelay time.Duration // second replay pass after resume
	SendBuffer         int           // live events queued per connection
	MaxConnsPerUser    int           // per user on one replica (tabs, devices)
	ReplayLimit        int           // more missed events than this: resync_required

	// Now defaults to time.Now.
	Now func() time.Time
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.HeartbeatInterval, DefaultHeartbeatInterval)
	def(&o.PingTimeout, DefaultPingTimeout)
	def(&o.WriteTimeout, DefaultWriteTimeout)
	def(&o.RecheckInterval, DefaultRecheckInterval)
	def(&o.ReplayRecheckDelay, DefaultReplayRecheckDelay)
	if o.SendBuffer <= 0 {
		o.SendBuffer = DefaultSendBuffer
	}
	if o.MaxConnsPerUser <= 0 {
		o.MaxConnsPerUser = DefaultMaxConnsPerUser
	}
	if o.ReplayLimit <= 0 {
		o.ReplayLimit = DefaultReplayLimit
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// Client is the authenticated caller of a handshake.
type Client struct {
	UserID      uuid.UUID
	WorkspaceID uuid.UUID // from the server-side session, never from the request
	Role        string
	// Recheck re-validates the session every RecheckInterval and returns
	// the current role, or ErrSessionEnded to close the connection with
	// 4401. Other errors (an outage) keep the connection.
	Recheck func(ctx context.Context) (role string, err error)
}

// Gateway accepts WebSocket connections and relays events to them.
type Gateway struct {
	o       Options
	log     *slog.Logger
	hub     *hub
	store   eventStore
	origins map[string]bool

	mu      sync.Mutex // guards closing against wg.Add
	closing bool
	wg      sync.WaitGroup
	active  atomic.Int64
}

// New builds a gateway. Run must be running for it to accept connections.
func New(tdb TenantDB, rdb *redis.Client, log *slog.Logger, o Options) *Gateway {
	o = o.withDefaults()
	origins := make(map[string]bool, len(o.TrustedOrigins))
	for _, v := range o.TrustedOrigins {
		origins[strings.TrimRight(v, "/")] = true
	}
	log = log.With("component", "realtime")
	return &Gateway{
		o: o, log: log, origins: origins,
		hub:   newHub(rdb, o.ChannelPrefix, o.MaxConnsPerUser, log),
		store: eventStore{db: tdb},
	}
}

func (g *Gateway) now() time.Time { return g.o.Now() }

// Run keeps the Redis subscription until ctx is done, then returns nil.
func (g *Gateway) Run(ctx context.Context) error { return g.hub.run(ctx) }

// Ready reports whether the gateway accepts connections.
func (g *Gateway) Ready() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.closing && g.hub.isReady()
}

// Connections is the number of open connections on this replica.
func (g *Gateway) Connections() int64 { return g.active.Load() }

// Serve upgrades an authenticated request and serves the connection until
// it ends. Before the upgrade it returns ErrUnavailable or
// ErrOriginNotAllowed, which the caller turns into an HTTP answer; after it,
// nil. The Origin check is strict: browsers always send Origin on a
// WebSocket handshake, and SameSite=Lax does not stop a cross-site page
// from opening one with the victim's cookie (CSWSH).
func (g *Gateway) Serve(w http.ResponseWriter, r *http.Request, cl Client) error {
	if !g.originAllowed(r) {
		return ErrOriginNotAllowed
	}
	g.mu.Lock()
	if g.closing || !g.hub.isReady() {
		g.mu.Unlock()
		return ErrUnavailable
	}
	g.wg.Add(1)
	g.mu.Unlock()
	defer g.wg.Done()

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Origin is checked above, stricter than the library (scheme too).
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		g.log.Debug("realtime handshake failed", "err", err)
		return nil
	}
	ws.SetReadLimit(MaxClientMessageBytes)
	g.active.Add(1)
	defer g.active.Add(-1)

	// The request context ends with the handler; the connection outlives
	// nothing but its own goroutines, and Shutdown can cancel it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	c := newConn(g, ws, cl, cancel)
	if err := g.hub.register(c); err != nil {
		_ = ws.Close(CloseTooManyConnections, "too many connections")
		return nil
	}
	defer g.hub.unregister(c)
	g.mu.Lock()
	if g.closing {
		c.fail(CloseServiceRestart, "server restart")
	}
	g.mu.Unlock()

	g.serveConn(ctx, cancel, c)
	return nil
}

// serveConn sends hello and runs the connection's goroutines. The hub
// already queues live events into c.live, so nothing published after
// hello's seq is missed; hello is written first, before the writer starts.
func (g *Gateway) serveConn(ctx context.Context, cancel context.CancelFunc, c *conn) {
	seq, err := g.store.latestSeq(ctx, c.workspaceID)
	if err != nil {
		g.log.Warn("realtime: read latest seq", "workspace_id", c.workspaceID, "err", err)
		_ = c.ws.Close(CloseInternalError, "try again")
		return
	}
	c.lastSeq.Store(seq)
	hello := encodeControl(TypeHello, helloData{Seq: seq, HeartbeatIntervalMS: g.o.HeartbeatInterval.Milliseconds()}, g.now())
	if err := c.write(ctx, hello); err != nil {
		_ = c.ws.CloseNow()
		return
	}
	g.log.Debug("realtime connection opened", "workspace_id", c.workspaceID)

	// The writer and the reader end only when the connection is gone, and
	// either ending stops the rest. The other loops end the connection by
	// asking the writer (fail) or by closing it, never by cancelling ctx:
	// a cancelled read would cut the connection before the close frame.
	var wg sync.WaitGroup
	for _, loop := range []struct {
		run  func(context.Context)
		ends bool
	}{{c.writeLoop, true}, {c.readLoop, true}, {c.watchLoop, false}, {c.replayLoop, false}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if loop.ends {
				defer cancel()
			}
			loop.run(ctx)
		}()
	}
	wg.Wait()
	g.log.Debug("realtime connection closed", "workspace_id", c.workspaceID, "code", int(c.closeCode()))
}

// Shutdown stops accepting connections, asks every client to reconnect
// (1012: they come back to another replica with jitter) and waits for the
// connections to finish. When ctx ends first, the rest are cut off.
func (g *Gateway) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.closing = true
	g.mu.Unlock()
	for _, c := range g.hub.all() {
		c.fail(CloseServiceRestart, "server restart")
	}
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		for _, c := range g.hub.all() {
			c.cancel()
		}
		<-done
		return ctx.Err()
	}
}

// originAllowed requires an Origin header from the server's own host or a
// trusted origin, like the CSRF check of the REST API but without the
// pass for requests that have no Origin.
func (g *Gateway) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if g.origins[origin] {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
}
