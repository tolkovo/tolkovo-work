package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime"
	"github.com/tolkovo/tolkovo/apps/server/internal/realtime/schematest"
)

const shopOrigin = "https://shop.example.test"

// widgetSetup is a workspace with a connected widget.
type widgetSetup struct {
	owner    *client
	ws       uuid.UUID
	channel  oapi.Channel
	key      string
	identity string
}

func (e *env) connectWidget(email, slug string, origins ...string) widgetSetup {
	e.t.Helper()
	c, sess := e.register(email, slug)
	rec := c.do(http.MethodPost, "/api/v1/channels/widget", map[string]any{"name": "Чат на сайте", "allowed_origins": origins})
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("connect widget: %d %s", rec.Code, rec.Body)
	}
	created := decode[oapi.WidgetChannelCreated](e.t, rec)
	return widgetSetup{
		owner: c, ws: sess.Workspace.Id, channel: created.Channel, key: deref(created.Channel.Details.WidgetKey),
		identity: created.IdentitySecret,
	}
}

// visitor is a browser tab of a site with the widget: no cookies, an
// Origin, a session token once started.
type visitor struct {
	c      *client
	origin string
	token  string
}

func (e *env) visitor(ip, origin string) *visitor {
	return &visitor{c: &client{e: e, ip: ip}, origin: origin}
}

func (v *visitor) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	v.c.e.t.Helper()
	all := []reqOpt{}
	if v.origin != "" {
		all = append(all, header("Origin", v.origin))
	}
	if v.token != "" {
		all = append(all, bearer(v.token))
	}
	return v.c.do(method, path, body, append(all, opts...)...)
}

func (v *visitor) start(key string, user map[string]any) oapi.WidgetSession {
	v.c.e.t.Helper()
	body := map[string]any{"widget_key": key}
	if v.token != "" {
		body["session_token"] = v.token
	}
	if user != nil {
		body["user"] = user
	}
	rec := v.do(http.MethodPost, "/api/v1/widget/session", body)
	if rec.Code != http.StatusOK {
		v.c.e.t.Fatalf("start session: %d %s", rec.Code, rec.Body)
	}
	s := decode[oapi.WidgetSession](v.c.e.t, rec)
	v.token = s.SessionToken
	return s
}

func (v *visitor) send(text, key string) *httptest.ResponseRecorder {
	v.c.e.t.Helper()
	var opts []reqOpt
	if key != "" {
		opts = append(opts, header("Idempotency-Key", key))
	}
	return v.do(http.MethodPost, "/api/v1/widget/messages", map[string]any{"message": text}, opts...)
}

func (v *visitor) messages(since string) oapi.WidgetMessages {
	v.c.e.t.Helper()
	path := "/api/v1/widget/messages"
	if since != "" {
		path += "?since=" + since
	}
	rec := v.do(http.MethodGet, path, nil)
	if rec.Code != http.StatusOK {
		v.c.e.t.Fatalf("messages: %d %s", rec.Code, rec.Body)
	}
	return decode[oapi.WidgetMessages](v.c.e.t, rec)
}

func userHash(secret, id string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

// The visitor's chat from the first message to the operator's read reply.
func TestWidget_Chat(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin+":443", "APP://Zerno")
	if w.channel.Type != oapi.ChannelTypeWidget || !strings.HasPrefix(w.key, "tk_widget_") {
		t.Fatalf("channel: %+v", w.channel)
	}
	if got := deref(w.channel.Details.AllowedOrigins); len(got) != 2 || got[0] != shopOrigin || got[1] != "app://zerno" {
		t.Fatalf("origins are not canonical: %v", got)
	}

	v := e.visitor("198.51.100.7", shopOrigin)
	sess := v.start(w.key, nil)
	if sess.Identity != oapi.WidgetSessionIdentityNone || sess.ChannelName != "Чат на сайте" || !strings.HasPrefix(sess.SessionToken, "tk_wsess_") {
		t.Fatalf("session: %+v", sess)
	}
	// Opening the widget creates nothing in the inbox.
	if n := e.ownerCount("SELECT count(*) FROM contacts WHERE workspace_id = $1", w.ws); n != 0 {
		t.Fatalf("contacts before the first message: %d", n)
	}
	if page := v.messages(""); len(page.Items) != 0 {
		t.Fatalf("messages before the first: %+v", page)
	}
	// The same token goes on.
	if again := v.start(w.key, nil); again.SessionToken != sess.SessionToken {
		t.Fatal("a working token was replaced")
	}

	rec := v.send("  Здравствуйте, где мой заказ?  ", "k1")
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	first := decode[oapi.WidgetMessage](t, rec)
	if first.Sender != oapi.WidgetMessageSenderContact || first.Body != "Здравствуйте, где мой заказ?" {
		t.Fatalf("message: %+v", first)
	}
	if rec := v.send("Здравствуйте, где мой заказ?", "k1"); rec.Code != http.StatusOK || decode[oapi.WidgetMessage](t, rec).Id != first.Id {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, v.send("Другой текст", "k1"), http.StatusUnprocessableEntity, "idempotency_key_reused")
	var status, thread string
	var contact uuid.UUID
	e.ownerScan(&status, "SELECT status FROM conversations WHERE id = $1", first.ConversationId)
	e.ownerScan(&thread, "SELECT external_thread_id FROM conversations WHERE id = $1", first.ConversationId)
	e.ownerScan(&contact, "SELECT contact_id FROM conversations WHERE id = $1", first.ConversationId)
	if status != "queued" || thread != contact.String() {
		t.Fatalf("conversation: status %s, thread %s, contact %s", status, thread, contact)
	}

	// The operator takes it, leaves a note and replies.
	conv := "/api/v1/conversations/" + first.ConversationId.String()
	var owner uuid.UUID
	e.ownerScan(&owner, "SELECT created_by FROM channels WHERE id = $1", w.channel.Id)
	if rec := w.owner.do(http.MethodPut, conv+"/assignee", map[string]any{"assignee_id": owner}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	if rec := w.owner.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Проверить склад", "private": true}); rec.Code != http.StatusCreated {
		t.Fatalf("note: %d %s", rec.Code, rec.Body)
	}
	rec = w.owner.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Заказ передан в доставку"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	reply := decode[oapi.Message](t, rec)
	// The push to the visitor is stored with the reply; ids only.
	var payload string
	e.ownerScan(&payload, "SELECT payload::text FROM outbox_events WHERE topic = 'widget.message' AND workspace_id = $1", w.ws)
	if !strings.Contains(payload, contact.String()) || !strings.Contains(payload, reply.Id.String()) || strings.Contains(payload, "доставку") {
		t.Fatalf("widget.message payload: %s", payload)
	}

	page := v.messages("")
	if len(page.Items) != 2 || page.Items[0].Id != first.Id || page.Items[1].Id != reply.Id ||
		page.Items[1].Sender != oapi.WidgetMessageSenderOperator || page.Items[1].Body != "Заказ передан в доставку" {
		t.Fatalf("chat: %+v", page.Items)
	}
	// Fresh messages are not settled: the cursor stays and they come again.
	if again := v.messages(page.Cursor); len(again.Items) != 2 {
		t.Fatalf("unsettled messages after the cursor: %+v", again.Items)
	}

	// Receipts move the reply to delivered, then read, never back.
	receipt := func(body map[string]any) {
		t.Helper()
		if rec := v.do(http.MethodPost, "/api/v1/widget/receipts", body); rec.Code != http.StatusNoContent {
			t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
		}
	}
	delivery := func() string {
		var s string
		e.ownerScan(&s, "SELECT delivery_status FROM messages WHERE id = $1", reply.Id)
		return s
	}
	receipt(map[string]any{"delivered_up_to": reply.Id})
	if got := delivery(); got != "delivered" {
		t.Fatalf("after delivered: %s", got)
	}
	receipt(map[string]any{"read_up_to": reply.Id})
	receipt(map[string]any{"delivered_up_to": reply.Id})
	if got := delivery(); got != "read" {
		t.Fatalf("after read: %s", got)
	}
	if n := e.ownerCount("SELECT count(*) FROM outbox_events WHERE topic = 'message.delivery' AND payload->>'message_id' = $1", reply.Id.String()); n < 2 {
		t.Fatalf("message.delivery events: %d", n)
	}
	wantProblem(t, v.do(http.MethodPost, "/api/v1/widget/receipts", map[string]any{}), http.StatusBadRequest, "invalid_request")

	// Signing out ends the session.
	if rec := v.do(http.MethodDelete, "/api/v1/widget/session", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("end: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, v.do(http.MethodGet, "/api/v1/widget/messages", nil), http.StatusUnauthorized, "widget_session_ended")
	if rec := v.do(http.MethodDelete, "/api/v1/widget/session", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("end twice: %d", rec.Code)
	}
	// A new session on the device starts with an empty chat.
	v.token = ""
	v.start(w.key, nil)
	if page := v.messages(""); len(page.Items) != 0 {
		t.Fatalf("chat of a new session: %+v", page.Items)
	}
}

// Only the pages the admin allowed are answered; CORS lets them read it.
func TestWidget_Origins(t *testing.T) {
	e := newEnv(t, envOptions{trusted: []string{"https://panel.example.com"}})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin, "app://zerno")

	pre := httptest.NewRequest(http.MethodOptions, "/api/v1/widget/messages", nil)
	pre.Header.Set("Origin", "app://zerno")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pre.Header.Set("Access-Control-Request-Headers", "authorization, content-type, idempotency-key")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, pre)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "app://zerno" ||
		!strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}

	app := e.visitor("198.51.100.8", "app://zerno")
	app.start(w.key, nil)
	if rec := app.send("Из приложения", ""); rec.Code != http.StatusCreated || rec.Header().Get("Access-Control-Allow-Origin") != "app://zerno" {
		t.Fatalf("send from the app: %d %v", rec.Code, rec.Header())
	}

	for _, origin := range []string{"https://evil.example.test", "null", ""} {
		v := e.visitor("198.51.100.9", origin)
		wantProblem(t, v.do(http.MethodPost, "/api/v1/widget/session", map[string]any{"widget_key": w.key}), http.StatusForbidden, "origin_not_allowed")
		v.token = app.token
		wantProblem(t, v.do(http.MethodGet, "/api/v1/widget/messages", nil), http.StatusForbidden, "origin_not_allowed")
	}
	v := e.visitor("198.51.100.9", shopOrigin)
	for _, key := range []string{"", "tk_widget_nope", "tk_widget_" + strings.Repeat("A", 43)} {
		wantProblem(t, v.do(http.MethodPost, "/api/v1/widget/session", map[string]any{"widget_key": key}), http.StatusUnauthorized, "invalid_widget_key")
	}
	wantProblem(t, v.do(http.MethodGet, "/api/v1/widget/messages", nil, bearer("tk_wsess_forged")), http.StatusUnauthorized, "widget_session_ended")

	// The admin removes the app: its session stops at once.
	path := "/api/v1/channels/" + w.channel.Id.String() + "/widget-origins"
	wantProblem(t, w.owner.do(http.MethodPut, path, map[string]any{"allowed_origins": []string{"*"}}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, w.owner.do(http.MethodPut, path, map[string]any{"allowed_origins": []string{"https://*.com"}}), http.StatusBadRequest, "invalid_request")
	rec = w.owner.do(http.MethodPut, path, map[string]any{"allowed_origins": []string{"https://*.example.test"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("set origins: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, app.do(http.MethodGet, "/api/v1/widget/messages", nil), http.StatusForbidden, "origin_not_allowed")
	sub := e.visitor("198.51.100.10", "https://m.shop.example.test")
	sub.start(w.key, nil)

	// A disconnected widget ends its sessions; its key stops working.
	if rec := w.owner.do(http.MethodDelete, "/api/v1/channels/"+w.channel.Id.String(), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, sub.do(http.MethodGet, "/api/v1/widget/messages", nil), http.StatusUnauthorized, "widget_session_ended")
	sub.token = ""
	wantProblem(t, sub.do(http.MethodPost, "/api/v1/widget/session", map[string]any{"widget_key": w.key}), http.StatusUnauthorized, "invalid_widget_key")

	// Widget paths of other channel types are 404.
	a := e.connectApp("app@example.test", "app-co")
	wantProblem(t, a.owner.do(http.MethodPut, "/api/v1/channels/"+a.channel.Id.String()+"/widget-origins",
		map[string]any{"allowed_origins": []string{shopOrigin}}), http.StatusNotFound, "not_found")
}

// A visitor the company's backend signed continues with the contact of
// their id on every device; a claim never reaches anyone's history (W-04).
func TestWidget_Identity(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	user := map[string]any{"external_id": "u-42", "user_hash": userHash(w.identity, "u-42"), "name": "Марина", "email": "marina@example.test"}

	laptop := e.visitor("198.51.100.11", shopOrigin)
	if s := laptop.start(w.key, user); s.Identity != oapi.WidgetSessionIdentityVerified {
		t.Fatalf("signed user: %+v", s)
	}
	if rec := laptop.send("С ноутбука", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	phone := e.visitor("198.51.100.12", shopOrigin)
	phone.start(w.key, user)
	if page := phone.messages(""); len(page.Items) != 1 || page.Items[0].Body != "С ноутбука" {
		t.Fatalf("the same user on another device: %+v", page.Items)
	}
	if n := e.ownerCount("SELECT count(*) FROM contacts WHERE workspace_id = $1 AND external_id = 'u-42'", w.ws); n != 1 {
		t.Fatalf("contacts of u-42: %d", n)
	}

	// A wrong hash is a claim: a new unverified contact, an empty chat.
	forger := e.visitor("198.51.100.13", shopOrigin)
	claim := map[string]any{"external_id": "u-42", "user_hash": strings.Repeat("0", 64), "name": "Не Марина"}
	if s := forger.start(w.key, claim); s.Identity != oapi.WidgetSessionIdentityUnverified {
		t.Fatalf("forged hash: %+v", s)
	}
	if page := forger.messages(""); len(page.Items) != 0 {
		t.Fatalf("a claim sees someone's chat: %+v", page.Items)
	}
	if rec := forger.send("Покажи историю", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if n := e.ownerCount("SELECT count(*) FROM contacts WHERE workspace_id = $1 AND identity_unverified AND external_id IS NULL AND name = 'Не Марина'", w.ws); n != 1 {
		t.Fatalf("unverified contacts: %d", n)
	}
	if page := forger.messages(""); len(page.Items) != 1 || page.Items[0].Body != "Покажи историю" {
		t.Fatalf("forger's own chat: %+v", page.Items)
	}

	// Signing in after an anonymous chat switches the session; the
	// anonymous conversation stays with the anonymous contact.
	anon := e.visitor("198.51.100.14", shopOrigin)
	anon.start(w.key, nil)
	if rec := anon.send("До входа", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	token := anon.token
	if s := anon.start(w.key, user); s.Identity != oapi.WidgetSessionIdentityVerified || s.SessionToken != token {
		t.Fatalf("sign in: %+v", s)
	}
	if page := anon.messages(""); len(page.Items) != 1 || page.Items[0].Body != "С ноутбука" {
		t.Fatalf("chat after signing in: %+v", page.Items)
	}

	// A new secret: the old hash is a claim from now on.
	rec := w.owner.do(http.MethodPost, "/api/v1/channels/"+w.channel.Id.String()+"/identity-secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	late := e.visitor("198.51.100.15", shopOrigin)
	if s := late.start(w.key, user); s.Identity != oapi.WidgetSessionIdentityUnverified {
		t.Fatalf("old hash after rotation: %+v", s)
	}
	wantProblem(t, late.do(http.MethodPost, "/api/v1/widget/session",
		map[string]any{"widget_key": w.key, "user": map[string]any{"user_hash": "ab"}}), http.StatusBadRequest, "invalid_request")
}

// Workspaces never see each other's visitors.
func TestWidget_Isolation(t *testing.T) {
	e := newEnv(t, envOptions{})
	a := e.connectWidget("a@example.test", "shop-a", shopOrigin)
	b := e.connectWidget("b@example.test", "shop-b", shopOrigin)
	va := e.visitor("198.51.100.20", shopOrigin)
	va.start(a.key, nil)
	if rec := va.send("Привет из A", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d", rec.Code)
	}
	// A's token with B's key: a new session of B, not A's chat.
	vb := &visitor{c: va.c, origin: shopOrigin, token: va.token}
	if s := vb.start(b.key, nil); s.SessionToken == va.token {
		t.Fatal("A's token continued in B")
	}
	if page := vb.messages(""); len(page.Items) != 0 {
		t.Fatalf("B's visitor sees A: %+v", page.Items)
	}
	if n := e.ownerCount("SELECT count(*) FROM conversations WHERE workspace_id = $1", b.ws); n != 0 {
		t.Fatalf("conversations in B: %d", n)
	}
}

// The widget's connection gets its own chat's replies and nothing else.
func TestWidget_Realtime(t *testing.T) {
	e := newEnv(t, envOptions{withWS: true})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	schema := schematest.Load(t, schematest.WidgetMessage)

	dial := func(v *visitor, protocols ...string) (*websocket.Conn, int) {
		t.Helper()
		h := http.Header{}
		h.Set("Origin", v.origin)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+WidgetRealtimePath,
			&websocket.DialOptions{HTTPHeader: h, Subprotocols: protocols})
		status := 0
		if resp != nil {
			status = resp.StatusCode
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		if err != nil {
			return nil, status
		}
		t.Cleanup(func() { _ = ws.CloseNow() })
		return ws, status
	}
	next := func(ws *websocket.Conn) map[string]any {
		t.Helper()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, b, err := ws.Read(ctx)
			cancel()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := schema.Validate(b); err != nil {
				t.Fatalf("message outside the contract: %v\n%s", err, b)
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["type"] != realtime.TypeHeartbeat {
				return m
			}
		}
	}

	v := e.visitor("198.51.100.30", shopOrigin)
	v.start(w.key, nil)
	if rec := v.send("Есть кто?", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d", rec.Code)
	}
	other := e.visitor("198.51.100.31", shopOrigin)
	other.start(w.key, nil)
	if rec := other.send("Я другой", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send: %d", rec.Code)
	}

	if _, status := dial(v, realtime.WidgetSubprotocol, "tk_wsess_forged"); status != http.StatusUnauthorized {
		t.Fatalf("forged token: %d", status)
	}
	evil := &visitor{origin: "https://evil.example.test", token: v.token}
	if _, status := dial(evil, realtime.WidgetSubprotocol, v.token); status != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", status)
	}
	ws, _ := dial(v, realtime.WidgetSubprotocol, v.token)
	if ws == nil || ws.Subprotocol() != realtime.WidgetSubprotocol {
		t.Fatal("handshake failed")
	}
	if m := next(ws); m["type"] != realtime.TypeHello {
		t.Fatalf("first message: %v", m)
	}
	otherWS, _ := dial(other, realtime.WidgetSubprotocol, other.token)
	next(otherWS) // hello

	// The operator replies to v: v hears it, the other visitor does not.
	var convID uuid.UUID
	e.ownerScan(&convID, "SELECT conversation_id FROM messages WHERE body = 'Есть кто?'")
	var owner uuid.UUID
	e.ownerScan(&owner, "SELECT created_by FROM channels WHERE id = $1", w.channel.Id)
	conv := "/api/v1/conversations/" + convID.String()
	if rec := w.owner.do(http.MethodPut, conv+"/assignee", map[string]any{"assignee_id": owner}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d", rec.Code)
	}
	rec := w.owner.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Да, слушаю"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	reply := decode[oapi.Message](t, rec)
	m := next(ws)
	data, _ := m["data"].(map[string]any)
	if m["type"] != realtime.TypeWidgetMessage || data["message_id"] != reply.Id.String() || data["conversation_id"] != convID.String() {
		t.Fatalf("widget.message: %v", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	for {
		_, b, err := otherWS.Read(ctx)
		if err != nil {
			break
		}
		if strings.Contains(string(b), realtime.TypeWidgetMessage) {
			t.Fatalf("another visitor got the reply: %s", b)
		}
	}

	// The widget sends nothing over the connection.
	if err := ws.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resume"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	for {
		if _, _, err := ws.Read(rctx); err != nil {
			if websocket.CloseStatus(err) != realtime.CloseProtocolViolation {
				t.Fatalf("close after a client frame: %v", err)
			}
			break
		}
	}
}
