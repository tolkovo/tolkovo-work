package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/db"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/dbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime/schematest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// rig is a gateway on a fresh database and Redis prefix, served over real
// TCP, with the outbox relay publishing through Publisher: the whole
// path of ADR-0005 except the session cookie (covered in internal/api).
type rig struct {
	t      *testing.T
	d      dbtest.Database
	app    *db.DB
	gw     *Gateway
	srv    *httptest.Server
	schema *schematest.Schema

	mu      sync.Mutex
	recheck func(context.Context) (string, error)
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	d := dbtest.New(t)
	rc, prefix := rdbtest.New(t)
	app, sys := d.OpenApp(t), d.OpenSystem(t)
	o.ChannelPrefix = prefix
	r := &rig{t: t, d: d, app: app, gw: New(app, rc, quiet, o), schema: schematest.Load(t, schematest.ServerMessage)}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = r.gw.Run(ctx) }()
	// The relay is platform infrastructure on the system role, as in cmd.
	relay := outbox.NewRelay(sys.Pool(), NewPublisher(rc, prefix), quiet) //nolint:forbidigo // see above
	relay.PollInterval, relay.RetryInterval = 10*time.Millisecond, 20*time.Millisecond
	go func() { defer wg.Done(); _ = relay.Run(ctx) }()

	// Test-only authentication: the caller is described by the query.
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		cl := Client{
			UserID: uuid.MustParse(q.Get("user")), WorkspaceID: uuid.MustParse(q.Get("ws")), Role: q.Get("role"),
			Recheck: func(ctx context.Context) (string, error) {
				r.mu.Lock()
				f := r.recheck
				r.mu.Unlock()
				if f == nil {
					return q.Get("role"), nil
				}
				return f(ctx)
			},
		}
		switch err := r.gw.Serve(w, req, cl); {
		case errors.Is(err, ErrOriginNotAllowed):
			http.Error(w, "forbidden", http.StatusForbidden)
		case errors.Is(err, ErrUnavailable):
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		_ = r.gw.Shutdown(sctx)
		r.srv.Close()
		cancel()
		wg.Wait()
	})
	deadline := time.Now().Add(10 * time.Second)
	for !r.gw.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("gateway did not subscribe to Redis")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return r
}

func (r *rig) workspace() uuid.UUID {
	r.t.Helper()
	id := uuid.Must(uuid.NewV7())
	r.owner("INSERT INTO workspaces (id, slug, name) VALUES ($1, $2, 'Test')", id, "ws-"+id.String()[24:])
	return id
}

func (r *rig) owner(sql string, args ...any) {
	r.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, r.d.OwnerURL)
	if err != nil {
		r.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		r.t.Fatalf("exec %q: %v", sql, err)
	}
}

// add writes an event in a business transaction, as producers do.
func (r *rig) add(ws uuid.UUID, topic string) {
	r.t.Helper()
	ctx := context.Background()
	payload := json.RawMessage(`{"n":1}`)
	if p, ok := samplePayloads[topic]; ok && p != "" {
		payload = samplePayload(topic)
	}
	if err := r.app.InTenantTx(ctx, ws, func(tx pgx.Tx) error {
		return outbox.Add(ctx, tx, ws, topic, payload)
	}); err != nil {
		r.t.Fatalf("add %s: %v", topic, err)
	}
}

// waitPublished waits until the relay has published everything.
func (r *rig) waitPublished() {
	r.t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, r.d.OwnerURL)
	if err != nil {
		r.t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var n int
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE published_at IS NULL").Scan(&n); err != nil {
			r.t.Fatalf("count: %v", err)
		}
		if n == 0 {
			return
		}
	}
	r.t.Fatal("relay did not publish")
}

type wsClient struct {
	t  *testing.T
	r  *rig
	ws *websocket.Conn
}

type wireMsg struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Seq  int64           `json:"seq"`
	Data json.RawMessage `json:"data"`
}

func (m wireMsg) dataSeq() int64 {
	var v seqData
	_ = json.Unmarshal(m.Data, &v)
	return v.Seq
}

func (r *rig) dialURL(ws, user uuid.UUID, role string) string {
	return "ws" + strings.TrimPrefix(r.srv.URL, "http") + "/?ws=" + ws.String() + "&user=" + user.String() + "&role=" + role
}

// rawDial opens a connection with the given Origin ("" for none) and
// returns the HTTP status of a refused handshake.
func (r *rig) rawDial(ws, user uuid.UUID, role, origin string) (*websocket.Conn, int, error) {
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, r.dialURL(ws, user, role), &websocket.DialOptions{HTTPHeader: h})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	if err == nil {
		r.t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, status, err
}

func (r *rig) dial(ws, user uuid.UUID, role string) *wsClient {
	r.t.Helper()
	c, _, err := r.rawDial(ws, user, role, r.srv.URL)
	if err != nil {
		r.t.Fatalf("dial: %v", err)
	}
	return &wsClient{t: r.t, r: r, ws: c}
}

// next returns the next non-heartbeat message, validated against the schema.
func (c *wsClient) next() wireMsg {
	c.t.Helper()
	for {
		m, err := c.read(5 * time.Second)
		if err != nil {
			c.t.Fatalf("read: %v", err)
		}
		if m.Type != TypeHeartbeat {
			return m
		}
	}
}

func (c *wsClient) read(timeout time.Duration) (wireMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, b, err := c.ws.Read(ctx)
	if err != nil {
		return wireMsg{}, err
	}
	if err := c.r.schema.Validate(b); err != nil {
		c.t.Fatalf("server sent a message outside the contract: %v\n%s", err, b)
	}
	var m wireMsg
	if err := json.Unmarshal(b, &m); err != nil {
		c.t.Fatalf("decode: %v", err)
	}
	return m, nil
}

// closedWith reads until the server closes and returns the close code.
func (c *wsClient) closedWith() websocket.StatusCode {
	c.t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func (c *wsClient) send(msg string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func resumeMsg(lastSeq int64) string {
	b, _ := json.Marshal(map[string]any{
		"type": TypeResume, "id": uuid.NewString(), "ts": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{"last_seq": lastSeq},
	})
	return string(b)
}

func (c *wsClient) hello() int64 {
	c.t.Helper()
	m := c.next()
	if m.Type != TypeHello {
		c.t.Fatalf("first message must be hello, got %s", m.Type)
	}
	return m.dataSeq()
}

func TestGateway_RelaysLiveEventsOnlyToTheirWorkspaceAndPermittedRoles(t *testing.T) {
	r := newRig(t, Options{})
	a, b := r.workspace(), r.workspace()
	ca := r.dial(a, uuid.New(), authz.RoleOperator)
	cb := r.dial(b, uuid.New(), authz.RoleOperator)
	blind := r.dial(a, uuid.New(), "nobody")
	ca.hello()
	cb.hello()
	blind.hello()

	r.add(a, "conversation.created")
	r.add(b, "contact.created")
	r.add(a, "test.thing.created") // no route: stays on the server
	r.add(a, "message.created")

	first, second := ca.next(), ca.next()
	if first.Type != "conversation.created" || second.Type != "message.created" || second.Seq <= first.Seq {
		t.Fatalf("workspace A: got %s(%d), %s(%d)", first.Type, first.Seq, second.Type, second.Seq)
	}
	if m := cb.next(); m.Type != "contact.created" {
		t.Fatalf("workspace B: got %s", m.Type)
	}
	// Nothing else may arrive: not B's event in A, not A's in B, nothing
	// for a role without permissions.
	r.add(a, "contact.updated")
	if m := ca.next(); m.Type != "contact.updated" {
		t.Fatalf("workspace A: got %s, want contact.updated", m.Type)
	}
	if m, err := cb.read(300 * time.Millisecond); err == nil {
		t.Fatalf("workspace B got a foreign event %s", m.Type)
	}
	if m, err := blind.read(300 * time.Millisecond); err == nil {
		t.Fatalf("a role without permissions got %s", m.Type)
	}
}

func TestGateway_ResumeReplaysMissedEventsFromPostgres(t *testing.T) {
	r := newRig(t, Options{ReplayRecheckDelay: 50 * time.Millisecond})
	a := r.workspace()
	user := uuid.New()

	c1 := r.dial(a, user, authz.RoleOperator)
	c1.hello()
	r.add(a, "conversation.created")
	seen := c1.next()
	_ = c1.ws.Close(websocket.StatusNormalClosure, "")

	r.add(a, "conversation.assigned")
	r.add(a, "message.created")
	r.waitPublished()

	c2 := r.dial(a, user, authz.RoleOperator)
	if latest := c2.hello(); latest <= seen.Seq {
		t.Fatalf("hello seq %d must be past the missed events (last seen %d)", latest, seen.Seq)
	}
	c2.send(resumeMsg(seen.Seq))
	e1, e2, done := c2.next(), c2.next(), c2.next()
	if e1.Type != "conversation.assigned" || e2.Type != "message.created" {
		t.Fatalf("replay: got %s, %s", e1.Type, e2.Type)
	}
	if done.Type != TypeResumed || done.dataSeq() != e2.Seq {
		t.Fatalf("want resumed at %d, got %s at %d", e2.Seq, done.Type, done.dataSeq())
	}
	// The second pass finds nothing new and stays silent.
	if m, err := c2.read(300 * time.Millisecond); err == nil {
		t.Fatalf("unexpected %s after resumed", m.Type)
	}
}

func TestGateway_ResyncRequiredWhenEventsCannotBeReplayed(t *testing.T) {
	t.Run("pruned by retention", func(t *testing.T) {
		r := newRig(t, Options{})
		a := r.workspace()
		r.owner("UPDATE outbox_watermark SET pruned_seq = 1000")
		c := r.dial(a, uuid.New(), authz.RoleOperator)
		if seq := c.hello(); seq != 1000 {
			t.Fatalf("hello must start at the watermark, got %d", seq)
		}
		c.send(resumeMsg(3))
		if m := c.next(); m.Type != TypeResyncRequired || m.dataSeq() != 1000 {
			t.Fatalf("want resync_required at 1000, got %s at %d", m.Type, m.dataSeq())
		}
	})
	t.Run("too many to replay", func(t *testing.T) {
		r := newRig(t, Options{ReplayLimit: 2})
		a := r.workspace()
		for range 3 {
			r.add(a, "contact.updated")
		}
		r.waitPublished()
		c := r.dial(a, uuid.New(), authz.RoleOperator)
		latest := c.hello()
		c.send(resumeMsg(0))
		if m := c.next(); m.Type != TypeResyncRequired || m.dataSeq() != latest {
			t.Fatalf("want resync_required at %d, got %s at %d", latest, m.Type, m.dataSeq())
		}
	})
}

func TestGateway_ReplaysAfterRedisResubscribe(t *testing.T) {
	r := newRig(t, Options{})
	a := r.workspace()
	c := r.dial(a, uuid.New(), authz.RoleOperator)
	seq := c.hello()
	// An event that went out while this replica's link to Redis was down:
	// published in Postgres, never seen on the channel.
	r.owner(`INSERT INTO outbox_events (workspace_id, topic, payload, published_at, seq)
		VALUES ($1, 'contact.updated', $2, now(), $3)`, a, samplePayload("contact.updated"), seq+10)
	r.gw.hub.resyncAll()
	if m := c.next(); m.Type != "contact.updated" || m.Seq != seq+10 {
		t.Fatalf("want the missed event replayed, got %s at %d", m.Type, m.Seq)
	}
}

func TestGateway_ClosesWhenTheSessionEnds(t *testing.T) {
	r := newRig(t, Options{RecheckInterval: 50 * time.Millisecond})
	c := r.dial(r.workspace(), uuid.New(), authz.RoleOperator)
	c.hello()
	r.mu.Lock()
	r.recheck = func(context.Context) (string, error) { return "", ErrSessionEnded }
	r.mu.Unlock()
	if code := c.closedWith(); code != CloseSessionEnded {
		t.Fatalf("want 4401, got %d", code)
	}
}

func TestGateway_KeepsConnectionWhenRecheckFailsTransiently(t *testing.T) {
	r := newRig(t, Options{RecheckInterval: 20 * time.Millisecond})
	a := r.workspace()
	c := r.dial(a, uuid.New(), authz.RoleOperator)
	c.hello()
	r.mu.Lock()
	r.recheck = func(context.Context) (string, error) { return "", errors.New("redis is down") }
	r.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	r.add(a, "contact.created")
	if m := c.next(); m.Type != "contact.created" {
		t.Fatalf("an outage must not drop the connection, got %s", m.Type)
	}
}

func TestGateway_RejectsForeignAndMissingOrigin(t *testing.T) {
	r := newRig(t, Options{TrustedOrigins: []string{"https://panel.example.com"}})
	a := r.workspace()
	for name, origin := range map[string]string{"foreign": "https://evil.example.com", "missing": ""} {
		if _, status, err := r.rawDial(a, uuid.New(), authz.RoleOperator, origin); err == nil || status != http.StatusForbidden {
			t.Errorf("%s origin: want 403, got %d %v", name, status, err)
		}
	}
	// A trusted origin on another host passes.
	if _, _, err := r.rawDial(a, uuid.New(), authz.RoleOperator, "https://panel.example.com"); err != nil {
		t.Fatalf("trusted origin: %v", err)
	}
}

func TestGateway_LimitsConnectionsPerUser(t *testing.T) {
	r := newRig(t, Options{MaxConnsPerUser: 1})
	a, user := r.workspace(), uuid.New()
	r.dial(a, user, authz.RoleOperator).hello()
	if code := r.dial(a, user, authz.RoleOperator).closedWith(); code != CloseTooManyConnections {
		t.Fatalf("want 4429, got %d", code)
	}
	r.dial(a, uuid.New(), authz.RoleOperator).hello() // another user is fine
}

func TestGateway_ClosesOnProtocolViolation(t *testing.T) {
	r := newRig(t, Options{})
	a := r.workspace()
	for name, msgs := range map[string][]string{
		"unknown type":  {`{"type":"subscribe","id":"1","ts":"2026-10-04T12:00:00Z","data":{}}`},
		"second resume": {resumeMsg(0), resumeMsg(0)},
		"too big":       {resumeMsg(0)[:1] + strings.Repeat(" ", MaxClientMessageBytes) + resumeMsg(0)[1:]},
		"flood":         slices.Repeat([]string{signalMsg(TypeTyping, uuid.New(), StateStop)}, ClientMessageBurst+5),
	} {
		c := r.dial(a, uuid.New(), authz.RoleOperator)
		c.hello()
		for _, m := range msgs {
			c.send(m)
		}
		want := CloseProtocolViolation
		if name == "too big" {
			want = CloseMessageTooBig
		}
		if code := c.closedWith(); code != want {
			t.Errorf("%s: want %d, got %d", name, want, code)
		}
	}
}

func TestGateway_ShutdownAsksClientsToReconnect(t *testing.T) {
	r := newRig(t, Options{})
	a := r.workspace()
	c := r.dial(a, uuid.New(), authz.RoleOperator)
	c.hello()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = r.gw.Shutdown(ctx) }()
	if code := c.closedWith(); code != CloseServiceRestart {
		t.Fatalf("want 1012, got %d", code)
	}
	if _, status, err := r.rawDial(a, uuid.New(), authz.RoleOperator, r.srv.URL); err == nil || status != http.StatusServiceUnavailable {
		t.Fatalf("after shutdown: want 503, got %d %v", status, err)
	}
}

func signalMsg(typ string, conversationID uuid.UUID, state string) string {
	b, _ := json.Marshal(map[string]any{
		"type": typ, "id": uuid.NewString(), "ts": time.Now().UTC().Format(time.RFC3339Nano),
		"data": map[string]any{"conversation_id": conversationID, "state": state},
	})
	return string(b)
}

type viewersWire struct {
	ConversationID uuid.UUID   `json:"conversation_id"`
	UserIDs        []uuid.UUID `json:"user_ids"`
}

// viewers waits for the next conversation.viewers.
func (c *wsClient) viewers() viewersWire {
	c.t.Helper()
	m := c.next()
	if m.Type != TypeConversationViewers {
		c.t.Fatalf("want %s, got %s", TypeConversationViewers, m.Type)
	}
	var v viewersWire
	if err := json.Unmarshal(m.Data, &v); err != nil {
		c.t.Fatal(err)
	}
	return v
}

func sortedIDs(ids ...uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	return out
}

func TestGateway_RelaysTypingAndViewersWithinTheWorkspace(t *testing.T) {
	r := newRig(t, Options{})
	a, b := r.workspace(), r.workspace()
	alice, bob, conv := uuid.New(), uuid.New(), uuid.New()
	tab1, tab2 := r.dial(a, alice, authz.RoleOperator), r.dial(a, alice, authz.RoleOperator)
	bobc := r.dial(a, bob, authz.RoleAdmin)
	foreign := r.dial(b, uuid.New(), authz.RoleOperator)
	blind := r.dial(a, uuid.New(), "nobody")
	for _, c := range []*wsClient{tab1, tab2, bobc, foreign, blind} {
		c.hello()
	}
	expect := func(want []uuid.UUID, clients ...*wsClient) {
		t.Helper()
		for _, c := range clients {
			if v := c.viewers(); v.ConversationID != conv || !slices.Equal(v.UserIDs, want) {
				t.Fatalf("viewers: got %v of %s, want %v", v.UserIDs, v.ConversationID, want)
			}
		}
	}

	tab1.send(signalMsg(TypeViewing, conv, StateStart))
	expect([]uuid.UUID{alice}, tab1, tab2, bobc)
	bobc.send(signalMsg(TypeViewing, conv, StateStart))
	expect(sortedIDs(alice, bob), tab1, tab2, bobc)
	// A second tab of the same user is one viewer.
	tab2.send(signalMsg(TypeViewing, conv, StateStart))
	expect(sortedIDs(alice, bob), tab1, tab2, bobc)

	// Closing one tab keeps alice listed through the other.
	_ = tab1.ws.Close(websocket.StatusNormalClosure, "bye")
	expect(sortedIDs(alice, bob), tab2, bobc)
	// Opening another conversation leaves this one.
	tab2.send(signalMsg(TypeViewing, uuid.New(), StateStart))
	expect([]uuid.UUID{bob}, bobc)
	if v := tab2.viewers(); v.ConversationID != conv || !slices.Equal(v.UserIDs, []uuid.UUID{bob}) {
		t.Fatalf("tab2 sees the old conversation without it first: %+v", v)
	}
	if v := tab2.viewers(); v.ConversationID == conv || !slices.Equal(v.UserIDs, []uuid.UUID{alice}) {
		t.Fatalf("then the new one with alice: %+v", v)
	}
	_ = bobc.viewers() // the same new list

	bobc.send(signalMsg(TypeTyping, conv, StateStart))
	m := tab2.next()
	var typing typingData
	_ = json.Unmarshal(m.Data, &typing)
	if m.Type != TypeConversationTyping || typing.ConversationID != conv || typing.Actor != "operator" ||
		typing.ActorID == nil || *typing.ActorID != bob || typing.State != StateStart || m.Seq != 0 {
		t.Fatalf("typing: %s %+v seq=%d", m.Type, typing, m.Seq)
	}
	_ = bobc.next() // its own signal comes back too; the panel hides it

	// A role without permissions neither sends nor receives signals.
	blind.send(signalMsg(TypeTyping, conv, StateStart))
	blind.send(signalMsg(TypeViewing, conv, StateStart))
	// The connection closing ends bob's viewing.
	_ = bobc.ws.Close(websocket.StatusNormalClosure, "bye")
	if v := tab2.viewers(); v.ConversationID != conv || len(v.UserIDs) != 0 {
		t.Fatalf("bob left: %+v", v)
	}
	for _, c := range []*wsClient{foreign, blind} {
		if m, err := c.read(300 * time.Millisecond); err == nil {
			t.Fatalf("got a signal it must not see: %s %s", m.Type, m.Data)
		}
	}
	if m, err := tab2.read(300 * time.Millisecond); err == nil {
		t.Fatalf("signals of a role without permissions must be dropped, got %s %s", m.Type, m.Data)
	}
}

// TestGateway_ReplyTypingReachesChannels: only a reply start goes on to
// the channel (T-03); typing a note or stopping stays among colleagues.
func TestGateway_ReplyTypingReachesChannels(t *testing.T) {
	heard := make(chan uuid.UUID, 4)
	r := newRig(t, Options{OnReplyTyping: func(_ context.Context, _, conversationID uuid.UUID) { heard <- conversationID }})
	ws := r.workspace()
	c := r.dial(ws, uuid.New(), authz.RoleOperator)
	c.hello()
	typing := func(conv uuid.UUID, state, mode string) string {
		b, _ := json.Marshal(map[string]any{
			"type": TypeTyping, "id": uuid.NewString(), "ts": time.Now().UTC().Format(time.RFC3339Nano),
			"data": map[string]any{"conversation_id": conv, "state": state, "mode": mode},
		})
		return string(b)
	}
	note, stop, reply := uuid.New(), uuid.New(), uuid.New()
	c.send(typing(note, StateStart, ModeNote))
	c.send(signalMsg(TypeTyping, note, StateStart)) // no mode: a note
	c.send(typing(stop, StateStop, ModeReply))
	c.send(typing(reply, StateStart, ModeReply))
	select {
	case got := <-heard:
		if got != reply {
			t.Fatalf("channels heard %s, want the reply %s", got, reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reply start must reach the channels")
	}
	select {
	case got := <-heard:
		t.Fatalf("only the reply start goes on, also got %s", got)
	case <-time.After(300 * time.Millisecond):
	}
}
