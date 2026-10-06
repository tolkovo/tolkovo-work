package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

// Where a widget reports its goals: off until the admin turns a way on.
func TestWidget_Analytics(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	def := deref(w.channel.Details.Analytics)
	if def.DataLayer || def.Gtag || def.MetrikaCounter != nil || def.Goals.ChatStarted != "tolkovo.chatStarted" ||
		def.Goals.ContactCollected != "tolkovo.contactCollected" || def.Goals.Rated != "tolkovo.rated" {
		t.Fatalf("defaults: %+v", def)
	}
	v := e.visitor("198.51.100.60", shopOrigin)
	if got := v.start(w.key, nil).Analytics; !reflect.DeepEqual(got, def) {
		t.Fatalf("session analytics: %+v", got)
	}

	path := "/api/v1/channels/" + w.channel.Id.String() + "/widget-analytics"
	set := func(counter any, goals map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return w.owner.do(http.MethodPut, path, map[string]any{
			"data_layer": true, "gtag": false, "metrika_counter": counter, "goals": goals,
		})
	}
	rec := set(" 12345678 ", map[string]any{"chat_started": "", "contact_collected": "lead", "rated": "csat:widget"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}
	got := deref(decode[oapi.Channel](t, rec).Details.Analytics)
	if !got.DataLayer || got.Gtag || deref(got.MetrikaCounter) != "12345678" || got.Goals.ChatStarted != "tolkovo.chatStarted" ||
		got.Goals.ContactCollected != "lead" || got.Goals.Rated != "csat:widget" {
		t.Fatalf("saved: %+v", got)
	}
	// The next session start carries them; the origins stay.
	v.token = ""
	if s := v.start(w.key, nil).Analytics; !reflect.DeepEqual(s, got) {
		t.Fatalf("session after the change: %+v", s)
	}
	wantProblem(t, set("ym-123", map[string]any{"chat_started": "a", "contact_collected": "b", "rated": "c"}),
		http.StatusBadRequest, "invalid_request")
	wantProblem(t, set(nil, map[string]any{"chat_started": "чат", "contact_collected": "b", "rated": "c"}),
		http.StatusBadRequest, "invalid_request")

	// Another workspace's widget is not found; the setting needs channels.manage.
	other := e.connectWidget("other@example.test", "other", shopOrigin)
	wantProblem(t, other.owner.do(http.MethodPut, path, map[string]any{
		"data_layer": true, "gtag": true, "metrika_counter": nil,
		"goals": map[string]any{"chat_started": "a", "contact_collected": "b", "rated": "c"},
	}), http.StatusNotFound, "not_found")
}

// The visitor rates a resolved conversation; the operator sees it.
func TestWidget_Rating(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.61", shopOrigin)
	v.start(w.key, nil)
	rate := func(v *visitor, conv uuid.UUID, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body["conversation_id"] = conv
		return v.do(http.MethodPost, "/api/v1/widget/rating", body)
	}

	// Before the first message there is nothing to rate.
	if page := v.messages(""); page.Conversation != nil {
		t.Fatalf("conversation before the first message: %+v", page.Conversation)
	}
	wantProblem(t, rate(v, uuid.New(), map[string]any{"score": 5}), http.StatusNotFound, "not_found")

	rec := v.send("Где мой заказ?", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	convID := decode[oapi.WidgetMessage](t, rec).ConversationId
	if c := v.messages("").Conversation; c == nil || c.Id != convID || c.Status != oapi.WidgetConversationStatusOpen || c.Rating != nil {
		t.Fatalf("open conversation: %+v", c)
	}
	wantProblem(t, rate(v, convID, map[string]any{"score": 5}), http.StatusConflict, "conversation_open")

	// The operator takes it, replies and resolves it: the widget is told.
	conv := "/api/v1/conversations/" + convID.String()
	var owner, contact uuid.UUID
	e.ownerScan(&owner, "SELECT created_by FROM channels WHERE id = $1", w.channel.Id)
	e.ownerScan(&contact, "SELECT contact_id FROM conversations WHERE id = $1", convID)
	if rec := w.owner.do(http.MethodPut, conv+"/assignee", map[string]any{"assignee_id": owner}); rec.Code != http.StatusOK {
		t.Fatalf("take: %d %s", rec.Code, rec.Body)
	}
	if rec := w.owner.do(http.MethodPost, conv+"/messages", map[string]any{"body": "Заказ в пути"}); rec.Code != http.StatusCreated {
		t.Fatalf("reply: %d %s", rec.Code, rec.Body)
	}
	if rec := w.owner.do(http.MethodPost, conv+"/status", map[string]any{"status": "resolved"}); rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	var payload string
	e.ownerScan(&payload, "SELECT payload::text FROM outbox_events WHERE topic = 'widget.conversation' AND workspace_id = $1", w.ws)
	if !strings.Contains(payload, contact.String()) || !strings.Contains(payload, convID.String()) {
		t.Fatalf("widget.conversation payload: %s", payload)
	}
	if c := v.messages("").Conversation; c == nil || c.Status != oapi.WidgetConversationStatusResolved || c.Rating != nil {
		t.Fatalf("resolved conversation: %+v", c)
	}

	wantProblem(t, rate(v, convID, map[string]any{"score": 6}), http.StatusBadRequest, "invalid_request")
	wantProblem(t, rate(v, convID, map[string]any{"score": 3, "comment": strings.Repeat("я", 1001)}), http.StatusBadRequest, "invalid_request")
	rec = rate(v, convID, map[string]any{"score": 4, "comment": "  Быстро помогли  "})
	if rec.Code != http.StatusOK {
		t.Fatalf("rate: %d %s", rec.Code, rec.Body)
	}
	r := decode[oapi.ConversationRating](t, rec)
	if r.Score != 4 || deref(r.Comment) != "Быстро помогли" || r.ResolvedBy != oapi.Human {
		t.Fatalf("rating: %+v", r)
	}
	// The panel is told with ids only and reads the rating over REST.
	e.ownerScan(&payload, "SELECT payload::text FROM outbox_events WHERE topic = 'conversation.rated' AND workspace_id = $1", w.ws)
	if !strings.Contains(payload, convID.String()) || strings.Contains(payload, "помогли") {
		t.Fatalf("conversation.rated payload: %s", payload)
	}
	got := decode[oapi.Conversation](t, w.owner.do(http.MethodGet, conv, nil))
	if got.Rating == nil || got.Rating.Score != 4 || deref(got.Rating.Comment) != "Быстро помогли" {
		t.Fatalf("panel rating: %+v", got.Rating)
	}

	// Rating again replaces it; without a comment the comment goes.
	if rec := rate(v, convID, map[string]any{"score": 5}); rec.Code != http.StatusOK {
		t.Fatalf("rate again: %d %s", rec.Code, rec.Body)
	}
	if c := v.messages("").Conversation; c == nil || c.Rating == nil || c.Rating.Score != 5 || c.Rating.Comment != nil {
		t.Fatalf("after rating again: %+v", c)
	}

	// Another visitor cannot rate this conversation.
	stranger := e.visitor("198.51.100.62", shopOrigin)
	stranger.start(w.key, nil)
	if rec := stranger.send("Привет", ""); rec.Code != http.StatusCreated {
		t.Fatalf("stranger send: %d", rec.Code)
	}
	wantProblem(t, rate(stranger, convID, map[string]any{"score": 1}), http.StatusNotFound, "not_found")

	// Writing again reopens it: the widget is told and stops asking.
	if rec := v.send("И ещё вопрос", ""); rec.Code != http.StatusCreated {
		t.Fatalf("send again: %d %s", rec.Code, rec.Body)
	}
	if n := e.ownerCount("SELECT count(*) FROM outbox_events WHERE topic = 'widget.conversation' AND workspace_id = $1", w.ws); n != 2 {
		t.Fatalf("widget.conversation events: %d", n)
	}
	if c := v.messages("").Conversation; c == nil || c.Id != convID || c.Status != oapi.WidgetConversationStatusOpen {
		t.Fatalf("reopened: %+v", c)
	}
}
