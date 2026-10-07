package conversations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
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
	conv, err := s.openThread(ctx, q, tx, workspaceID, contactID, in, false) // a report is for people
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
// a contact. An unverified external id is not stored on the contact; the
// new contact is marked unverified when the app user claimed anything.
func (s *Service) reportContact(ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, c ReportContact) (uuid.UUID, error) {
	name, email := truncate(strings.TrimSpace(c.Name), 200), optional(strings.TrimSpace(c.Email))
	if email != nil && len(*email) < 3 {
		email = nil // below the column's minimum: not an address anyway
	}
	if c.Verified {
		id, _, err := s.verifiedContact(ctx, q, tx, workspaceID, c.ExternalID, name, email)
		return id, err
	}
	return s.newContact(ctx, q, tx, workspaceID, store.CreateContactParams{
		Name: name, Email: email, IdentityUnverified: c.ExternalID != "" || name != "" || email != nil,
	})
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

// AttachedKeys returns which of the storage keys are files of messages: a
// channel that stored objects ahead of a message deletes only the others.
func (s *Service) AttachedKeys(ctx context.Context, workspaceID uuid.UUID, storageKeys []string) ([]string, error) {
	if len(storageKeys) == 0 {
		return nil, nil
	}
	var keys []string
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		keys, err = q.AttachedKeys(ctx, store.AttachedKeysParams{WorkspaceID: workspaceID, StorageKeys: storageKeys})
		return err
	})
	return keys, wrap(err, "attached keys")
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

// Replies of a report conversation to the app user (docs/02 §11.6).
const (
	// MaxReportReplies is the most replies one call returns.
	MaxReportReplies = 50
	// replySettle: a reply is returned this long after it is stored, when
	// every transaction that took a smaller id has committed.
	replySettle = 5 * time.Second
)

// ReportReply is what an operator or the bot wrote to the app user: no
// names, no notes, no events.
type ReportReply struct {
	ID         uuid.UUID
	SenderType string // SenderOperator or SenderBot
	Body       string
	CreatedAt  time.Time
	// Files that can be downloaded, with a fresh signed request each.
	Files []ReplyFile
}

// ReplyFile is a file of a reply.
type ReplyFile struct {
	FileName    string
	ContentType string
	Size        int64
	Download    storage.SignedRequest
}

// ReportReplies is a page of replies.
type ReportReplies struct {
	Items []ReportReply
	// Cursor follows the last item; it is since when nothing is new.
	Cursor string
	More   bool
}

// ReportReplies returns the replies of a report conversation after since,
// oldest first.
func (s *Service) ReportReplies(ctx context.Context, workspaceID, conversationID uuid.UUID, since string) (ReportReplies, error) {
	after, err := decodeIDCursor(since)
	if err != nil {
		return ReportReplies{}, err
	}
	var (
		rows []store.Message
		atts []store.Attachment
	)
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		rows, err = q.ReportReplies(ctx, store.ReportRepliesParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, AfterID: after,
			Settled: s.now().Add(-replySettle), Lim: MaxReportReplies + 1,
		})
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, m := range rows {
			ids = append(ids, m.ID)
		}
		atts, err = q.MessageAttachments(ctx, store.MessageAttachmentsParams{WorkspaceID: workspaceID, MessageIds: ids})
		return err
	})
	if err != nil {
		return ReportReplies{}, wrap(err, "list report replies")
	}
	out := ReportReplies{Cursor: since, More: len(rows) > MaxReportReplies}
	rows = rows[:min(len(rows), MaxReportReplies)]
	byMessage := make(map[uuid.UUID][]store.Attachment, len(atts))
	for _, a := range atts {
		if a.Status == AttachmentReady {
			byMessage[*a.MessageID] = append(byMessage[*a.MessageID], a)
		}
	}
	// Signing needs no network for either driver; it stays out of the
	// transaction anyway (docs/00 §6.5).
	for _, m := range rows {
		r := ReportReply{ID: m.ID, SenderType: m.SenderType, Body: m.Body, CreatedAt: m.CreatedAt, Files: []ReplyFile{}}
		for _, a := range byMessage[m.ID] {
			req, err := s.files.PresignGet(ctx, a.StorageKey, storage.PresignGetOptions{Filename: a.FileName, ContentType: a.Mime})
			if err != nil {
				return ReportReplies{}, fmt.Errorf("presign reply file: %w", err)
			}
			r.Files = append(r.Files, ReplyFile{FileName: a.FileName, ContentType: a.Mime, Size: a.Size, Download: req})
		}
		out.Items = append(out.Items, r)
		out.Cursor = encodeIDCursor(m.ID)
	}
	return out, nil
}

// ReportMessage is what the app user wrote back about their report.
type ReportMessage struct {
	ConversationID uuid.UUID
	// Key names the message: a retry with the same key finds the first one.
	Key  string
	Body string
}

// ReportMessageReceived is a stored message of the app user.
type ReportMessageReceived struct {
	MessageID uuid.UUID
	CreatedAt time.Time
	// Created is false when Key named an earlier message.
	Created bool
}

// ReceiveReportMessage stores a message of the app user in the report's
// conversation as a message of the customer. The conversation wakes up as
// for any customer message, and a closed one reopens: one report is one
// conversation for good (docs/02 §8). A retry with the same key and text
// returns the first message; with another text, ErrIdempotencyMismatch.
func (s *Service) ReceiveReportMessage(ctx context.Context, workspaceID uuid.UUID, m ReportMessage) (ReportMessageReceived, error) {
	m.Body = strings.TrimSpace(m.Body)
	switch {
	case m.Body == "" || utf8.RuneCountInString(m.Body) > MaxBodyLength:
		return ReportMessageReceived{}, fmt.Errorf("%w: a message must be 1 to %d characters", ErrInvalid, MaxBodyLength)
	case m.Key == "" || len(m.Key) > 255:
		return ReportMessageReceived{}, fmt.Errorf("%w: a message needs a key of at most 255 bytes", ErrInvalid)
	}
	// The prefix keeps the app's keys apart from the report's own message.
	in := Inbound{ExternalID: "msg:" + m.Key, Body: m.Body}
	var out ReportMessageReceived
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		conv, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: m.ConversationID})
		if err != nil {
			return err
		}
		if conv.ChannelID == nil || conv.ExternalThreadID == nil {
			return ErrNotFound // not a report conversation
		}
		prev, err := q.MessageByExternalID(ctx, store.MessageByExternalIDParams{
			WorkspaceID: workspaceID, ConversationID: conv.ID, ExternalID: &in.ExternalID,
		})
		if err == nil {
			if prev.Body != in.Body || prev.Direction != DirectionIn {
				return ErrIdempotencyMismatch
			}
			out = ReportMessageReceived{MessageID: prev.ID, CreatedAt: prev.CreatedAt}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := s.now()
		if conv, err = s.wakeUp(ctx, q, tx, conv, now); err != nil {
			return err
		}
		id, err := s.storeCustomerMessage(ctx, q, tx, conv, in, now)
		out = ReportMessageReceived{MessageID: id, CreatedAt: now, Created: true}
		return err
	})
	if err != nil {
		return ReportMessageReceived{}, wrap(err, "receive report message")
	}
	return out, nil
}
