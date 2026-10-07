package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// What a customer sent that the panel does not show (Message.Unsupported).
const (
	UnsupportedSticker  = "sticker"
	UnsupportedLocation = "location"
	UnsupportedContact  = "contact"
	UnsupportedPoll     = "poll"
	UnsupportedOther    = "other"
)

// AttachmentRejected marks a file the channel could not fetch (too large,
// unavailable): the row has no object in storage.
const AttachmentRejected = "rejected"

// Inbound is a message that arrived through a channel (docs/02 §11.2).
type Inbound struct {
	ChannelID uuid.UUID
	// Thread is the external chat: for Telegram private chats, the user id
	// of the person.
	Thread string
	// ExternalID is the message id within the thread; a message seen
	// before is not stored again.
	ExternalID string
	Sender     InboundSender
	// ContactID: the channel knows the contact already (a widget session,
	// W-01), so the sender is not looked up by Sender; Sender is ignored.
	ContactID uuid.UUID
	// FromOwner: the owner of a personal account wrote it in their own
	// Telegram app. It is stored as an operator reply, and only into an
	// open conversation: the panel is not a mirror of every private chat
	// (ADR-0014).
	FromOwner bool
	Body      string
	Files     []ReceivedFile
	// Unsupported names what the message holds instead of text and files
	// (UnsupportedSticker…); empty for ordinary messages.
	Unsupported string
	// CanAskTopic: the channel can ask the customer for the topic (the
	// Telegram bot's buttons); Received.AskTopic says when to.
	CanAskTopic bool
}

// InboundSender is the person who wrote, as the channel knows them.
type InboundSender struct {
	ExternalUserID string
	Name           string
	Username       string // without "@"
}

// ReceivedFile is a file the channel stored before calling Receive
// (storage calls never run inside a transaction).
type ReceivedFile struct {
	// StorageKey comes from storage.NewKey(workspace, KindConversation);
	// empty for a rejected file.
	StorageKey  string
	FileName    string
	ContentType string
	Size        int64
	Rejected    bool
}

// Received is the outcome of Receive. Stored is false for a duplicate and
// for an owner's message without an open conversation.
type Received struct {
	Stored              bool
	ConversationID      uuid.UUID
	MessageID           uuid.UUID
	CreatedConversation bool
	// AskTopic: the channel asks the customer for the topic now (see
	// TopicQuestion). Asked once per conversation.
	AskTopic bool
}

// InboundState tells a channel, before it fetches files, whether the
// message is stored already and whether its chat has an open conversation.
func (s *Service) InboundState(ctx context.Context, workspaceID, channelID uuid.UUID, thread, externalID string) (seen, open bool, err error) {
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		if seen, err = q.ThreadMessageExists(ctx, store.ThreadMessageExistsParams{
			WorkspaceID: workspaceID, ChannelID: &channelID, ExternalThreadID: &thread, ExternalID: &externalID,
		}); err != nil {
			return err
		}
		open, err = q.OpenThreadExists(ctx, store.OpenThreadExistsParams{
			WorkspaceID: workspaceID, ChannelID: &channelID, ExternalThreadID: &thread,
		})
		return err
	})
	return seen, open, wrap(err, "inbound state")
}

// Receive stores an inbound message: the contact by its identity in the
// channel, the open conversation of the chat or a new one in the queue,
// the message with its files, the events, and a reminder for the assignee.
// A customer message reopens a resolved conversation and ends waiting for
// the customer (docs/02 §8).
func (s *Service) Receive(ctx context.Context, workspaceID uuid.UUID, in Inbound) (Received, error) {
	if err := in.validate(); err != nil {
		return Received{}, err
	}
	var out Received
	// Two first messages of one person may race to create the contact or
	// the conversation; the loser sees the winner's rows on the retry.
	for attempt := 0; ; attempt++ {
		err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
			var err error
			out, err = s.receive(ctx, q, tx, workspaceID, in)
			return err
		})
		if err != nil && attempt == 0 && isUniqueViolation(err) {
			continue
		}
		if err != nil {
			return Received{}, wrap(err, "receive message")
		}
		return out, nil
	}
}

func (s *Service) receive(ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, in Inbound) (Received, error) {
	seen, err := q.ThreadMessageExists(ctx, store.ThreadMessageExistsParams{
		WorkspaceID: workspaceID, ChannelID: &in.ChannelID, ExternalThreadID: &in.Thread, ExternalID: &in.ExternalID,
	})
	if err != nil || seen {
		return Received{}, err
	}
	conv, err := q.LockOpenThread(ctx, store.LockOpenThreadParams{
		WorkspaceID: workspaceID, ChannelID: &in.ChannelID, ExternalThreadID: &in.Thread,
	})
	hasOpen := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Received{}, err
	}
	now := s.now()
	if in.FromOwner {
		if !hasOpen {
			return Received{}, nil
		}
		return s.storeOwnerReply(ctx, q, tx, conv, in, now)
	}

	out := Received{Stored: true}
	// Also for an open conversation: the username follows Telegram.
	contactID := in.ContactID
	if contactID == uuid.Nil {
		if contactID, err = s.identify(ctx, q, tx, workspaceID, in); err != nil {
			return Received{}, err
		}
	}
	if !hasOpen {
		if conv, err = s.openThread(ctx, q, tx, workspaceID, contactID, in, true); err != nil {
			return Received{}, err
		}
		out.CreatedConversation = true
	} else if conv, err = s.wakeUp(ctx, q, tx, conv, now); err != nil {
		return Received{}, err
	}
	out.ConversationID = conv.ID
	if out.MessageID, err = s.storeCustomerMessage(ctx, q, tx, conv, in, now); err != nil {
		return Received{}, err
	}
	if in.CanAskTopic {
		if out.AskTopic, err = s.askTopic(ctx, q, conv, now); err != nil {
			return Received{}, err
		}
	}
	return out, s.wakeBot(ctx, tx, conv)
}

// storeCustomerMessage adds what the customer wrote to their conversation,
// the event, and a reminder for the assignee.
func (s *Service) storeCustomerMessage(
	ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, in Inbound, now time.Time,
) (uuid.UUID, error) {
	kind := KindText
	if in.Body == "" && len(in.Files) > 0 {
		kind = KindMedia
	}
	msg, err := s.insertInbound(ctx, q, conv, in, inboundMessage{
		Direction: DirectionIn, SenderType: SenderContact, SenderID: &conv.ContactID, Kind: kind,
	}, now)
	if err != nil {
		return uuid.Nil, err
	}
	if err := q.TouchConversation(ctx, store.TouchConversationParams{WorkspaceID: conv.WorkspaceID, ID: conv.ID, At: now}); err != nil {
		return uuid.Nil, err
	}
	if err := outbox.Add(ctx, tx, conv.WorkspaceID, "message.created", map[string]any{
		"conversation_id": conv.ID, "message_id": msg, "kind": kind, "is_private": false,
		"sender_type": SenderContact, "sender_id": conv.ContactID,
	}); err != nil {
		return uuid.Nil, err
	}
	// The assignee may be away from the panel (docs/04 §5.5).
	if s.notifier != nil && conv.AssigneeID != nil {
		if err := s.notifier.NeedsAttention(ctx, tx, conv.WorkspaceID, conv.ID, *conv.AssigneeID); err != nil {
			return uuid.Nil, err
		}
	}
	return msg, nil
}

// identify returns the contact of the sender in the channel, creating the
// contact and its identity on the first message. An operator may have
// renamed the contact: its name is not overwritten later.
func (s *Service) identify(ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID uuid.UUID, in Inbound) (uuid.UUID, error) {
	username := optional(in.Sender.Username)
	ident, err := q.GetIdentity(ctx, store.GetIdentityParams{
		WorkspaceID: workspaceID, ChannelID: in.ChannelID, ExternalUserID: in.Sender.ExternalUserID,
	})
	if err == nil {
		if !sameText(ident.Username, username) {
			err = q.UpdateIdentityUsername(ctx, store.UpdateIdentityUsernameParams{WorkspaceID: workspaceID, ID: ident.ID, Username: username})
		}
		return ident.ContactID, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	contactID, err := newID()
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := q.CreateContact(ctx, store.CreateContactParams{
		ID: contactID, WorkspaceID: workspaceID, Name: truncate(strings.TrimSpace(in.Sender.Name), 200),
	}); err != nil {
		return uuid.Nil, err
	}
	identID, err := newID()
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := q.CreateIdentity(ctx, store.CreateIdentityParams{
		ID: identID, WorkspaceID: workspaceID, ContactID: contactID, ChannelID: in.ChannelID,
		ExternalUserID: in.Sender.ExternalUserID, Username: username,
	}); err != nil {
		return uuid.Nil, err
	}
	return contactID, outbox.Add(ctx, tx, workspaceID, "contact.created", map[string]any{"contact_id": contactID})
}

// openThread starts the conversation of a chat: withBot, with the AI bot
// when it answers the channel (ADR-0018), otherwise in the queue at once,
// as CreateConversation does.
func (s *Service) openThread(
	ctx context.Context, q *store.Queries, tx pgx.Tx, workspaceID, contactID uuid.UUID, in Inbound, withBot bool,
) (store.Conversation, error) {
	id, err := newID()
	if err != nil {
		return store.Conversation{}, err
	}
	number, err := q.NextConversationNumber(ctx, workspaceID)
	if err != nil {
		return store.Conversation{}, err
	}
	status := StatusQueued
	if withBot && s.bot != nil {
		ok, err := s.bot.Answers(ctx, tx, workspaceID, in.ChannelID)
		if err != nil {
			return store.Conversation{}, fmt.Errorf("ask bot: %w", err)
		}
		if ok {
			status = StatusBot
		}
	}
	conv, err := q.CreateThreadConversation(ctx, store.CreateThreadConversationParams{
		ID: id, WorkspaceID: workspaceID, Number: number, ContactID: contactID,
		ChannelID: &in.ChannelID, ExternalThreadID: &in.Thread, Status: string(status),
	})
	if err != nil {
		return store.Conversation{}, err
	}
	if err := outbox.Add(ctx, tx, workspaceID, "conversation.created", map[string]any{
		"conversation_id": id, "contact_id": contactID, "number": number, "status": status,
	}); err != nil {
		return store.Conversation{}, err
	}
	if status == StatusBot {
		return conv, nil
	}
	return conv, s.queued(ctx, tx, workspaceID, nil)
}

// wakeUp applies what a customer message means for the state: waiting for
// the customer is over (pending → assigned), a resolved conversation
// reopens — to its operator while they are a member and, with automatic
// distribution, online (canReturnTo), otherwise to the queue. A closed one
// reopens the same way; only an app report brings one here
// (ReceiveReportMessage), since a report keeps its conversation for good
// (docs/02 §8).
func (s *Service) wakeUp(ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, now time.Time) (store.Conversation, error) {
	cur := state{Status: Status(conv.Status), AssigneeID: conv.AssigneeID, ResolvedAt: conv.ResolvedAt, ClosedAt: conv.ClosedAt}
	to := cur.Status
	switch cur.Status {
	case StatusPending:
		to = StatusAssigned
	case StatusResolved, StatusClosed:
		to = StatusQueued
		if cur.AssigneeID != nil {
			ok, err := s.canReturnTo(ctx, q, tx, conv.WorkspaceID, *cur.AssigneeID)
			if err != nil {
				return conv, err
			}
			if ok {
				to = StatusAssigned
			}
		}
	}
	if to == cur.Status {
		return conv, nil
	}
	next, err := transition(cur, to, now)
	if cur.Status == StatusClosed {
		next, err = reopenClosed(cur, to)
	}
	if err != nil {
		return conv, err
	}
	n, err := q.UpdateConversationState(ctx, store.UpdateConversationStateParams{
		WorkspaceID: conv.WorkspaceID, ID: conv.ID, Version: conv.Version, Status: string(next.Status),
		AssigneeID: next.AssigneeID, ResolvedAt: next.ResolvedAt, ClosedAt: next.ClosedAt,
	})
	if err != nil {
		return conv, err
	}
	if n == 0 {
		return conv, ErrConflict // cannot happen under the row lock
	}
	if err := s.addStateEvents(ctx, tx, conv, uuid.Nil, cur, next); err != nil {
		return conv, err
	}
	if err := addEventMessage(ctx, q, tx, conv, uuid.Nil, cur, next, now); err != nil {
		return conv, err
	}
	conv.Status, conv.AssigneeID, conv.ResolvedAt, conv.ClosedAt = string(next.Status), next.AssigneeID, next.ResolvedAt, next.ClosedAt
	conv.Version++
	return conv, nil
}

// storeOwnerReply records what the account owner wrote in Telegram as an
// operator reply "sent outside the panel": delivered already, by nobody
// in the workspace.
func (s *Service) storeOwnerReply(
	ctx context.Context, q *store.Queries, tx pgx.Tx, conv store.Conversation, in Inbound, now time.Time,
) (Received, error) {
	kind := KindText
	if in.Body == "" && len(in.Files) > 0 {
		kind = KindMedia
	}
	msg, err := s.insertInbound(ctx, q, conv, in, inboundMessage{
		Direction: DirectionOut, SenderType: SenderOperator, Kind: kind, Delivery: ptr("sent"), OutsidePanel: true,
	}, now)
	if err != nil {
		return Received{}, err
	}
	if err := q.TouchConversation(ctx, store.TouchConversationParams{
		WorkspaceID: conv.WorkspaceID, ID: conv.ID, At: now, OperatorReply: true,
	}); err != nil {
		return Received{}, err
	}
	err = outbox.Add(ctx, tx, conv.WorkspaceID, "message.created", map[string]any{
		"conversation_id": conv.ID, "message_id": msg, "kind": kind, "is_private": false,
		"sender_type": SenderOperator, "sender_id": nil,
	})
	return Received{Stored: true, ConversationID: conv.ID, MessageID: msg}, err
}

type inboundMessage struct {
	Direction, SenderType, Kind string
	SenderID                    *uuid.UUID
	Delivery                    *string
	OutsidePanel                bool
	// Report: the message is a problem report from an app.
	Report *ReportInfo
}

// messageMeta is the meta of a message that came through a channel.
type messageMeta struct {
	Unsupported  string `json:"unsupported,omitempty"`
	OutsidePanel bool   `json:"sent_outside_panel,omitempty"`
	// Report: the message is a problem report from an app (docs/02 §11.6).
	Report *ReportInfo `json:"report,omitempty"`
}

func (s *Service) insertInbound(
	ctx context.Context, q *store.Queries, conv store.Conversation, in Inbound, m inboundMessage, now time.Time,
) (uuid.UUID, error) {
	id, err := newID()
	if err != nil {
		return uuid.Nil, err
	}
	meta, err := json.Marshal(messageMeta{Unsupported: in.Unsupported, OutsidePanel: m.OutsidePanel, Report: m.Report})
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode meta: %w", err)
	}
	if _, err := q.CreateInboundMessage(ctx, store.CreateInboundMessageParams{
		ID: id, WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID,
		Direction: m.Direction, SenderType: m.SenderType, SenderID: m.SenderID, Kind: m.Kind,
		Body: in.Body, AttachmentsCount: int32(len(in.Files)), ExternalID: &in.ExternalID, //nolint:gosec // G115: at most MaxAttachments
		DeliveryStatus: m.Delivery, Meta: meta, CreatedAt: now,
	}); err != nil {
		return uuid.Nil, err
	}
	for _, f := range in.Files {
		attID, err := newID()
		if err != nil {
			return uuid.Nil, err
		}
		key, status := f.StorageKey, AttachmentReady
		if f.Rejected {
			// The key stays unused; it satisfies the prefix CHECK.
			if key, err = storage.NewKey(conv.WorkspaceID, storage.KindConversation); err != nil {
				return uuid.Nil, err
			}
			status = AttachmentRejected
		}
		if _, err := q.CreateReceivedAttachment(ctx, store.CreateReceivedAttachmentParams{
			ID: attID, WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID, MessageID: &id,
			StorageKey: key, FileName: f.FileName, Mime: f.ContentType, Size: max(f.Size, 1), Status: status,
		}); err != nil {
			return uuid.Nil, err
		}
	}
	return id, nil
}

func (in *Inbound) validate() error {
	in.Body = truncate(strings.TrimSpace(in.Body), MaxBodyLength)
	switch {
	case in.ChannelID == uuid.Nil:
		return fmt.Errorf("%w: inbound message without a channel", ErrInvalid)
	case in.Thread == "" || len(in.Thread) > 255 || in.ExternalID == "" || len(in.ExternalID) > 255:
		return fmt.Errorf("%w: inbound message needs a thread and an external id", ErrInvalid)
	case in.FromOwner && in.ContactID != uuid.Nil:
		return fmt.Errorf("%w: an owner's message has no contact", ErrInvalid)
	case !in.FromOwner && in.ContactID == uuid.Nil && (in.Sender.ExternalUserID == "" || len(in.Sender.ExternalUserID) > 64):
		return fmt.Errorf("%w: inbound message needs the sender's id", ErrInvalid)
	case in.Body == "" && len(in.Files) == 0 && in.Unsupported == "":
		return fmt.Errorf("%w: inbound message is empty", ErrInvalid)
	case len(in.Files) > MaxAttachments:
		return fmt.Errorf("%w: at most %d attachments per message", ErrInvalid, MaxAttachments)
	}
	in.Sender.Username = truncate(in.Sender.Username, 64)
	for i, f := range in.Files {
		name := truncate(strings.TrimSpace(f.FileName), 255)
		if name == "" {
			name = "file"
		}
		in.Files[i].FileName = name
		if len(f.ContentType) < 3 || len(f.ContentType) > 255 {
			in.Files[i].ContentType = "application/octet-stream"
		}
		if !f.Rejected && f.StorageKey == "" {
			return fmt.Errorf("%w: a received file has no storage key", ErrInvalid)
		}
	}
	return nil
}

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
