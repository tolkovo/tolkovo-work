package realtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// conn is one WebSocket client. Its goroutines are owned by Gateway.Serve,
// which waits for all of them:
//
//   - writeLoop is the only writer and the one that closes with a code;
//   - readLoop reads client frames until the connection is gone;
//   - watchLoop sends heartbeats, pings and re-checks the session;
//   - replayLoop answers resume and server-side resyncs from Postgres.
//
// Live events arrive from the hub into a bounded buffer; replayed events
// and control messages go through an unbuffered channel, so a long replay
// waits for the writer instead of overflowing the live buffer.
type conn struct {
	g           *Gateway
	ws          *websocket.Conn
	userID      uuid.UUID
	workspaceID uuid.UUID
	recheck     func(context.Context) (string, error)
	cancel      context.CancelFunc

	role    atomic.Pointer[string]
	lastSeq atomic.Int64 // highest seq queued for this client
	resumed atomic.Bool

	live     chan []byte
	ctrl     chan []byte
	resumeCh chan int64
	resyncCh chan struct{}

	kill       chan struct{}
	killOnce   sync.Once
	killCode   websocket.StatusCode
	killReason string
}

func newConn(g *Gateway, ws *websocket.Conn, cl Client, cancel context.CancelFunc) *conn {
	c := &conn{
		g: g, ws: ws, userID: cl.UserID, workspaceID: cl.WorkspaceID, recheck: cl.Recheck, cancel: cancel,
		live:     make(chan []byte, g.o.SendBuffer),
		ctrl:     make(chan []byte),
		resumeCh: make(chan int64, 1),
		resyncCh: make(chan struct{}, 1),
		kill:     make(chan struct{}),
	}
	role := cl.Role
	c.role.Store(&role)
	return c
}

func (c *conn) currentRole() string { return *c.role.Load() }

// fail asks the writer to close the connection with code. The first call
// wins; it never blocks, so the hub may call it.
func (c *conn) fail(code websocket.StatusCode, reason string) {
	c.killOnce.Do(func() {
		c.killCode, c.killReason = code, reason
		close(c.kill)
	})
}

func (c *conn) failed() bool {
	select {
	case <-c.kill:
		return true
	default:
		return false
	}
}

// closeCode is the code the server closed with, 0 if it did not. Reading
// killCode after observing the closed channel is race-free.
func (c *conn) closeCode() websocket.StatusCode {
	if !c.failed() {
		return 0
	}
	return c.killCode
}

// bumpSeq raises lastSeq to seq.
func (c *conn) bumpSeq(seq int64) {
	for {
		cur := c.lastSeq.Load()
		if seq <= cur || c.lastSeq.CompareAndSwap(cur, seq) {
			return
		}
	}
}

// deliverLive queues a fan-out event. Called by the hub under its read
// lock, so it must not block.
func (c *conn) deliverLive(channel string, msg []byte, seq int64) {
	if !channelAllowed(c.currentRole(), channel) {
		return
	}
	select {
	case c.live <- msg:
		c.bumpSeq(seq)
	default:
		c.fail(CloseSlowConsumer, "slow consumer")
	}
}

// requestResync asks for a replay from lastSeq (after a Redis reconnect).
func (c *conn) requestResync() {
	select {
	case c.resyncCh <- struct{}{}:
	default: // one is already pending
	}
}

// sendCtrl queues a control or replayed message, waiting for the writer.
func (c *conn) sendCtrl(ctx context.Context, msg []byte) bool {
	select {
	case c.ctrl <- msg:
		return true
	case <-c.kill:
		return false
	case <-ctx.Done():
		return false
	}
}

func (c *conn) write(ctx context.Context, msg []byte) error {
	wctx, cancel := context.WithTimeout(ctx, c.g.o.WriteTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, msg)
}

func (c *conn) writeLoop(ctx context.Context) {
	for {
		select {
		case <-c.kill:
			// Close runs the close handshake; readLoop keeps reading so the
			// peer's answer is consumed.
			_ = c.ws.Close(c.killCode, c.killReason)
			return
		case <-ctx.Done():
			_ = c.ws.CloseNow()
			return
		case msg := <-c.ctrl:
			if c.write(ctx, msg) != nil {
				_ = c.ws.CloseNow()
				return
			}
		case msg := <-c.live:
			if c.write(ctx, msg) != nil {
				_ = c.ws.CloseNow()
				return
			}
		}
	}
}

// readLoop returns when the connection is closed. After a protocol error
// it keeps reading (and ignoring) frames until the writer's close
// handshake completes.
func (c *conn) readLoop(ctx context.Context) {
	for {
		typ, b, err := c.ws.Read(ctx)
		if err != nil {
			return
		}
		if c.failed() {
			continue
		}
		if typ != websocket.MessageText {
			c.fail(CloseProtocolViolation, "text frames only")
			continue
		}
		last, err := parseResume(b)
		if err != nil {
			c.fail(CloseProtocolViolation, "invalid message")
			continue
		}
		if c.resumed.Swap(true) {
			c.fail(CloseProtocolViolation, "resume is accepted once")
			continue
		}
		c.resumeCh <- last // capacity 1 and accepted once: never blocks
	}
}

// watchLoop keeps the connection honest: a heartbeat for the client, a
// ping for the server (a dead peer does not answer), and a session
// re-check so a revoked session or a removed member is cut off.
func (c *conn) watchLoop(ctx context.Context) {
	hb := time.NewTicker(c.g.o.HeartbeatInterval)
	defer hb.Stop()
	rc := time.NewTicker(c.g.o.RecheckInterval)
	defer rc.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			if !c.sendCtrl(ctx, encodeControl(TypeHeartbeat, struct{}{}, c.g.now())) {
				return
			}
			pctx, cancel := context.WithTimeout(ctx, c.g.o.PingTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					_ = c.ws.CloseNow() // no pong: the peer is gone, no handshake possible
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

func (c *conn) recheckSession(ctx context.Context) bool {
	if c.recheck == nil {
		return true
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	role, err := c.recheck(rctx)
	cancel()
	switch {
	case errors.Is(err, ErrSessionEnded):
		c.fail(CloseSessionEnded, "session ended")
		return false
	case err != nil:
		// An outage of Redis or Postgres is not a revoked session: keep the
		// connection and try again on the next tick.
		if ctx.Err() == nil {
			c.g.log.Warn("realtime session re-check failed", "workspace_id", c.workspaceID, "err", err)
		}
		return true
	default:
		c.role.Store(&role)
		return true
	}
}

// replayLoop serves resume once, re-checks shortly after it, and replays
// after server-side resyncs. The second pass closes a race: the relay
// publishes to Redis before it marks events in Postgres, so an event
// published just before this connection subscribed may become readable
// only a moment after the first pass.
func (c *conn) replayLoop(ctx context.Context) {
	var (
		again     <-chan time.Time
		againFrom int64
		timer     *time.Timer
	)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case last := <-c.resumeCh:
			top, ok := c.replay(ctx, last, true)
			if !ok {
				return
			}
			timer = time.NewTimer(c.g.o.ReplayRecheckDelay)
			again, againFrom = timer.C, top
		case <-again:
			again = nil
			if _, ok := c.replay(ctx, againFrom, false); !ok {
				return
			}
		case <-c.resyncCh:
			if _, ok := c.replay(ctx, c.lastSeq.Load(), false); !ok {
				return
			}
		}
	}
}

// replay sends the events after afterSeq, or resync_required when they
// cannot all be replayed. It returns the highest seq covered.
func (c *conn) replay(ctx context.Context, afterSeq int64, reply bool) (int64, bool) {
	limit := c.g.o.ReplayLimit
	cu, err := c.g.store.since(ctx, c.workspaceID, c.currentRole(), afterSeq, limit+1)
	if err != nil {
		if ctx.Err() == nil {
			c.g.log.Warn("realtime replay failed", "workspace_id", c.workspaceID, "err", err)
			c.fail(CloseInternalError, "replay failed")
		}
		return 0, false
	}
	if afterSeq < cu.pruned || len(cu.events) > limit {
		c.bumpSeq(cu.latest)
		return cu.latest, c.sendCtrl(ctx, encodeControl(TypeResyncRequired, seqData{Seq: cu.latest}, c.g.now()))
	}
	top := afterSeq
	for _, e := range cu.events {
		msg, err := encodeEvent(e)
		if err != nil {
			c.g.log.Error("realtime: skip an event that does not encode", "workspace_id", c.workspaceID, "id", e.ID, "err", err)
			continue
		}
		if !c.sendCtrl(ctx, msg) {
			return 0, false
		}
		top = max(top, e.Seq)
		c.bumpSeq(e.Seq)
	}
	if reply && !c.sendCtrl(ctx, encodeControl(TypeResumed, seqData{Seq: top}, c.g.now())) {
		return 0, false
	}
	return top, true
}
