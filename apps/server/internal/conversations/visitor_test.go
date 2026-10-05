package conversations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
)

// appChannel inserts an app channel of ws behind the channels module's back.
func (f *fixture) appChannel(ws uuid.UUID) uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	f.ownerExec("INSERT INTO channels (id, workspace_id, type, name, status) VALUES ($1, $2, 'app', 'Приложение', 'active')", id, ws)
	return id
}

// report receives an app report and returns the contact it landed on.
func (f *fixture) report(ws, ch uuid.UUID, c conversations.ReportContact) uuid.UUID {
	f.t.Helper()
	got, err := f.svc.ReceiveReport(f.ctx, ws, conversations.AppReport{
		ChannelID: ch, ReportID: uuid.Must(uuid.NewV7()), Body: "Не синхронизируется корзина", Contact: c,
	})
	if err != nil {
		f.t.Fatalf("report: %v", err)
	}
	conv, err := f.svc.Conversation(f.ctx, ws, f.ownerOf(ws), got.ConversationID)
	if err != nil {
		f.t.Fatalf("conversation: %v", err)
	}
	return conv.ContactID
}

func (f *fixture) ownerOf(ws uuid.UUID) uuid.UUID {
	if ws == f.wsB {
		return f.ownerB
	}
	return f.ownerA
}

func (f *fixture) resolve(ws uuid.UUID, v conversations.Visitor) conversations.VisitorContact {
	f.t.Helper()
	got, err := f.svc.ResolveVisitor(f.ctx, ws, v)
	if err != nil {
		f.t.Fatalf("resolve visitor: %v", err)
	}
	return got
}

func (f *fixture) identity(ws, contact uuid.UUID) conversations.Contact {
	f.t.Helper()
	c, err := f.svc.Contact(f.ctx, ws, contact)
	if err != nil {
		f.t.Fatalf("contact: %v", err)
	}
	return c
}

// A verified chat visitor and verified app reports of the same user land
// on one contact, whichever comes first.
func TestResolveVisitor_VerifiedSharesTheReportContact(t *testing.T) {
	f := setup(t)
	ch := f.appChannel(f.wsA)
	reported := f.report(f.wsA, ch, conversations.ReportContact{Verified: true, ExternalID: "user-42", Name: "Анна"})

	got := f.resolve(f.wsA, conversations.Visitor{Verified: true, ExternalID: "user-42", Email: "anna@example.test"})
	if got.ContactID != reported || got.Created || got.Identity != conversations.IdentityVerified {
		t.Fatalf("visitor %+v, report contact %s", got, reported)
	}
	c := f.identity(f.wsA, reported)
	if c.Identity != conversations.IdentityVerified || c.Name != "Анна" || c.Email == nil || *c.Email != "anna@example.test" {
		t.Fatalf("contact: %+v", c)
	}

	// The other way round: the chat first, then the report.
	chat := f.resolve(f.wsA, conversations.Visitor{Verified: true, ExternalID: "user-43"})
	if !chat.Created {
		t.Fatalf("first visit: %+v", chat)
	}
	if got := f.report(f.wsA, ch, conversations.ReportContact{Verified: true, ExternalID: "user-43"}); got != chat.ContactID {
		t.Fatalf("report went to %s, not the chat contact %s", got, chat.ContactID)
	}
	// An anonymous session that logs in moves to the verified contact.
	anon := f.resolve(f.wsA, conversations.Visitor{})
	if got := f.resolve(f.wsA, conversations.Visitor{Current: anon.ContactID, Verified: true, ExternalID: "user-42"}); got.ContactID != reported {
		t.Fatalf("login: %+v", got)
	}
}

// An unverified claim never leads to an existing contact: not the one with
// that external id, not another one claiming the same email.
func TestResolveVisitor_UnverifiedNeverMerges(t *testing.T) {
	f := setup(t)
	ch := f.appChannel(f.wsA)
	victim := f.report(f.wsA, ch, conversations.ReportContact{Verified: true, ExternalID: "user-42", Email: "anna@example.test"})

	claim := conversations.Visitor{ExternalID: "user-42", Email: "anna@example.test", Name: "Анна"}
	first := f.resolve(f.wsA, claim)
	second := f.resolve(f.wsA, claim)
	if first.ContactID == victim || second.ContactID == victim || first.ContactID == second.ContactID {
		t.Fatalf("victim %s, claims %s and %s", victim, first.ContactID, second.ContactID)
	}
	if !first.Created || first.Identity != conversations.IdentityUnverified {
		t.Fatalf("claim: %+v", first)
	}
	c := f.identity(f.wsA, first.ContactID)
	if c.ExternalID != nil || c.Identity != conversations.IdentityUnverified || c.Email == nil || c.Name != "Анна" {
		t.Fatalf("claimed contact: %+v", c)
	}
	// An unverified report is a claim too, and the visitor's id is not stored.
	if got := f.report(f.wsA, ch, conversations.ReportContact{ExternalID: "user-42"}); got == victim ||
		f.identity(f.wsA, got).Identity != conversations.IdentityUnverified {
		t.Fatalf("unverified report went to %s", got)
	}
	// Nothing claimed: an anonymous contact, not marked.
	if anon := f.resolve(f.wsA, conversations.Visitor{}); anon.Identity != conversations.IdentityNone ||
		f.identity(f.wsA, anon.ContactID).Identity != conversations.IdentityNone {
		t.Fatalf("anonymous: %+v", anon)
	}
}

// identify after login with a forged id keeps the anonymous session's own
// contact; a verified session does not carry over to a claimed other id.
func TestResolveVisitor_SessionUpgrade(t *testing.T) {
	f := setup(t)
	victim := f.resolve(f.wsA, conversations.Visitor{Verified: true, ExternalID: "user-42", Name: "Анна"})

	anon := f.resolve(f.wsA, conversations.Visitor{})
	forged := f.resolve(f.wsA, conversations.Visitor{Current: anon.ContactID, ExternalID: "user-42", Email: "anna@example.test"})
	if forged.ContactID != anon.ContactID || forged.Created || forged.Identity != conversations.IdentityUnverified {
		t.Fatalf("forged identify: %+v, anonymous %s", forged, anon.ContactID)
	}
	if c := f.identity(f.wsA, anon.ContactID); c.Identity != conversations.IdentityUnverified || c.Email == nil {
		t.Fatalf("anonymous after claim: %+v", c)
	}

	// The verified session resumes; a typed email is not written onto it.
	resumed := f.resolve(f.wsA, conversations.Visitor{Current: victim.ContactID, Email: "mallory@example.test"})
	if resumed.ContactID != victim.ContactID || resumed.Identity != conversations.IdentityVerified {
		t.Fatalf("resume: %+v", resumed)
	}
	if c := f.identity(f.wsA, victim.ContactID); c.Email != nil {
		t.Fatalf("a claim wrote onto a verified contact: %+v", c)
	}
	// The same browser now claims another (or even the same) id without a
	// valid signature: a new contact, not the verified history.
	for _, id := range []string{"user-43", "user-42"} {
		switched := f.resolve(f.wsA, conversations.Visitor{Current: victim.ContactID, ExternalID: id})
		if switched.ContactID == victim.ContactID || !switched.Created || switched.Identity != conversations.IdentityUnverified {
			t.Fatalf("claim %s from a verified session: %+v", id, switched)
		}
	}
}

// External ids are one namespace per workspace, never across workspaces;
// a session contact of another workspace is not continued.
func TestResolveVisitor_Isolation(t *testing.T) {
	f := setup(t)
	a := f.resolve(f.wsA, conversations.Visitor{Verified: true, ExternalID: "user-42"})
	b := f.resolve(f.wsB, conversations.Visitor{Verified: true, ExternalID: "user-42"})
	if a.ContactID == b.ContactID || !b.Created {
		t.Fatalf("workspaces share a contact: %+v %+v", a, b)
	}
	other := f.resolve(f.wsB, conversations.Visitor{Current: a.ContactID})
	if other.ContactID == a.ContactID || !other.Created {
		t.Fatalf("continued a contact of another workspace: %+v", other)
	}
	if _, err := f.svc.Contact(f.ctx, f.wsB, a.ContactID); !errors.Is(err, conversations.ErrNotFound) {
		t.Fatalf("contact of A from B: %v", err)
	}
}

func TestResolveVisitor_Rejects(t *testing.T) {
	f := setup(t)
	for name, v := range map[string]conversations.Visitor{
		"verified without id": {Verified: true},
		"long id":             {Verified: true, ExternalID: strings.Repeat("x", 256)},
		"invalid utf-8":       {ExternalID: "\xff"},
		"long email":          {Email: strings.Repeat("a", 250) + "@x.test"},
	} {
		if _, err := f.svc.ResolveVisitor(f.ctx, f.wsA, v); !errors.Is(err, conversations.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A malformed email is dropped, not an error.
	got := f.resolve(f.wsA, conversations.Visitor{Email: "not an address"})
	if c := f.identity(f.wsA, got.ContactID); c.Email != nil {
		t.Fatalf("kept a malformed email: %+v", c)
	}
}
