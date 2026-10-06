package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
)

const policyURL = "https://shop.example.test/privacy"

func (v *visitor) introduce(body map[string]any) {
	v.c.e.t.Helper()
	if rec := v.do(http.MethodPost, "/api/v1/widget/contact", body); rec.Code != http.StatusNoContent {
		v.c.e.t.Fatalf("pre-chat form: %d %s", rec.Code, rec.Body)
	}
}

// contactOf returns the contact of the visitor's first conversation.
func (e *env) contactOf(conversation uuid.UUID) (name, email, phone, consent string, unverified, placeholder bool) {
	e.t.Helper()
	e.ownerScan(&name, "SELECT k.name FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", conversation)
	e.ownerScan(&email, "SELECT coalesce(k.email::text, '') FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", conversation)
	e.ownerScan(&phone, "SELECT coalesce(k.phone, '') FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", conversation)
	e.ownerScan(&consent, "SELECT coalesce(k.attributes->'consent'->>'policy_url', '') FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", conversation)
	e.ownerScan(&unverified, "SELECT k.identity_unverified FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", conversation)
	e.ownerScan(&placeholder, "SELECT k.name_placeholder FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", conversation)
	return
}

// The pre-chat form before the first message: nothing in the inbox until
// the visitor writes, then a contact with what they typed and the consent.
func TestWidget_Prechat(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)

	v := e.visitor("198.51.100.30", shopOrigin)
	v.start(w.key, nil)
	v.introduce(map[string]any{
		"name": "  Анна  ", "email": "anna@example.test", "phone": "+7 900 123-45-67",
		"consent": map[string]any{"policy_url": policyURL},
	})
	if n := e.ownerCount("SELECT count(*) FROM contacts WHERE workspace_id = $1", w.ws); n != 0 {
		t.Fatalf("contacts before the first message: %d", n)
	}
	// The next start says the visitor introduced themselves.
	if s := v.start(w.key, nil); s.Identity != oapi.WidgetSessionIdentityUnverified {
		t.Fatalf("session after the form: %+v", s)
	}

	rec := v.send("Здравствуйте", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	first := decode[oapi.WidgetMessage](t, rec)
	name, email, phone, consent, unverified, placeholder := e.contactOf(first.ConversationId)
	if name != "Анна" || email != "anna@example.test" || phone != "+7 900 123-45-67" || consent != policyURL || !unverified || placeholder {
		t.Fatalf("contact: %q %q %q %q unverified=%v placeholder=%v", name, email, phone, consent, unverified, placeholder)
	}
	var source string
	e.ownerScan(&source, "SELECT k.attributes->'consent'->>'source' FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", first.ConversationId)
	if source != "widget_prechat" {
		t.Fatalf("consent source: %q", source)
	}

	// After the first message the form fills what the contact lacks, never
	// overwrites, and the operator sees it.
	v.introduce(map[string]any{"name": "Не Анна", "phone": "+7 900 000-00-00"})
	if name, _, phone, _, _, _ := e.contactOf(first.ConversationId); name != "Анна" || phone != "+7 900 123-45-67" {
		t.Fatalf("overwritten: %q %q", name, phone)
	}

	// An anonymous visitor who wrote first gets a guest name, then the form
	// replaces it.
	anon := e.visitor("198.51.100.31", shopOrigin)
	anon.start(w.key, nil)
	rec = anon.send("Без имени", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	conv := decode[oapi.WidgetMessage](t, rec).ConversationId
	if name, _, _, _, unverified, placeholder := e.contactOf(conv); !strings.HasPrefix(name, "Гость ") || !placeholder || unverified {
		t.Fatalf("anonymous contact: %q placeholder=%v unverified=%v", name, placeholder, unverified)
	}
	anon.introduce(map[string]any{"name": "Борис", "consent": map[string]any{"policy_url": policyURL}})
	if name, _, _, consent, unverified, placeholder := e.contactOf(conv); name != "Борис" || placeholder || !unverified || consent != policyURL {
		t.Fatalf("after the form: %q placeholder=%v unverified=%v consent=%q", name, placeholder, unverified, consent)
	}
	if n := e.ownerCount("SELECT count(*) FROM outbox_events WHERE topic = 'contact.updated' AND workspace_id = $1", w.ws); n == 0 {
		t.Fatal("no contact.updated for the panel")
	}
}

// A signed user keeps the values the company's backend gave; only the
// consent is taken.
func TestWidget_PrechatVerified(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.32", shopOrigin)
	v.start(w.key, map[string]any{"external_id": "u-7", "user_hash": userHash(w.identity, "u-7"), "name": "Марина"})
	rec := v.send("Привет", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	conv := decode[oapi.WidgetMessage](t, rec).ConversationId
	v.introduce(map[string]any{"name": "Кто-то другой", "email": "other@example.test", "consent": map[string]any{"policy_url": policyURL}})
	name, email, _, consent, unverified, _ := e.contactOf(conv)
	if name != "Марина" || email != "" || consent != policyURL || unverified {
		t.Fatalf("verified contact: %q %q %q unverified=%v", name, email, consent, unverified)
	}
	if s := v.start(w.key, map[string]any{"external_id": "u-7", "user_hash": userHash(w.identity, "u-7")}); s.Identity != oapi.WidgetSessionIdentityVerified {
		t.Fatalf("session: %+v", s)
	}
}

func TestWidget_PrechatRejects(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)
	v := e.visitor("198.51.100.33", shopOrigin)
	v.start(w.key, nil)
	for name, body := range map[string]map[string]any{
		"empty":          {},
		"blank":          {"name": "   "},
		"bad email":      {"email": "anna at example"},
		"bad phone":      {"phone": "позвоните"},
		"short phone":    {"phone": "12"},
		"long name":      {"name": strings.Repeat("я", 201)},
		"policy not url": {"consent": map[string]any{"policy_url": "javascript:alert(1)"}},
		"no policy":      {"consent": map[string]any{}},
		"unknown field":  {"name": "Анна", "company": "Acme"},
	} {
		if rec := v.do(http.MethodPost, "/api/v1/widget/contact", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	// No session, a foreign origin.
	stranger := e.visitor("198.51.100.34", shopOrigin)
	wantProblem(t, stranger.do(http.MethodPost, "/api/v1/widget/contact", map[string]any{"name": "Анна"}, bearer("tk_wsess_forged")),
		http.StatusUnauthorized, "widget_session_ended")
	evil := &visitor{c: v.c, origin: "https://evil.example.test", token: v.token}
	wantProblem(t, evil.do(http.MethodPost, "/api/v1/widget/contact", map[string]any{"name": "Анна"}), http.StatusForbidden, "origin_not_allowed")

	// Ten forms a minute per session; refused ones above did not count.
	for range 10 {
		v.introduce(map[string]any{"name": "Анна"})
	}
	wantProblem(t, v.do(http.MethodPost, "/api/v1/widget/contact", map[string]any{"name": "Анна"}), http.StatusTooManyRequests, "too_many_requests")
}

// Nobody takes conversations: the widget is told so and the visitor leaves
// a contact with the offline form, its consent marked as such (W-02).
func TestWidget_Offline(t *testing.T) {
	e := newEnv(t, envOptions{})
	w := e.connectWidget("owner@example.test", "zerno", shopOrigin)

	v := e.visitor("198.51.100.40", shopOrigin)
	if s := v.start(w.key, nil); s.OperatorsOnline {
		t.Fatalf("online with nobody's panel open: %+v", s)
	}
	available := func() bool {
		t.Helper()
		rec := v.do(http.MethodGet, "/api/v1/widget/availability", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("availability: %d %s", rec.Code, rec.Body)
		}
		return decode[oapi.WidgetAvailability](t, rec).OperatorsOnline
	}
	if available() {
		t.Fatal("available with nobody's panel open")
	}

	v.introduce(map[string]any{
		"form": "offline", "email": "anna@example.test", "consent": map[string]any{"policy_url": policyURL},
	})
	rec := v.send("Перезвоните завтра", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	first := decode[oapi.WidgetMessage](t, rec)
	var source string
	e.ownerScan(&source, "SELECT k.attributes->'consent'->>'source' FROM contacts k JOIN conversations c ON c.contact_id = k.id WHERE c.id = $1", first.ConversationId)
	if _, email, _, _, _, _ := e.contactOf(first.ConversationId); email != "anna@example.test" || source != "widget_offline" {
		t.Fatalf("contact %q, consent source %q", email, source)
	}
	wantProblem(t, v.do(http.MethodPost, "/api/v1/widget/contact", map[string]any{"form": "later", "name": "Анна"}), http.StatusBadRequest, "invalid_request")

	// The owner opens the panel: the widget is told on the next check.
	var owner uuid.UUID
	e.ownerScan(&owner, "SELECT user_id FROM memberships WHERE workspace_id = $1", w.ws)
	if err := e.routing.Seen(context.Background(), w.ws, []uuid.UUID{owner}); err != nil {
		t.Fatalf("seen: %v", err)
	}
	if !available() {
		t.Fatal("not available with the owner online")
	}
	if s := v.start(w.key, nil); !s.OperatorsOnline {
		t.Fatalf("session: %+v", s)
	}

	gone := e.visitor("198.51.100.41", shopOrigin)
	wantProblem(t, gone.do(http.MethodGet, "/api/v1/widget/availability", nil, bearer("tk_wsess_nope")), http.StatusUnauthorized, "widget_session_ended")
}
