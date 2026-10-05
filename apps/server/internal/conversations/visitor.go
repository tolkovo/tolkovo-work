package conversations

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Contacts of site visitors (docs/02 §13.2, docs/06 W-04). A verified
// identity leads to the contact with that external id — one contact per
// external id in the workspace, whichever channel it came through, so the
// chat and the app reports of one person share a customer card. External
// ids are therefore one namespace per workspace: a company whose site and
// app number users differently keeps them in separate workspaces or
// prefixes the ids. Anything not verified is a claim: it never finds an
// existing contact, so a forged id cannot read someone else's history.

// Visitor is who a site visitor is, as the widget session knows it.
type Visitor struct {
	// Current is the contact of the visitor's session, taken from the
	// session token the server signed; uuid.Nil for a new session.
	Current uuid.UUID
	// Verified: the company's backend signed ExternalID
	// (channels.Service.VerifyClaim). Without it ExternalID is only a claim
	// and is not stored.
	Verified   bool
	ExternalID string
	// Email and Name are what the site passed; they fill what a contact
	// lacks and never overwrite what an operator entered.
	Email string
	Name  string
}

// VisitorContact is the contact a visitor's session continues with.
type VisitorContact struct {
	ContactID uuid.UUID
	// Identity is IdentityVerified, IdentityUnverified or IdentityNone.
	Identity string
	// Created: a new contact, not the current or a found one.
	Created bool
}

// ResolveVisitor returns the contact a widget session continues with:
//   - verified: the contact with that external id in the workspace, the one
//     app reports of the same user land on, created if there is none;
//   - not verified, with a session: the session's contact, unless it is a
//     verified one and the visitor now claims an id — then a new contact,
//     so another user's history is not shown under a claim;
//   - not verified, without a session: a new contact.
//
// An unverified claim (id, email, name) marks the contact unverified; the
// claimed id is never stored. A verified identity does not move the
// conversations of the session's anonymous contact.
func (s *Service) ResolveVisitor(ctx context.Context, workspaceID uuid.UUID, v Visitor) (VisitorContact, error) {
	if err := v.validate(); err != nil {
		return VisitorContact{}, err
	}
	name, email := v.claimed()
	var out VisitorContact
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		if v.Verified {
			id, created, err := s.verifiedContact(ctx, q, tx, workspaceID, v.ExternalID, name, email)
			out = VisitorContact{ContactID: id, Identity: IdentityVerified, Created: created}
			return err
		}
		claimed := v.ExternalID != "" || name != "" || email != nil
		if v.Current != uuid.Nil {
			cur, err := q.GetContact(ctx, store.GetContactParams{WorkspaceID: workspaceID, ID: v.Current})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				// Deleted or not of this workspace: start afresh.
			case err != nil:
				return err
			case cur.ExternalID != nil && v.ExternalID == "":
				// A verified session goes on; a typed email or name is not
				// written onto a verified contact.
				out = VisitorContact{ContactID: cur.ID, Identity: IdentityVerified}
				return nil
			case cur.ExternalID == nil:
				out = VisitorContact{ContactID: cur.ID, Identity: contactFrom(cur).Identity}
				if !claimed {
					return nil
				}
				out.Identity = IdentityUnverified
				if err := q.MarkContactUnverified(ctx, store.MarkContactUnverifiedParams{
					WorkspaceID: workspaceID, ID: cur.ID, Name: name, Email: email,
				}); err != nil {
					return err
				}
				return outbox.Add(ctx, tx, workspaceID, "contact.updated", map[string]any{"contact_id": cur.ID})
			}
		}
		id, err := s.newContact(ctx, q, tx, workspaceID, store.CreateContactParams{
			Name: name, Email: email, IdentityUnverified: claimed,
		})
		out = VisitorContact{ContactID: id, Identity: IdentityNone, Created: true}
		if claimed {
			out.Identity = IdentityUnverified
		}
		return err
	})
	if err != nil {
		return VisitorContact{}, wrap(err, "resolve visitor")
	}
	return out, nil
}

// validate bounds the visitor; messages name the field, never the value.
func (v Visitor) validate() error {
	switch {
	case len(v.ExternalID) > 255 || !utf8.ValidString(v.ExternalID):
		return fmt.Errorf("%w: external_id must be valid text of at most 255 bytes", ErrInvalid)
	case v.Verified && v.ExternalID == "":
		return fmt.Errorf("%w: a verified visitor needs an external id", ErrInvalid)
	case len(v.Email) > 254 || len(v.Name) > 4*200:
		return fmt.Errorf("%w: visitor email or name is too long", ErrInvalid)
	}
	return nil
}

// claimed returns the name and email worth keeping: the name trimmed to
// the column, the email only when it is an address.
func (v Visitor) claimed() (string, *string) {
	name := truncate(strings.TrimSpace(strings.ToValidUTF8(v.Name, "")), 200)
	e := strings.TrimSpace(v.Email)
	if addr, err := mail.ParseAddress(e); err != nil || addr.Address != e || len(e) < 3 {
		return name, nil
	}
	return name, &e
}

// verifiedContact returns the contact with a verified external id, the
// same one for every channel of the workspace (site widget, app reports),
// filling the name and email it lacks; or creates it. The first requests
// of one user are serialised, so they find one contact instead of two.
func (s *Service) verifiedContact(
	ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, externalID, name string, email *string,
) (id uuid.UUID, created bool, err error) {
	if err := q.LockExternalID(ctx, store.LockExternalIDParams{WorkspaceID: workspaceID.String(), ExternalID: externalID}); err != nil {
		return uuid.Nil, false, err
	}
	found, err := q.ContactByExternalID(ctx, store.ContactByExternalIDParams{WorkspaceID: workspaceID, ExternalID: &externalID})
	if err == nil {
		return found.ID, false, q.FillContact(ctx, store.FillContactParams{WorkspaceID: workspaceID, ID: found.ID, Name: name, Email: email})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, err
	}
	id, err = s.newContact(ctx, q, tx, workspaceID, store.CreateContactParams{Name: name, Email: email, ExternalID: &externalID})
	return id, err == nil, err
}

// newContact creates a contact of the workspace with a new id and
// announces it.
func (s *Service) newContact(ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, p store.CreateContactParams) (uuid.UUID, error) {
	id, err := newID()
	if err != nil {
		return uuid.Nil, err
	}
	p.ID, p.WorkspaceID = id, workspaceID
	if _, err := q.CreateContact(ctx, p); err != nil {
		return uuid.Nil, err
	}
	return id, outbox.Add(ctx, tx, workspaceID, "contact.created", map[string]any{"contact_id": id})
}
