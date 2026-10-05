package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// The widget's realtime connection (W-01, api/ws/widget-message.schema.json).
// A visitor gets only the events of their own chat: widget.* outbox events
// are published to the channel visitor:{contact} of the workspace, and a
// visitor connection takes nothing else. There is no seq and no replay:
// the widget refetches its chat over REST after hello, resync_required or
// a reconnect, so a missed event costs a moment, never a message. The
// widget sends nothing over the connection.

// WidgetSubprotocol is the subprotocol a widget asks for, followed by its
// session token: browsers cannot set headers on a WebSocket handshake.
const WidgetSubprotocol = "tolkovo.widget.v1"

// Widget message types.
const (
	TypeWidgetMessage = "widget.message"
	TypeWidgetTyping  = "widget.typing"
)

// widgetTopicPrefix marks the outbox events that go to visitors.
const widgetTopicPrefix = "widget."

// visitorChannel is {topic} of a visitor's fan-out channel.
func visitorChannel(contactID uuid.UUID) string { return "visitor:" + contactID.String() }

// Visitor is the authenticated widget session of a handshake; internal/api
// checks the token and the page's origin before passing it.
type Visitor struct {
	WorkspaceID uuid.UUID
	SessionID   uuid.UUID
	// ContactID is uuid.Nil until the visitor's first message.
	ContactID uuid.UUID
	// Recheck re-validates the session every RecheckInterval and returns
	// its contact, which the first message or an identify may change.
	// ErrSessionEnded closes the connection with 4401; other errors (an
	// outage) keep it.
	Recheck func(ctx context.Context) (contactID uuid.UUID, err error)
}

// ServeVisitor upgrades a widget's request and serves it until it ends.
// Before the upgrade it returns ErrUnavailable; after it, nil.
func (g *Gateway) ServeVisitor(w http.ResponseWriter, r *http.Request, v Visitor) error {
	g.mu.Lock()
	if g.closing || !g.hub.isReady() {
		g.mu.Unlock()
		return ErrUnavailable
	}
	g.wg.Add(1)
	g.mu.Unlock()
	defer g.wg.Done()

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{WidgetSubprotocol},
		// The caller checked the Origin against the widget's own list.
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		g.log.Debug("realtime widget handshake failed", "err", err)
		return nil
	}
	if ws.Subprotocol() != WidgetSubprotocol {
		_ = ws.Close(CloseProtocolViolation, "subprotocol "+WidgetSubprotocol+" required")
		return nil
	}
	ws.SetReadLimit(MaxClientMessageBytes)
	g.active.Add(1)
	defer g.active.Add(-1)

	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	c := &visitorConn{
		g: g, ws: ws, workspaceID: v.WorkspaceID, sessionID: v.SessionID, recheck: v.Recheck, cancel: cancel,
		live: make(chan []byte, g.o.SendBuffer), kill: make(chan struct{}),
	}
	c.contact.Store(&v.ContactID)
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

	hello := encodeControl(TypeHello, map[string]int64{"heartbeat_interval_ms": g.o.HeartbeatInterval.Milliseconds()}, g.now())
	if err := c.write(ctx, hello); err != nil {
		_ = ws.CloseNow()
		return nil
	}
	var wg sync.WaitGroup
	for _, loop := range []struct {
		run  func(context.Context)
		ends bool
	}{{c.writeLoop, true}, {c.readLoop, true}, {c.watchLoop, false}} {
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
	return nil
}

// visitorConn is one widget connection: a writer, a reader that only waits
// for the close, and a watcher for heartbeats and session re-checks.
type visitorConn struct {
	g           *Gateway
	ws          *websocket.Conn
	workspaceID uuid.UUID
	sessionID   uuid.UUID
	recheck     func(context.Context) (uuid.UUID, error)
	cancel      context.CancelFunc
	contact     atomic.Pointer[uuid.UUID]

	live       chan []byte
	kill       chan struct{}
	killOnce   sync.Once
	killCode   websocket.StatusCode
	killReason string
}

func (c *visitorConn) workspace() uuid.UUID { return c.workspaceID }
func (c *visitorConn) owner() uuid.UUID     { return c.sessionID }
func (c *visitorConn) cancelNow()           { c.cancel() }

func (c *visitorConn) fail(code websocket.StatusCode, reason string) {
	c.killOnce.Do(func() {
		c.killCode, c.killReason = code, reason
		close(c.kill)
	})
}

// deliverLive queues an event of the visitor's own channel. Called by the
// hub under its read lock, so it must not block.
func (c *visitorConn) deliverLive(channel string, msg []byte, _ int64) {
	contact := *c.contact.Load()
	if contact == uuid.Nil || channel != visitorChannel(contact) {
		return
	}
	c.queue(msg)
}

// requestResync asks the widget to refetch: the hub lost events.
func (c *visitorConn) requestResync() {
	c.queue(encodeControl(TypeResyncRequired, struct{}{}, c.g.now()))
}

func (c *visitorConn) queue(msg []byte) {
	select {
	case c.live <- msg:
	default:
		c.fail(CloseSlowConsumer, "slow consumer")
	}
}

func (c *visitorConn) write(ctx context.Context, msg []byte) error {
	wctx, cancel := context.WithTimeout(ctx, c.g.o.WriteTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, msg)
}

func (c *visitorConn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-c.kill:
			_ = c.ws.Close(c.killCode, c.killReason)
			return
		case <-ctx.Done():
			_ = c.ws.CloseNow()
			return
		case msg := <-c.live:
			if c.write(ctx, msg) != nil {
				_ = c.ws.CloseNow()
				return
			}
		}
	}
}

// readLoop waits for the connection to end. The widget sends nothing: any
// frame is a protocol violation.
func (c *visitorConn) readLoop(ctx context.Context) {
	for {
		if _, _, err := c.ws.Read(ctx); err != nil {
			return
		}
		c.fail(CloseProtocolViolation, "the widget sends nothing over this connection")
	}
}

func (c *visitorConn) watchLoop(ctx context.Context) {
	hb := time.NewTicker(c.g.o.HeartbeatInterval)
	defer hb.Stop()
	rc := time.NewTicker(c.g.o.RecheckInterval)
	defer rc.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.kill:
			return
		case <-hb.C:
			c.queue(encodeControl(TypeHeartbeat, struct{}{}, c.g.now()))
			pctx, cancel := context.WithTimeout(ctx, c.g.o.PingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					_ = c.ws.CloseNow()
				}
				return
			}
		case <-rc.C:
			if !c.recheckSession(ctx) {
				return
			}
		}
	}
}

// recheckSession closes the connection of an ended session and follows a
// change of its contact; then the widget refetches, as events of the new
// contact may have come in between.
func (c *visitorConn) recheckSession(ctx context.Context) bool {
	if c.recheck == nil {
		return true
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	contact, err := c.recheck(rctx)
	cancel()
	switch {
	case errors.Is(err, ErrSessionEnded):
		c.fail(CloseSessionEnded, "session ended")
		return false
	case err != nil:
		if ctx.Err() == nil {
			c.g.log.Warn("realtime widget session re-check failed", "workspace_id", c.workspaceID, "err", err)
		}
		return true
	}
	if prev := c.contact.Swap(&contact); *prev != contact {
		c.requestResync()
	}
	return true
}

// widgetRoute returns the fan-out channel of a widget event: the visitor
// named by its contact_id.
func widgetRoute(e outbox.Event) (string, bool) {
	if !strings.HasPrefix(e.Topic, widgetTopicPrefix) {
		return "", false
	}
	var p struct {
		ContactID string `json:"contact_id"`
	}
	if json.Unmarshal(e.Payload, &p) != nil {
		return "", false
	}
	id, err := uuid.Parse(p.ContactID)
	if err != nil {
		return "", false
	}
	return visitorChannel(id), true
}

// encodeWidgetEvent is the wire form of a widget event: no seq, the widget
// does not resume.
func encodeWidgetEvent(e outbox.Event) ([]byte, error) {
	b, err := json.Marshal(envelope{Type: e.Topic, ID: fmt.Sprint(e.ID), TS: e.CreatedAt.UTC(), Data: e.Payload})
	if err != nil {
		return nil, fmt.Errorf("realtime: encode widget event %d: %w", e.ID, err)
	}
	return b, nil
}

// VisitorTyping shows a visitor that an operator is typing a reply
// (channels.VisitorSignals). The signal bypasses the outbox like every
// signal (docs/02 §10.5).
func (p *Publisher) VisitorTyping(ctx context.Context, workspaceID, contactID uuid.UUID) error {
	msg := encodeControl(TypeWidgetTyping, map[string]string{"state": StateStart}, time.Now())
	if err := p.rdb.Publish(ctx, redisChannel(p.prefix, workspaceID, visitorChannel(contactID)), msg).Err(); err != nil {
		return fmt.Errorf("realtime: publish widget typing: %w", err)
	}
	return nil
}
