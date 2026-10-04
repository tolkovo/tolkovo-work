package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// AppReport is a problem report from a company app (docs/02 §11.6) that
// the ingest module checked: one report is one new conversation.
type AppReport struct {
	ChannelID uuid.UUID
	// ReportID names the thread and the message: a retried job finds the
	// conversation it created instead of making a second one.
	ReportID uuid.UUID
	Body     string
	Contact  ReportContact
	// Info is stored with the message (Message.Report); the panel shows it.
	Info  ReportInfo
	Files []ReceivedFile
}

// ReportInfo is what the panel shows about a report: the app, its version
// and OS, whether the contact is verified, how each file was checked. No
// contact data: that is on the contact.
type ReportInfo struct {
	ReportID        uuid.UUID `json:"report_id"`
	ChannelID       uuid.UUID `json:"channel_id"`
	App             ReportApp `json:"app"`
	ContactVerified bool      `json:"contact_verified"`
	// Files: the check of each file in the order of the attachments
	// (FileOK…).
	Files []string `json:"files,omitempty"`
}

// ReportApp is what the app said about itself.
type ReportApp struct {
	Name      string            `json:"name,omitempty"`
	Version   string            `json:"version,omitempty"`
	OS        string            `json:"os,omitempty"`
	OSVersion string            `json:"os_version,omitempty"`
	Locale    string            `json:"locale,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

// The check of a report file (ReportInfo.Files); every one but FileOK comes
// with a rejected attachment.
const (
	FileOK           = "ok"
	FileTypeMismatch = "type_mismatch"
	FileInfected     = "infected"
	FileNotScanned   = "not_scanned"
	FileMissing      = "missing"
)

// ReportContact is who the app user says they are.
type ReportContact struct {
	// Verified: the company's backend signed ExternalID. Only then is the
	// report added to the contact with that external id; otherwise a new
	// contact is created and never merged, so a forged id cannot put files
	// into someone else's history.
	Verified   bool
	ExternalID string
	Name       string
	Email      string
}

// ReceiveReport stores a report as a new conversation in the queue, with
// the report as the customer's first message and the files attached.
// Receiving the same report again returns its conversation.
func (s *Service) ReceiveReport(ctx context.Context, workspaceID uuid.UUID, r AppReport) (Received, error) {
	if err := r.validate(); err != nil {
		return Received{}, err
	}
	in := Inbound{
		ChannelID: r.ChannelID, Thread: r.ReportID.String(), ExternalID: r.ReportID.String(),
		Body: r.Body, Files: r.Files,
	}
	var out Received
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		var err error
		out, err = s.receiveReport(ctx, q, tx, workspaceID, r, in)
		return err
	})
	if err != nil {
		return Received{}, wrap(err, "receive report")
	}
	return out, nil
}

func (s *Service) receiveReport(
	ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, r AppReport, in Inbound,
) (Received, error) {
	prev, err := q.ThreadMessageConversation(ctx, store.ThreadMessageConversationParams{
		WorkspaceID: workspaceID, ChannelID: &in.ChannelID, ExternalThreadID: &in.Thread, ExternalID: &in.ExternalID,
	})
	if err == nil {
		return Received{ConversationID: prev}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Received{}, err
	}
	contactID, err := s.reportContact(ctx, q, tx, workspaceID, r.Contact)
	if err != nil {
		return Received{}, err
	}
	conv, err := s.openThread(ctx, q, tx, workspaceID, contactID, in)
	if err != nil {
		return Received{}, err
	}
	kind := KindText
	if in.Body == "" {
		kind = KindMedia
	}
	info := r.Info
	info.ReportID, info.ChannelID = r.ReportID, r.ChannelID
	now := s.now()
	msg, err := s.insertInbound(ctx, q, conv, in, inboundMessage{
		Direction: DirectionIn, SenderType: SenderContact, SenderID: &contactID, Kind: kind, Report: &info,
	}, now)
	if err != nil {
		return Received{}, err
	}
	if err := q.TouchConversation(ctx, store.TouchConversationParams{WorkspaceID: workspaceID, ID: conv.ID, At: now}); err != nil {
		return Received{}, err
	}
	if err := outbox.Add(ctx, tx, workspaceID, "message.created", map[string]any{
		"conversation_id": conv.ID, "message_id": msg, "kind": kind, "is_private": false,
		"sender_type": SenderContact, "sender_id": contactID,
	}); err != nil {
		return Received{}, err
	}
	return Received{Stored: true, ConversationID: conv.ID, MessageID: msg, CreatedConversation: true}, nil
}

// reportContact finds the verified contact by its external id or creates
// a contact. An unverified external id is not stored on the contact.
func (s *Service) reportContact(ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, c ReportContact) (uuid.UUID, error) {
	name, email := truncate(strings.TrimSpace(c.Name), 200), optional(strings.TrimSpace(c.Email))
	if email != nil && len(*email) < 3 {
		email = nil // below the column's minimum: not an address anyway
	}
	var externalID *string
	if c.Verified {
		externalID = &c.ExternalID
		if err := q.LockExternalID(ctx, store.LockExternalIDParams{WorkspaceID: workspaceID.String(), ExternalID: c.ExternalID}); err != nil {
			return uuid.Nil, err
		}
		found, err := q.ContactByExternalID(ctx, store.ContactByExternalIDParams{WorkspaceID: workspaceID, ExternalID: externalID})
		if err == nil {
			return found.ID, q.FillContact(ctx, store.FillContactParams{WorkspaceID: workspaceID, ID: found.ID, Name: name, Email: email})
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	id, err := newID()
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := q.CreateContact(ctx, store.CreateContactParams{
		ID: id, WorkspaceID: workspaceID, Name: name, Email: email, ExternalID: externalID,
	}); err != nil {
		return uuid.Nil, err
	}
	return id, outbox.Add(ctx, tx, workspaceID, "contact.created", map[string]any{"contact_id": id})
}

// ExpireAttachments marks the attachments of deleted objects expired.
func (s *Service) ExpireAttachments(ctx context.Context, workspaceID uuid.UUID, storageKeys []string) error {
	if len(storageKeys) == 0 {
		return nil
	}
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		_, err := q.ExpireAttachments(ctx, store.ExpireAttachmentsParams{WorkspaceID: workspaceID, StorageKeys: storageKeys})
		return err
	})
	return wrap(err, "expire attachments")
}

func (r *AppReport) validate() error {
	r.Body = truncate(strings.TrimSpace(r.Body), MaxBodyLength)
	switch {
	case r.ChannelID == uuid.Nil || r.ReportID == uuid.Nil:
		return fmt.Errorf("%w: report without a channel or an id", ErrInvalid)
	case r.Body == "" && len(r.Files) == 0:
		return fmt.Errorf("%w: report is empty", ErrInvalid)
	case len(r.Files) > MaxAttachments:
		return fmt.Errorf("%w: at most %d attachments per message", ErrInvalid, MaxAttachments)
	case r.Contact.Verified && (r.Contact.ExternalID == "" || len(r.Contact.ExternalID) > 255):
		return fmt.Errorf("%w: a verified contact needs an external id", ErrInvalid)
	case len(r.Contact.Email) > 254:
		return fmt.Errorf("%w: contact email is too long", ErrInvalid)
	case len(r.Info.Files) > 0 && len(r.Info.Files) != len(r.Files):
		return fmt.Errorf("%w: a file check for each file", ErrInvalid)
	}
	for i, f := range r.Files {
		r.Files[i].FileName = truncate(strings.TrimSpace(f.FileName), 255)
		if r.Files[i].FileName == "" {
			r.Files[i].FileName = "file"
		}
		if len(f.ContentType) < 3 || len(f.ContentType) > 255 {
			r.Files[i].ContentType = "application/octet-stream"
		}
		if !f.Rejected && f.StorageKey == "" {
			return fmt.Errorf("%w: a report file has no storage key", ErrInvalid)
		}
	}
	return nil
}
