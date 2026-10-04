package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/outbox"
)

// Delivery statuses of an operator reply in a channel conversation.
const (
	DeliveryPending = "pending"
	DeliverySent    = "sent"
	DeliveryRead    = "read"
	DeliveryFailed  = "failed"
)

// Why a reply was not delivered (Message.DeliveryError), docs/02 §11.4.
const (
	// DeliveryBlocked: the customer blocked the bot or the account.
	DeliveryBlocked = "blocked"
	// DeliveryChatNotFound: the chat is gone or the account cannot write to
	// the person (deleted account, unknown peer).
	DeliveryChatNotFound = "chat_not_found"
	// DeliveryLimited: Telegram restricted the account from writing first.
	DeliveryLimited = "limited"
	// DeliveryRejected: Telegram refused this message (size, format).
	DeliveryRejected = "rejected"
	// DeliveryChannelUnavailable: the channel is disconnected or broken.
	DeliveryChannelUnavailable = "channel_unavailable"
	// DeliveryGaveUp: Telegram stayed unreachable through every retry.
	DeliveryGaveUp = "failed"
)

// ErrNotFailed rejects a retry of a reply that has not failed.
var ErrNotFailed = errors.New("conversations: the message has not failed")

// Deliverer queues an operator reply for its channel. It runs inside the
// transaction that stores the reply, so a stored reply is always queued
// (docs/02 §11.4); it must not call the network.
type Deliverer interface {
	QueueDelivery(ctx context.Context, tx pgx.Tx, workspaceID, messageID uuid.UUID) error
}

// SetDeliverer connects the channels module; call it before serving.
func (s *Service) SetDeliverer(d Deliverer) { s.deliverer = d }

// Outgoing is an operator reply as its channel sends it.
type Outgoing struct {
	MessageID      uuid.UUID
	ConversationID uuid.UUID
	// ChannelID and Thread: where the reply goes; nil and empty when the
	// conversation has no channel.
	ChannelID *uuid.UUID
	Thread    string
	Body      string
	Files     []OutgoingFile
	Status    string
	// SentParts are the external ids of the pieces already delivered by
	// earlier attempts, in order.
	SentParts []string
	// Waiting: an earlier reply of the conversation is still pending.
	Waiting bool
}

// OutgoingFile is an attachment of a reply with its object in storage.
type OutgoingFile struct {
	FileName    string
	ContentType string
	Size        int64
	StorageKey  string
}

// Outgoing loads a reply for delivery.
func (s *Service) Outgoing(ctx context.Context, workspaceID, messageID uuid.UUID) (Outgoing, error) {
	var out Outgoing
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		row, err := q.GetOutgoing(ctx, store.GetOutgoingParams{WorkspaceID: workspaceID, ID: messageID})
		if err != nil {
			return err
		}
		m := row.Message
		out = Outgoing{
			MessageID: m.ID, ConversationID: m.ConversationID, ChannelID: row.ThreadChannelID,
			Body: m.Body, Waiting: row.Waiting,
		}
		if row.ExternalThreadID != nil {
			out.Thread = *row.ExternalThreadID
		}
		if m.DeliveryStatus != nil {
			out.Status = *m.DeliveryStatus
		}
		var meta struct {
			SentParts []string `json:"sent_parts"`
		}
		if len(m.Meta) > 2 {
			_ = json.Unmarshal(m.Meta, &meta) // written only by this module
		}
		out.SentParts = meta.SentParts
		atts, err := q.MessageAttachments(ctx, store.MessageAttachmentsParams{WorkspaceID: workspaceID, MessageIds: []uuid.UUID{m.ID}})
		if err != nil {
			return err
		}
		for _, a := range atts {
			out.Files = append(out.Files, OutgoingFile{FileName: a.FileName, ContentType: a.Mime, Size: a.Size, StorageKey: a.StorageKey})
		}
		return nil
	})
	if err != nil {
		return Outgoing{}, wrap(err, "load outgoing message")
	}
	return out, nil
}

// AddSentPart records that one more piece of a pending reply was delivered
// with the given external id; a retry starts after it.
func (s *Service) AddSentPart(ctx context.Context, workspaceID, messageID uuid.UUID, externalID string) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		_, err := q.AddSentPart(ctx, store.AddSentPartParams{WorkspaceID: workspaceID, ID: messageID, Part: externalID})
		return err
	})
	return wrap(err, "record sent part")
}

// Delivery is the outcome of sending a reply.
type Delivery struct {
	Status string // DeliverySent or DeliveryFailed
	// ExternalID: the channel's id of the last piece sent, if known.
	ExternalID string
	// Error: why it failed (DeliveryBlocked…); empty when sent.
	Error string
}

// SetDelivery records the outcome of a pending reply and tells the panel
// (message.delivery). A reply that is no longer pending is left alone.
func (s *Service) SetDelivery(ctx context.Context, workspaceID, messageID uuid.UUID, d Delivery) error {
	if d.Status != DeliverySent && d.Status != DeliveryFailed {
		return fmt.Errorf("%w: delivery status %q", ErrInvalid, d.Status)
	}
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		convID, err := q.SetDelivery(ctx, store.SetDeliveryParams{
			WorkspaceID: workspaceID, ID: messageID, DeliveryStatus: &d.Status,
			DeliveryError: optional(d.Error), ExternalID: optional(d.ExternalID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return deliveryEvent(ctx, tx, workspaceID, convID, messageID, d.Status, d.Error)
	})
	return wrap(err, "set delivery")
}

// MarkContactRead records that the customer read the chat up to the
// channel's message id upTo: earlier replies become read (personal
// Telegram accounts report it, docs/02 §10.5).
func (s *Service) MarkContactRead(ctx context.Context, workspaceID, channelID uuid.UUID, thread string, upTo int64) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		rows, err := q.MarkContactRead(ctx, store.MarkContactReadParams{
			WorkspaceID: workspaceID, ChannelID: &channelID, ExternalThreadID: &thread, UpTo: upTo,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := deliveryEvent(ctx, tx, workspaceID, r.ConversationID, r.ID, DeliveryRead, ""); err != nil {
				return err
			}
		}
		return nil
	})
	return wrap(err, "mark read by contact")
}

// Thread is the chat of a conversation in its channel.
type Thread struct {
	ChannelID uuid.UUID
	Thread    string
	// LastInbound: the channel's id of the newest customer message, 0 when
	// there is none or the ids are not numbers.
	LastInbound int64
}

// ConversationThread returns the chat of a channel conversation;
// ErrNotFound for a missing conversation or one without a channel.
func (s *Service) ConversationThread(ctx context.Context, workspaceID, conversationID uuid.UUID) (Thread, error) {
	var t Thread
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		row, err := q.GetConversationThread(ctx, store.GetConversationThreadParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if row.ChannelID == nil || row.ExternalThreadID == nil {
			return ErrNotFound
		}
		t = Thread{ChannelID: *row.ChannelID, Thread: *row.ExternalThreadID, LastInbound: row.LastInbound}
		return nil
	})
	if err != nil {
		return Thread{}, wrap(err, "conversation thread")
	}
	return t, nil
}

// RetryDelivery queues a failed reply again. The actor needs the same
// rights as for replying; pieces delivered before are not sent twice.
func (s *Service) RetryDelivery(ctx context.Context, workspaceID uuid.UUID, actor Actor, conversationID, messageID uuid.UUID) (Message, error) {
	var msg Message
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		conv, err := q.LockConversation(ctx, store.LockConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if err := canPost(conv, actor, false); err != nil {
			return err
		}
		row, err := q.RetryDelivery(ctx, store.RetryDeliveryParams{WorkspaceID: workspaceID, ConversationID: conversationID, ID: messageID})
		if errors.Is(err, pgx.ErrNoRows) {
			exists, err := q.MessageExists(ctx, store.MessageExistsParams{WorkspaceID: workspaceID, ConversationID: conversationID, ID: messageID})
			if err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
			return ErrNotFailed
		}
		if err != nil {
			return err
		}
		if err := s.queueDelivery(ctx, tx, workspaceID, messageID); err != nil {
			return err
		}
		if err := deliveryEvent(ctx, tx, workspaceID, conversationID, messageID, DeliveryPending, ""); err != nil {
			return err
		}
		msgs := []Message{messageFrom(row, nil)}
		if err := attachTo(ctx, q, workspaceID, msgs); err != nil {
			return err
		}
		msg = msgs[0]
		return nil
	})
	if err != nil {
		return Message{}, wrap(err, "retry delivery")
	}
	return msg, nil
}

// queueDelivery hands a reply to the channels module in tx.
func (s *Service) queueDelivery(ctx context.Context, tx pgx.Tx, workspaceID, messageID uuid.UUID) error {
	if s.deliverer == nil {
		return nil // no channels wired (tests of other modules): stays pending
	}
	if err := s.deliverer.QueueDelivery(ctx, tx, workspaceID, messageID); err != nil {
		return fmt.Errorf("queue delivery: %w", err)
	}
	return nil
}

func deliveryEvent(ctx context.Context, tx pgx.Tx, workspaceID, conversationID, messageID uuid.UUID, status, reason string) error {
	data := map[string]any{"conversation_id": conversationID, "message_id": messageID, "status": status, "error": nil}
	if reason != "" {
		data["error"] = reason
	}
	return outbox.Add(ctx, tx, workspaceID, "message.delivery", data)
}
