package conversations

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// A widget visitor's side of their chat (docs/04 §6, W-01). The chat is the
// thread of the visitor's contact in a widget channel: every conversation
// of that thread, oldest first, as one feed. The widget module
// (internal/widget) has authenticated the visitor and passes the thread.

// MaxVisitorMessages is the most messages one call returns.
const MaxVisitorMessages = 100

// visitorSettle: the cursor moves past a message only this long after it
// is stored, when every transaction that took a smaller id has committed.
// Younger messages are returned anyway and come again with the next call;
// the widget drops repeats by id. A live chat cannot wait for them.
const visitorSettle = 5 * time.Second

// VisitorChat names a visitor's chat.
type VisitorChat struct {
	ChannelID uuid.UUID
	Thread    string
}

// VisitorMessage is a message the visitor sees: theirs or a reply. No staff
// names, no notes, no events.
type VisitorMessage struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	SenderType     string // SenderContact, SenderOperator or SenderBot
	Body           string
	CreatedAt      time.Time
	Files          []ReplyFile
}

// VisitorMessages is a page of the visitor's chat.
type VisitorMessages struct {
	Items []VisitorMessage
	// Cursor is where the next call starts; since when nothing settled.
	Cursor string
	// More: more messages follow the page; ask again at once.
	More bool
	// Conversation is the visitor's newest conversation; nil before the
	// first message.
	Conversation *VisitorConversation
}

// VisitorMessages returns the visitor's chat after since, oldest first;
// without since, the newest MaxVisitorMessages.
func (s *Service) VisitorMessages(ctx context.Context, workspaceID uuid.UUID, chat VisitorChat, since string) (VisitorMessages, error) {
	after, err := decodeIDCursor(since)
	if err != nil {
		return VisitorMessages{}, err
	}
	var (
		rows []store.Message
		atts []store.Attachment
		conv *VisitorConversation
	)
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		if conv, err = visitorConversation(ctx, q, workspaceID, chat); err != nil {
			return err
		}
		if after == nil {
			rows, err = q.VisitorMessagesLatest(ctx, store.VisitorMessagesLatestParams{
				WorkspaceID: workspaceID, ChannelID: &chat.ChannelID, Thread: &chat.Thread, Lim: MaxVisitorMessages,
			})
			slices.Reverse(rows)
		} else {
			rows, err = q.VisitorMessagesAfter(ctx, store.VisitorMessagesAfterParams{
				WorkspaceID: workspaceID, ChannelID: &chat.ChannelID, Thread: &chat.Thread, AfterID: *after, Lim: MaxVisitorMessages + 1,
			})
		}
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
		return VisitorMessages{}, wrap(err, "list visitor messages")
	}
	out := VisitorMessages{Cursor: since, Items: []VisitorMessage{}, More: after != nil && len(rows) > MaxVisitorMessages, Conversation: conv}
	rows = rows[:min(len(rows), MaxVisitorMessages)]
	byMessage := make(map[uuid.UUID][]store.Attachment, len(atts))
	for _, a := range atts {
		if a.Status == AttachmentReady {
			byMessage[*a.MessageID] = append(byMessage[*a.MessageID], a)
		}
	}
	settled := s.now().Add(-visitorSettle)
	for _, m := range rows {
		v := VisitorMessage{
			ID: m.ID, ConversationID: m.ConversationID, SenderType: m.SenderType, Body: m.Body, CreatedAt: m.CreatedAt,
		}
		if v.Files, err = s.visitorFiles(ctx, byMessage[m.ID]); err != nil {
			return VisitorMessages{}, err
		}
		out.Items = append(out.Items, v)
		// With More the next page starts right after this one, settled or
		// not: what follows is older than the page's end anyway.
		if m.CreatedAt.Before(settled) || out.More {
			out.Cursor = encodeIDCursor(m.ID)
		}
	}
	return out, nil
}

// VisitorReceipt is what the visitor's widget reports: it received the
// replies up to DeliveredUpTo and showed them up to ReadUpTo. Either may
// be uuid.Nil.
type VisitorReceipt struct {
	DeliveredUpTo uuid.UUID
	ReadUpTo      uuid.UUID
}

// MarkVisitorReceipt moves the replies of the visitor's chat to delivered
// and read (docs/02 §10.2, message.delivery). A status never moves back,
// so a late report changes nothing.
func (s *Service) MarkVisitorReceipt(ctx context.Context, workspaceID uuid.UUID, chat VisitorChat, r VisitorReceipt) error {
	err := s.tx(ctx, workspaceID, func(q *store.Queries, tx pgx.Tx) error {
		for _, step := range []struct {
			status string
			upTo   uuid.UUID
		}{{DeliveryRead, r.ReadUpTo}, {DeliveryDelivered, r.DeliveredUpTo}} {
			if step.upTo == uuid.Nil {
				continue
			}
			status := step.status
			rows, err := q.MarkVisitorReceipt(ctx, store.MarkVisitorReceiptParams{
				Status: &status, WorkspaceID: workspaceID, ChannelID: &chat.ChannelID, Thread: &chat.Thread, UpTo: step.upTo,
			})
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := deliveryEvent(ctx, tx, workspaceID, row.ConversationID, row.ID, status, ""); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return wrap(err, "mark visitor receipt")
}

// VisitorMessageByKey returns the visitor's own message stored with the
// channel's external id (a retried send); ErrNotFound if there is none.
func (s *Service) VisitorMessageByKey(ctx context.Context, workspaceID uuid.UUID, chat VisitorChat, externalID string) (VisitorMessage, error) {
	var (
		m    store.Message
		atts []store.Attachment
	)
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		m, err = q.VisitorMessageByExternalID(ctx, store.VisitorMessageByExternalIDParams{
			WorkspaceID: workspaceID, ChannelID: &chat.ChannelID, Thread: &chat.Thread, ExternalID: &externalID,
		})
		if err != nil {
			return err
		}
		atts, err = q.MessageAttachments(ctx, store.MessageAttachmentsParams{WorkspaceID: workspaceID, MessageIds: []uuid.UUID{m.ID}})
		return err
	})
	if err != nil {
		return VisitorMessage{}, wrap(err, "visitor message by key")
	}
	ready := atts[:0]
	for _, a := range atts {
		if a.Status == AttachmentReady {
			ready = append(ready, a)
		}
	}
	files, err := s.visitorFiles(ctx, ready)
	if err != nil {
		return VisitorMessage{}, err
	}
	return VisitorMessage{
		ID: m.ID, ConversationID: m.ConversationID, SenderType: m.SenderType, Body: m.Body, CreatedAt: m.CreatedAt, Files: files,
	}, nil
}

// visitorFiles signs downloads of a message's ready files.
func (s *Service) visitorFiles(ctx context.Context, atts []store.Attachment) ([]ReplyFile, error) {
	files := make([]ReplyFile, 0, len(atts))
	for _, a := range atts {
		req, err := s.files.PresignGet(ctx, a.StorageKey, storage.PresignGetOptions{Filename: a.FileName, ContentType: a.Mime})
		if err != nil {
			return nil, fmt.Errorf("presign visitor file: %w", err)
		}
		files = append(files, ReplyFile{FileName: a.FileName, ContentType: a.Mime, Size: a.Size, Download: req})
	}
	return files, nil
}
