package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/platform/rdbtest"
)

func TestLinkToken_RoundTrip(t *testing.T) {
	owner := uuid.New()
	token, hash, err := newLinkToken(owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 || strings.ContainsAny(token, "+/=") {
		t.Fatalf("token must be 64 base64url characters: %q", token)
	}
	got, gotHash, err := parseLinkToken(token)
	if err != nil || got != owner || !bytes.Equal(gotHash, hash) {
		t.Fatalf("parse: %s %x %v", got, gotHash, err)
	}
	other, _, _ := newLinkToken(owner)
	if other == token {
		t.Fatal("tokens must be random")
	}
}

func TestLinkToken_RejectsMalformed(t *testing.T) {
	nilOwner, _, _ := newLinkToken(uuid.Nil)
	for _, tok := range []string{"", "x", "%%%", strings.Repeat("A", 63), strings.Repeat("A", 65), nilOwner} {
		if _, _, err := parseLinkToken(tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q: want ErrInvalidToken, got %v", tok, err)
		}
	}
}

func TestUserTokens_SingleUseAndReplaced(t *testing.T) {
	rdb, prefix := rdbtest.New(t)
	ctx := context.Background()
	tokens := userTokens{rdb: rdb, prefix: prefix, purpose: "test", ttl: time.Minute}
	user := uuid.New()

	first, err := tokens.issue(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tokens.issue(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.consume(ctx, first); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a replaced token must not work: %v", err)
	}
	got, err := tokens.consume(ctx, second)
	if err != nil || got != user {
		t.Fatalf("consume: %s %v", got, err)
	}
	if _, err := tokens.consume(ctx, second); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a used token must not work again: %v", err)
	}
	// A token of another user's key does not match.
	forged, _, _ := newLinkToken(user)
	if _, err := tokens.consume(ctx, forged); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged token: %v", err)
	}
}

func TestRenderMail(t *testing.T) {
	m, err := renderMail("en", "invitation", "a@example.com", mailData{
		Link: "http://panel.test/invite#tok", Workspace: "Acme\r\nBcc: x@evil.test", Inviter: "Olga",
		Role: roleName("en", "operator"), Days: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Fatalf("subject must be one line: %q", m.Subject)
	}
	if !strings.Contains(m.Text, "Olga invites you") || !strings.Contains(m.Text, "Role: operator") ||
		!strings.Contains(m.Text, "http://panel.test/invite#tok") || !strings.Contains(m.Text, "7 days") {
		t.Fatalf("text:\n%s", m.Text)
	}
	// Unknown locales fall back to Russian; every kind renders in both.
	for _, kind := range []string{"invitation", "reset", "verify"} {
		for _, locale := range []string{"ru", "en", "de"} {
			m, err := renderMail(locale, kind, "a@example.com", mailData{Link: "L", Name: "N", Workspace: "W", Days: 7, Hours: 48, Minutes: 60})
			if err != nil || m.Subject == "" || !strings.Contains(m.Text, "L") {
				t.Errorf("%s/%s: %+v %v", kind, locale, m, err)
			}
		}
	}
	ru, _ := renderMail("de", "reset", "a@example.com", mailData{Link: "L", Name: "N", Minutes: 60})
	if !strings.Contains(ru.Text, "60 минут") {
		t.Fatalf("want Russian fallback:\n%s", ru.Text)
	}
}

func TestLinkBuilder(t *testing.T) {
	b, err := newLinkBuilder("https://app.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.link(resetPath, "abc"); got != "https://app.example.com/reset-password#abc" {
		t.Fatalf("link: %s", got)
	}
	for _, bad := range []string{"", "app.example.com", "ftp://x", "https://x/?a=1"} {
		if _, err := newLinkBuilder(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
