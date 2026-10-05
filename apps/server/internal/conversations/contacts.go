package conversations

import (
	"context"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Contact is a customer. Email and phone are personal data: they go to API
// responses for authorised members only and never to logs or events.
type Contact struct {
	ID   uuid.UUID
	Name string
	// NamePlaceholder: Name is the guest name the server gave a contact
	// that came without one («Гость 4821»).
	NamePlaceholder bool
	Email           *string
	Phone           *string
	Locale          *string
	ExternalID      *string
	// Identity is IdentityVerified, IdentityUnverified or IdentityNone.
	Identity  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Identity of a contact (docs/02 §13.2).
const (
	// IdentityVerified: the company's backend signed the external id.
	IdentityVerified = "verified"
	// IdentityUnverified: the visitor or app user claimed an id, a name or
	// an email without a valid signature; the values are only typed ones.
	IdentityUnverified = "unverified"
	// IdentityNone: nothing was claimed.
	IdentityNone = "none"
)

// ContactInput describes a new contact. Empty optional fields stay unset.
type ContactInput struct {
	Name   string
	Email  string
	Phone  string
	Locale string
}

// ContactPatch changes a contact. A nil field is left as is; an empty
// email, phone or locale clears it.
type ContactPatch struct {
	Name   *string
	Email  *string
	Phone  *string
	Locale *string
}

var phoneRe = regexp.MustCompile(`^\+?[0-9][0-9 ()-]{1,30}$`)

// CreateContact adds a contact to the workspace.
func (s *Service) CreateContact(ctx context.Context, workspaceID uuid.UUID, in ContactInput) (Contact, error) {
	fields, err := contactFields(in.Name, optional(in.Email), optional(in.Phone), optional(in.Locale))
	if err != nil {
		return Contact{}, err
	}
	id, err := newID()
	if err != nil {
		return Contact{}, err
	}
	var c store.Contact
	err = s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		c, err = q.CreateContact(ctx, store.CreateContactParams{
			ID: id, WorkspaceID: workspaceID, Name: fields.Name,
			Email: fields.Email, Phone: fields.Phone, Locale: fields.Locale,
		})
		if err != nil {
			return err
		}
		return outbox.Add(ctx, tx, workspaceID, "contact.created", map[string]any{"contact_id": id})
	})
	if err != nil {
		return Contact{}, wrap(err, "create contact")
	}
	return contactFrom(c), nil
}

// Contact returns a contact of the workspace.
func (s *Service) Contact(ctx context.Context, workspaceID, id uuid.UUID) (Contact, error) {
	var c store.Contact
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		c, err = q.GetContact(ctx, store.GetContactParams{WorkspaceID: workspaceID, ID: id})
		return err
	})
	if err != nil {
		return Contact{}, wrap(err, "get contact")
	}
	return contactFrom(c), nil
}

// Contacts lists contacts, newest first. cursor is NextCursor of the
// previous page or empty.
func (s *Service) Contacts(ctx context.Context, workspaceID uuid.UUID, cursor string, limit int) (Page[Contact], error) {
	before, err := decodeIDCursor(cursor)
	if err != nil {
		return Page[Contact]{}, err
	}
	lim := pageSize(limit)
	var rows []store.Contact
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		rows, err = q.ListContacts(ctx, store.ListContactsParams{WorkspaceID: workspaceID, BeforeID: before, Lim: lim + 1})
		return err
	})
	if err != nil {
		return Page[Contact]{}, wrap(err, "list contacts")
	}
	return paginate(rows, lim, contactFrom, func(c store.Contact) string { return encodeIDCursor(c.ID) }), nil
}

// UpdateContact applies a patch.
func (s *Service) UpdateContact(ctx context.Context, workspaceID, id uuid.UUID, p ContactPatch) (Contact, error) {
	var c store.Contact
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		cur, err := q.GetContact(ctx, store.GetContactParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return err
		}
		name, email, phone, locale := cur.Name, cur.Email, cur.Phone, cur.Locale
		if p.Name != nil {
			name = *p.Name
		}
		if p.Email != nil {
			email = optional(*p.Email)
		}
		if p.Phone != nil {
			phone = optional(*p.Phone)
		}
		if p.Locale != nil {
			locale = optional(*p.Locale)
		}
		fields, err := contactFields(name, email, phone, locale)
		if err != nil {
			return err
		}
		c, err = q.UpdateContact(ctx, store.UpdateContactParams{
			WorkspaceID: workspaceID, ID: id, Name: fields.Name,
			Email: fields.Email, Phone: fields.Phone, Locale: fields.Locale,
		})
		if err != nil {
			return err
		}
		return outbox.Add(ctx, tx, workspaceID, "contact.updated", map[string]any{"contact_id": id})
	})
	if err != nil {
		return Contact{}, wrap(err, "update contact")
	}
	return contactFrom(c), nil
}

type contactValues struct {
	Name                 string
	Email, Phone, Locale *string
}

// contactFields normalises and validates contact data. Error messages name
// the field, never the value: it is personal data.
func contactFields(name string, email, phone, locale *string) (contactValues, error) {
	v := contactValues{Name: strings.TrimSpace(name)}
	if utf8.RuneCountInString(v.Name) > 200 {
		return v, fmt.Errorf("%w: name must be at most 200 characters", ErrInvalid)
	}
	if email != nil {
		e := strings.TrimSpace(*email)
		addr, err := mail.ParseAddress(e)
		if err != nil || addr.Address != e || len(e) > 254 {
			return v, fmt.Errorf("%w: email is not a valid address", ErrInvalid)
		}
		v.Email = &e
	}
	if phone != nil {
		ph := strings.TrimSpace(*phone)
		if !phoneRe.MatchString(ph) {
			return v, fmt.Errorf("%w: phone must be 3-32 digits, spaces, brackets or hyphens", ErrInvalid)
		}
		v.Phone = &ph
	}
	if locale != nil {
		if *locale != "ru" && *locale != "en" {
			return v, fmt.Errorf("%w: locale must be ru or en", ErrInvalid)
		}
		v.Locale = locale
	}
	return v, nil
}

// optional maps "" to nil.
func optional(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func contactFrom(c store.Contact) Contact {
	identity := IdentityNone
	switch {
	case c.ExternalID != nil:
		identity = IdentityVerified
	case c.IdentityUnverified:
		identity = IdentityUnverified
	}
	return Contact{
		ID: c.ID, Name: c.Name, NamePlaceholder: c.NamePlaceholder, Email: c.Email, Phone: c.Phone, Locale: c.Locale,
		ExternalID: c.ExternalID, Identity: identity, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
