package channels

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

func TestNormalizeOrigins(t *testing.T) {
	got, err := NormalizeOrigins([]string{
		" HTTPS://Shop.Example.com:443 ", "http://localhost:5173", "http://localhost:80", "https://*.example.com",
		"app://zerno", "file://", "NULL", "https://shop.example.com", "https://[::1]:8443",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := []string{
		"https://shop.example.com", "http://localhost:5173", "http://localhost", "https://*.example.com",
		"app://zerno", "file://", "null", "https://[::1]:8443",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, bad := range []string{
		"*", "", "https://*", "https://*.com", "app://*.zerno", "https://shop.example.com/", "https://shop.example.com/path",
		"shop.example.com", "wss://shop.example.com", "file:///etc", "https://user@shop.example.com", "https://shop.example.com:99999",
		"https://shop.example.com:abc", "https://sh op.example.com", "https://shop.*.example.com",
	} {
		if _, err := NormalizeOrigins([]string{bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
	if _, err := NormalizeOrigins(make([]string, MaxAllowedOrigins+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("more than %d origins accepted", MaxAllowedOrigins)
	}
}

func TestOriginAllowed(t *testing.T) {
	allowed, err := NormalizeOrigins([]string{"https://shop.example.com", "https://*.example.org", "app://zerno", "null"})
	if err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]bool{
		"https://shop.example.com":      true,
		"https://shop.example.com:443":  true,
		"HTTPS://SHOP.EXAMPLE.COM":      true,
		"http://shop.example.com":       false,
		"https://shop.example.com:444":  false,
		"https://evil.shop.example.com": false,
		"https://m.example.org":         true,
		"https://a.b.example.org":       true,
		"https://example.org":           false,
		"https://evilexample.org":       false,
		"app://zerno":                   true,
		"app://other":                   false,
		"null":                          true,
		"file://":                       false,
		"":                              false,
		"https://shop.example.com/x":    false,
	} {
		if got := OriginAllowed(allowed, origin); got != want {
			t.Errorf("%q: got %v, want %v", origin, got, want)
		}
	}
	if OriginAllowed(nil, "https://shop.example.com") {
		t.Error("an empty list allows a page")
	}
}

func TestWidgetKey(t *testing.T) {
	ws, ch := uuid.New(), uuid.New()
	key := WidgetKey(ws, ch)
	gotWS, gotCh, ok := parseWidgetKey(key)
	if !ok || gotWS != ws || gotCh != ch {
		t.Fatalf("round trip: %v %v %v", gotWS, gotCh, ok)
	}
	for _, bad := range []string{"", "tk_widget_", key + "A", "tk_ingest_" + key[len("tk_widget_"):], WidgetKey(uuid.Nil, ch)} {
		if _, _, ok := parseWidgetKey(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

// visitorSignals records the typing shown to visitors.
type visitorSignals struct {
	mu    sync.Mutex
	typed []uuid.UUID
}

func (v *visitorSignals) VisitorTyping(_ context.Context, _, contactID uuid.UUID) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.typed = append(v.typed, contactID)
	return nil
}

func (v *visitorSignals) snapshot() []uuid.UUID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.typed)
}

// A reply to a visitor is pushed in its transaction and marked sent by the
// job; the operator's typing reaches the visitor at most every typingEvery.
func TestSend_Widget(t *testing.T) {
	f := sendSetup(t)
	signals := &visitorSignals{}
	f.svc.opts.Visitors = signals
	w, err := f.svc.ConnectWidget(f.ctx, f.wsA, f.ownerA, "Чат", []string{"https://shop.example.test"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if w.Channel.Details.WidgetKey == "" || !strings.HasPrefix(w.IdentitySecret.Reveal(), "tk_identity_") {
		t.Fatalf("created: %+v", w.Channel.Details)
	}
	contact, err := f.inbox.ResolveVisitor(f.ctx, f.wsA, conversations.Visitor{Name: "Гость"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.ReceiveVisitorMessage(f.ctx, f.wsA, VisitorMessage{
		ChannelID: w.Channel.ID, ContactID: contact.ContactID, Key: "k1", Body: "Здравствуйте",
	})
	if err != nil || !got.Stored || !got.CreatedConversation {
		t.Fatalf("receive: %+v %v", got, err)
	}
	if again, err := f.svc.ReceiveVisitorMessage(f.ctx, f.wsA, VisitorMessage{
		ChannelID: w.Channel.ID, ContactID: contact.ContactID, Key: "k1", Body: "Здравствуйте",
	}); err != nil || again.Stored {
		t.Fatalf("a retry was stored again: %+v %v", again, err)
	}
	if l := f.list(f.wsA); l[0].LastInboundAt == nil {
		t.Fatalf("last inbound not marked: %+v", l[0])
	}

	m := f.reply(got.ConversationID, "Слушаю")
	if n := f.count("SELECT count(*) FROM outbox_events WHERE topic = 'widget.message' AND payload->>'message_id' = $1 AND payload->>'contact_id' = $2",
		m.ID.String(), contact.ContactID.String()); n != 1 {
		t.Fatalf("widget.message events: %d", n)
	}
	if err := f.deliver(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if s := status(f.message(got.ConversationID, m.ID)); s != conversations.DeliverySent {
		t.Fatalf("status %q", s)
	}
	if len(f.bots.SnapshotSent()) != 0 {
		t.Fatal("a widget reply went to Telegram")
	}

	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	go func() { f.svc.runActions(ctx) }()
	eventually(t, "typing", func() bool {
		f.svc.ReplyTyping(f.ctx, f.wsA, got.ConversationID)
		return len(signals.snapshot()) > 0
	})
	f.svc.ReplyTyping(f.ctx, f.wsA, got.ConversationID)
	f.svc.ConversationRead(f.ctx, f.wsA, got.ConversationID) // the widget reports reading itself
	time.Sleep(200 * time.Millisecond)
	if typed := signals.snapshot(); len(typed) != 1 || typed[0] != contact.ContactID {
		t.Fatalf("typing at most every %s: %v", typingEvery, typed)
	}

	// Origins are the widget's only: another type is not found.
	if _, err := f.svc.SetWidgetOrigins(f.ctx, f.wsB, w.Channel.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another workspace's widget: %v", err)
	}
	if wc, err := f.svc.WidgetByKey(f.ctx, w.Channel.Details.WidgetKey); err != nil || wc.ChannelID != w.Channel.ID || !wc.Allows("https://shop.example.test") {
		t.Fatalf("by key: %+v %v", wc, err)
	}
	if _, err := f.svc.WidgetByKey(f.ctx, WidgetKey(f.wsB, w.Channel.ID)); !errors.Is(err, ErrInvalidWidgetKey) {
		t.Fatalf("a forged workspace: %v", err)
	}
}
