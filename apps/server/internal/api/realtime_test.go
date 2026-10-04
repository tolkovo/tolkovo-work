package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tolkovo/tolkovo/apps/server/internal/realtime"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime/schematest"
	"github.com/tolkovo/tolkovo/apps/server/internal/tenancy"
)

// wsConn is a panel tab connected through the real router over TCP.
type wsConn struct {
	t      *testing.T
	ws     *websocket.Conn
	schema *schematest.Schema
}

// dialRealtime returns the connection, or the HTTP status of a refused
// handshake.
func dialRealtime(t *testing.T, srv *httptest.Server, c *client, origin string) (*wsConn, int, error) {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	if c != nil && c.cookie != nil {
		h.Set("Cookie", c.cookie.Name+"="+c.cookie.Value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+RealtimePath, &websocket.DialOptions{HTTPHeader: h})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	if err != nil {
		return nil, status, err
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return &wsConn{t: t, ws: ws, schema: schematest.Load(t, schematest.ServerMessage)}, status, nil
}

// next returns the next message other than heartbeat; every message must
// match api/ws/server-message.schema.json.
func (w *wsConn) next(timeout time.Duration) (map[string]any, error) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_, b, err := w.ws.Read(ctx)
		cancel()
		if err != nil {
			return nil, err
		}
		if err := w.schema.Validate(b); err != nil {
			w.t.Fatalf("message outside the contract: %v\n%s", err, b)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			w.t.Fatalf("decode: %v", err)
		}
		if m["type"] != realtime.TypeHeartbeat {
			return m, nil
		}
	}
}

func (w *wsConn) mustNext() map[string]any {
	w.t.Helper()
	m, err := w.next(5 * time.Second)
	if err != nil {
		w.t.Fatalf("read: %v", err)
	}
	return m
}

func TestRealtime_HandshakeNeedsSessionAndTrustedOrigin(t *testing.T) {
	e := newEnv(t, envOptions{withWS: true, trusted: []string{"https://panel.example.com"}})
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	owner, _ := e.register("owner@example.test", "zerno")

	if _, status, err := dialRealtime(t, srv, e.client(), srv.URL); err == nil || status != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d %v", status, err)
	}
	for _, origin := range []string{"https://evil.example.com", ""} {
		if _, status, err := dialRealtime(t, srv, owner, origin); err == nil || status != http.StatusForbidden {
			t.Fatalf("origin %q: want 403, got %d %v", origin, status, err)
		}
	}
	for _, origin := range []string{srv.URL, "https://panel.example.com"} {
		w, _, err := dialRealtime(t, srv, owner, origin)
		if err != nil {
			t.Fatalf("origin %q: %v", origin, err)
		}
		if m := w.mustNext(); m["type"] != realtime.TypeHello {
			t.Fatalf("want hello, got %v", m["type"])
		}
	}
}

// The real producers (internal/conversations) emit events that pass the
// schema, reach the members of their workspace and nobody else.
func TestRealtime_DeliversDomainEventsOfOwnWorkspaceWithinContract(t *testing.T) {
	e := newEnv(t, envOptions{withWS: true})
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	owner, so := e.register("owner@example.test", "zerno")
	_, sop := e.register("operator@example.test", "operator-home")
	e.addMember(so.Workspace.Id, sop.User.Id, tenancy.RoleOperator)
	stranger, _ := e.register("stranger@example.test", "other")

	ow, _, err := dialRealtime(t, srv, owner, srv.URL)
	if err != nil {
		t.Fatalf("owner dial: %v", err)
	}
	sw, _, err := dialRealtime(t, srv, stranger, srv.URL)
	if err != nil {
		t.Fatalf("stranger dial: %v", err)
	}
	ow.mustNext()
	sw.mustNext()

	in := inbox{e: e, owner: owner, so: so, sop: sop}
	c := in.conversation(t) // contact.created, conversation.created
	base := "/api/v1/conversations/" + c.Id.String()
	if rec := owner.do(http.MethodPut, base+"/assignee", map[string]any{"assignee_id": so.User.Id}); rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body)
	}
	if rec := owner.do(http.MethodPost, base+"/messages", map[string]any{"body": "hello"}); rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}

	want := map[string]bool{
		"contact.created": true, "conversation.created": true, "conversation.status_changed": true,
		"conversation.assigned": true, "message.created": true,
	}
	var lastSeq float64
	for len(want) > 0 {
		m := ow.mustNext()
		typ, _ := m["type"].(string)
		if !want[typ] {
			t.Fatalf("unexpected or repeated %s", typ)
		}
		delete(want, typ)
		if seq := m["seq"].(float64); seq <= lastSeq {
			t.Fatalf("seq must grow: %v after %v", seq, lastSeq)
		} else {
			lastSeq = seq
		}
		if data, _ := json.Marshal(m["data"]); strings.Contains(string(data), "hello") || strings.Contains(string(data), "ivan@") {
			t.Fatalf("message text and contact data must not travel in events: %s", data)
		}
	}
	if m, err := sw.next(300 * time.Millisecond); err == nil {
		t.Fatalf("another workspace received %v", m["type"])
	}
}

func TestRealtime_LogoutClosesTheConnection(t *testing.T) {
	e := newEnv(t, envOptions{withWS: true, realtime: realtime.Options{RecheckInterval: 50 * time.Millisecond}})
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	owner, _ := e.register("owner@example.test", "zerno")
	w, _, err := dialRealtime(t, srv, owner, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	w.mustNext()
	if rec := owner.do(http.MethodPost, "/api/v1/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	_, err = w.next(5 * time.Second)
	if code := websocket.CloseStatus(err); code != realtime.CloseSessionEnded {
		t.Fatalf("want close 4401 after logout, got %d (%v)", code, err)
	}
}
