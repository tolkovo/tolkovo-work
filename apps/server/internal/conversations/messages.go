package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Message directions, sender types and kinds as stored (docs/02 §7.2).
const (
	DirectionIn  = "in"
	DirectionOut = "out"

	SenderContact  = "contact"
	SenderOperator = "operator"
	SenderBot      = "bot"
	SenderSystem   = "system"

	KindText  = "text"
	KindMedia = "media"
	KindNote  = "note"
	KindEvent = "event"
)

// Message is one entry of a conversation feed.
type Message struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Direction      string
	SenderType     string
	SenderID       *uuid.UUID
	Kind           string
	Body           string
	IsPrivate      bool // internal note, never shown to the customer
	DeliveryStatus *string
	// DeliveryError: why a failed reply was not delivered (DeliveryBlocked…).
	DeliveryError string
	Attachments   []Attachment
	Event         *Event // what a system message records; nil for other kinds
	// Unsupported: what a customer sent through a channel that the panel
	// does not show (UnsupportedSticker…); empty otherwise.
	Unsupported string
	// SentOutsidePanel: an operator message the owner of a personal
	// account wrote in their own Telegram app.
	SentOutsidePanel bool
	// Report: the message is a problem report from an app (docs/02 §11.6).
	Report    *ReportInfo
	CreatedAt time.Time
	EditedAt  *time.Time
	DeletedAt *time.Time
}

// MessageInput is an operator reply or an internal note.
type MessageInput struct {
	Body          string
	Private       bool // internal note
	AttachmentIDs []uuid.UUID
	// IdempotencyKey makes a retried send return the first message instead
	// of posting a second one (docs/00 §8).
	IdempotencyKey string
}

// Messages lists a conversation's feed, newest first.
func (s *Service) Messages(ctx context.Context, workspaceID, conversationID uuid.UUID, cursor string, limit int) (Page[Message], error) {
	before, err := decodeIDCursor(cursor)
	if err != nil {
		return Page[Message]{}, err
	}
	lim := pageSize(limit)
	var page Page[Message]
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		// A foreign or missing conversation is 404, not an empty feed.
		if _, err := q.GetConversation(ctx, store.GetConversationParams{WorkspaceID: workspaceID, ID: conversationID}); err != nil {
			return err
		}
		rows, err := q.ListMessages(ctx, store.ListMessagesParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, BeforeID: before, Lim: lim + 1,
		})
		if err != nil {
			return err
		}
		page = paginate(rows, lim, func(m store.Message) Message { return messageFrom(m, nil) },
			func(m store.Message) string { return encodeIDCursor(m.ID) })
		return attachTo(ctx, q, workspaceID, page.Items)
	})
	if err != nil {
		return Page[Message]{}, wrap(err, "list messages")
	}
	return page, nil
}

// PostMessage adds an operator reply or an internal note. Replies need the
// conversation to be the actor's own unless actor.AnyConversation; notes
// may be left in any conversation the actor can see. Attachments must be
// uploads of the same conversation (CreateUpload) that are not yet sent.
// created is false when the Idempotency-Key matched an earlier message.
func (s *Service) PostMessage(
	ctx context.Context, workspaceID uuid.UUID, actor Actor, conversationID uuid.UUID, in MessageInput,
) (msg Message, created bool, err error) {
	if err := in.validate(); err != nil {
		return Message{}, false, err
	}
	if in.IdempotencyKey != "" {
		if m, found, err := s.replay(ctx, workspaceID, conversationID, in); err != nil || found {
			return m, false, err
		}
	}
	// Storage is checked before the transaction: S3 is a network call.
	if err := s.checkUploads(ctx, workspaceID, conversationID, in.AttachmentIDs); err != nil {
		return Message{}, false, err
	}
	err = s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		conv, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if in.IdempotencyKey != "" {
			// A concurrent retry may have won while this request was checking files.
			if m, found, err := replayIn(ctx, q, workspaceID, conversationID, in); err != nil || found {
				msg = m
				return err
			}
		}
		if err := canPost(conv, actor, in.Private); err != nil {
			return err
		}
		msg, err = insertMessage(ctx, q, tx, conv, actor, in, s.now())
		if err != nil {
			return err
		}
		created = true
		if msg.DeliveryStatus != nil && *msg.DeliveryStatus == DeliveryPending {
			return s.queueDelivery(ctx, tx, workspaceID, msg.ID)
		}
		return nil
	})
	if err != nil {
		return Message{}, false, wrap(err, "post message")
	}
	return msg, created, nil
}

func (in *MessageInput) validate() error {
	in.Body = strings.TrimSpace(in.Body)
	switch {
	case in.Body == "" && len(in.AttachmentIDs) == 0:
		return fmt.Errorf("%w: a message needs a body or attachments", ErrInvalid)
	case utf8.RuneCountInString(in.Body) > MaxBodyLength:
		return fmt.Errorf("%w: body must be at most %d characters", ErrInvalid, MaxBodyLength)
	case len(in.AttachmentIDs) > MaxAttachments:
		return fmt.Errorf("%w: at most %d attachments per message", ErrInvalid, MaxAttachments)
	case len(in.IdempotencyKey) > 255:
		return fmt.Errorf("%w: Idempotency-Key must be at most 255 characters", ErrInvalid)
	}
	ids := slices.Clone(in.AttachmentIDs)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if len(slices.Compact(ids)) != len(in.AttachmentIDs) {
		return fmt.Errorf("%w: attachment ids must be unique", ErrInvalid)
	}
	return nil
}

func canPost(conv store.Conversation, actor Actor, private bool) error {
	if Status(conv.Status) == StatusClosed {
		return ErrClosed
	}
	if private || actor.AnyConversation {
		return nil
	}
	if conv.AssigneeID == nil || *conv.AssigneeID != actor.UserID {
		return ErrNotAssignee
	}
	return nil
}

func insertMessage(
	ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, actor Actor, in MessageInput, now time.Time,
) (Message, error) {
	id, err := newID()
	if err != nil {
		return Message{}, err
	}
	kind := KindText
	switch {
	case in.Private:
		kind = KindNote
	case in.Body == "":
		kind = KindMedia
	}
	var delivery *string
	if !in.Private && conv.ChannelID != nil {
		delivery = ptr(DeliveryPending) // the channel's job delivers it (T-03)
	}
	row, err := q.CreateMessage(ctx, store.CreateMessageParams{
		ID: id, WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID,
		Direction: DirectionOut, SenderType: SenderOperator, SenderID: &actor.UserID, Kind: kind,
		Body: in.Body, AttachmentsCount: int32(len(in.AttachmentIDs)), IsPrivate: in.Private, //nolint:gosec // G115: at most MaxAttachments
		DeliveryStatus: delivery, IdempotencyKey: optional(in.IdempotencyKey), CreatedAt: now,
	})
	if err != nil {
		return Message{}, err
	}
	var atts []store.Attachment
	if len(in.AttachmentIDs) > 0 {
		n, err := q.LinkAttachments(ctx, store.LinkAttachmentsParams{
			WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID, MessageID: &id, Ids: in.AttachmentIDs,
		})
		if err != nil {
			return Message{}, err
		}
		if n != int64(len(in.AttachmentIDs)) {
			return Message{}, fmt.Errorf("%w: an attachment is unknown or already sent", ErrInvalid)
		}
		atts, err = q.MessageAttachments(ctx, store.MessageAttachmentsParams{WorkspaceID: conv.WorkspaceID, MessageIds: []uuid.UUID{id}})
		if err != nil {
			return Message{}, err
		}
	}
	if !in.Private {
		err = q.TouchConversation(ctx, store.TouchConversationParams{
			WorkspaceID: conv.WorkspaceID, ID: conv.ID, At: now, OperatorReply: true,
		})
		if err != nil {
			return Message{}, err
		}
	}
	err = outbox.Add(ctx, tx, conv.WorkspaceID, "message.created", map[string]any{
		"conversation_id": conv.ID, "message_id": id, "kind": kind, "is_private": in.Private,
		"sender_type": SenderOperator, "sender_id": actor.UserID,
	})
	if err != nil {
		return Message{}, err
	}
	return messageFrom(row, atts), nil
}

// replay looks up an earlier message with the same Idempotency-Key.
func (s *Service) replay(ctx context.Context, workspaceID, conversationID uuid.UUID, in MessageInput) (Message, bool, error) {
	var (
		m     Message
		found bool
	)
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		m, found, err = replayIn(ctx, q, workspaceID, conversationID, in)
		return err
	})
	return m, found, wrap(err, "replay message")
}

func replayIn(ctx context.Context, q *store.Queries, workspaceID, conversationID uuid.UUID, in MessageInput) (Message, bool, error) {
	key := in.IdempotencyKey
	row, err := q.GetMessageByIdempotencyKey(ctx, store.GetMessageByIdempotencyKeyParams{
		WorkspaceID: workspaceID, ConversationID: conversationID, IdempotencyKey: &key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	msgs := []Message{messageFrom(row, nil)}
	if err := attachTo(ctx, q, workspaceID, msgs); err != nil {
		return Message{}, false, err
	}
	m := msgs[0]
	if m.Body != in.Body || m.IsPrivate != in.Private || !sameAttachments(m.Attachments, in.AttachmentIDs) {
		return Message{}, false, ErrIdempotencyMismatch
	}
	return m, true, nil
}

func sameAttachments(atts []Attachment, ids []uuid.UUID) bool {
	if len(atts) != len(ids) {
		return false
	}
	for _, a := range atts {
		if !slices.Contains(ids, a.ID) {
			return false
		}
	}
	return true
}

// attachTo loads the attachments of msgs in one query.
func attachTo(ctx context.Context, q *store.Queries, workspaceID uuid.UUID, msgs []Message) error {
	ids := make([]uuid.UUID, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.MessageAttachments(ctx, store.MessageAttachmentsParams{WorkspaceID: workspaceID, MessageIds: ids})
	if err != nil {
		return err
	}
	byMessage := make(map[uuid.UUID][]Attachment, len(rows))
	for _, r := range rows {
		byMessage[*r.MessageID] = append(byMessage[*r.MessageID], attachmentFrom(r))
	}
	for i := range msgs {
		msgs[i].Attachments = byMessage[msgs[i].ID]
	}
	return nil
}

func messageFrom(m store.Message, atts []store.Attachment) Message {
	out := Message{
		ID: m.ID, ConversationID: m.ConversationID, Direction: m.Direction, SenderType: m.SenderType,
		SenderID: m.SenderID, Kind: m.Kind, Body: m.Body, IsPrivate: m.IsPrivate,
		DeliveryStatus: m.DeliveryStatus, Event: eventFrom(m.Kind, m.Meta),
		CreatedAt: m.CreatedAt, EditedAt: m.EditedAt, DeletedAt: m.DeletedAt,
	}
	if m.DeliveryError != nil {
		out.DeliveryError = *m.DeliveryError
	}
	if m.Kind != KindEvent && len(m.Meta) > 2 {
		var meta messageMeta
		if json.Unmarshal(m.Meta, &meta) == nil {
			out.Unsupported, out.SentOutsidePanel, out.Report = meta.Unsupported, meta.OutsidePanel, meta.Report
		}
	}
	for _, a := range atts {
		out.Attachments = append(out.Attachments, attachmentFrom(a))
	}
	return out
}
