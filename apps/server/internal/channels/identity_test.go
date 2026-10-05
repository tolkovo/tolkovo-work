package channels

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/channelstest"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// sign is what the company's backend computes (docs/02 §13.2).
func sign(secret, externalID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(externalID))
	return hex.EncodeToString(mac.Sum(nil))
}

const testSecret = "tk_identity_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"

func TestSignatureMatches(t *testing.T) {
	good := sign(testSecret, "user-42")
	flipped := []byte(good) // still hex, one digit different
	if flipped[10] == '0' {
		flipped[10] = '1'
	} else {
		flipped[10] = '0'
	}
	for _, tc := range []struct {
		name, secret, id, sig string
		want                  bool
	}{
		{"valid", testSecret, "user-42", good, true},
		{"upper-case hex", testSecret, "user-42", strings.ToUpper(good), true},
		{"wrong secret", testSecret + "x", "user-42", good, false},
		{"other user", testSecret, "user-43", good, false},
		{"tampered signature", testSecret, "user-42", string(flipped), false},
		{"truncated", testSecret, "user-42", good[:62], false},
		{"extended", testSecret, "user-42", good + "00", false},
		{"not hex", testSecret, "user-42", strings.Repeat("z", 64), false},
		{"empty signature", testSecret, "user-42", "", false},
		{"no id", testSecret, "", sign(testSecret, ""), false},
		{"no secret", "", "user-42", sign("", "user-42"), false},
	} {
		if got := signatureMatches(tc.secret, tc.id, tc.sig); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIdentityClaim_Validate(t *testing.T) {
	good := sign(testSecret, "user-42")
	for _, tc := range []struct {
		name  string
		claim IdentityClaim
		ok    bool
	}{
		{"nothing", IdentityClaim{}, true},
		{"user_hash", IdentityClaim{ExternalID: "user-42", UserHash: good}, true},
		{"identity_token", IdentityClaim{ExternalID: "user-42", IdentityToken: good}, true},
		{"both, equal", IdentityClaim{ExternalID: "user-42", UserHash: good, IdentityToken: good}, true},
		{"id without signature", IdentityClaim{ExternalID: "user-42"}, true},
		{"longest id", IdentityClaim{ExternalID: strings.Repeat("x", MaxExternalIDLength), UserHash: good}, true},
		{"malformed signature", IdentityClaim{ExternalID: "user-42", UserHash: "abc"}, true},
		{"both, different", IdentityClaim{ExternalID: "user-42", UserHash: good, IdentityToken: sign(testSecret, "x")}, false},
		{"signature without id", IdentityClaim{UserHash: good}, false},
		{"oversize id", IdentityClaim{ExternalID: strings.Repeat("x", MaxExternalIDLength+1), UserHash: good}, false},
		{"oversize signature", IdentityClaim{ExternalID: "user-42", UserHash: strings.Repeat("a", maxSignatureLength+1)}, false},
		{"invalid utf-8 id", IdentityClaim{ExternalID: "\xff", UserHash: good}, false},
	} {
		err := tc.claim.validate()
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrInvalid)) {
			t.Errorf("%s: %v", tc.name, err)
		}
		// Errors never carry the id or the signature.
		if err != nil && (strings.Contains(err.Error(), good) || strings.Contains(err.Error(), "user-42")) {
			t.Errorf("%s: the error shows the claim: %v", tc.name, err)
		}
	}
}

// The identity secret of the app channel verifies the widget's user_hash
// and the reports' identity_token alike; rotation and other workspaces
// stop it.
func TestVerifyClaim(t *testing.T) {
	f := setup(t)
	app, err := f.svc.ConnectApp(f.ctx, f.wsA, f.ownerA, "Зерно")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ch, secret := app.Channel.ID, app.IdentitySecret.Reveal()
	verify := func(c IdentityClaim) Identity {
		t.Helper()
		got, err := f.svc.VerifyClaim(f.ctx, f.wsA, ch, c)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		return got
	}
	if got := verify(IdentityClaim{ExternalID: "user-42", UserHash: sign(secret, "user-42")}); !got.Verified || got.ExternalID != "user-42" {
		t.Fatalf("user_hash: %+v", got)
	}
	if ok, err := f.svc.VerifyIdentity(f.ctx, f.wsA, ch, "user-42", sign(secret, "user-42")); err != nil || !ok {
		t.Fatalf("identity_token: %v %v", ok, err)
	}
	if got := verify(IdentityClaim{ExternalID: "user-43", UserHash: sign(secret, "user-42")}); got.Verified || got.ExternalID != "" {
		t.Fatalf("forged: %+v", got)
	}
	if got := verify(IdentityClaim{ExternalID: "user-42"}); got.Verified {
		t.Fatalf("no signature: %+v", got)
	}

	// Another workspace does not know the channel.
	if _, err := f.svc.VerifyClaim(f.ctx, f.wsB, ch, IdentityClaim{ExternalID: "user-42", UserHash: sign(secret, "user-42")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace: %v", err)
	}
	// After rotation the old signature is a claim.
	fresh, err := f.svc.RotateIdentitySecret(f.ctx, f.wsA, ch)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if got := verify(IdentityClaim{ExternalID: "user-42", UserHash: sign(secret, "user-42")}); got.Verified {
		t.Fatal("the old secret still verifies")
	}
	if got := verify(IdentityClaim{ExternalID: "user-42", UserHash: sign(fresh.Reveal(), "user-42")}); !got.Verified {
		t.Fatal("the new secret does not verify")
	}

	// End to end: a verified identity leads the chat visitor to the
	// contact the verified report of that user landed on.
	rep, err := f.inbox.ReceiveReport(f.ctx, f.wsA, conversations.AppReport{
		ChannelID: ch, ReportID: uuid.Must(uuid.NewV7()), Body: "Не синхронизируется корзина",
		Contact: conversations.ReportContact{Verified: true, ExternalID: "user-42"},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	conv, err := f.inbox.Conversation(f.ctx, f.wsA, f.ownerA, rep.ConversationID)
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	id := verify(IdentityClaim{ExternalID: "user-42", UserHash: sign(fresh.Reveal(), "user-42")})
	visitor, err := f.inbox.ResolveVisitor(f.ctx, f.wsA, conversations.Visitor{Verified: id.Verified, ExternalID: id.ExternalID})
	if err != nil || visitor.ContactID != conv.ContactID {
		t.Fatalf("visitor %+v (%v), report contact %s", visitor, err, conv.ContactID)
	}
}

// Channels without an identity secret verify nothing.
func TestVerifyClaim_BotChannel(t *testing.T) {
	f := setup(t)
	bot, err := f.svc.ConnectBot(f.ctx, f.wsA, f.ownerA, channelstest.BotToken, "")
	if err != nil {
		t.Fatalf("connect bot: %v", err)
	}
	got, err := f.svc.VerifyClaim(f.ctx, f.wsA, bot.ID, IdentityClaim{ExternalID: "user-42", UserHash: sign("", "user-42")})
	if err != nil || got.Verified {
		t.Fatalf("bot channel: %+v %v", got, err)
	}
}
