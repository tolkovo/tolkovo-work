package channels

import (
	"context"
	"errors"
	"io"
	"mime"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/channels/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/channels/telegram"
	"github.com/tolkovo/tolkovo/apps/server/internal/conversations"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// File limits of received media: the Bot API gives files up to 20 MB, and
// no attachment is larger than conversations.MaxAttachmentSize.
const (
	BotFileLimit  = 20 << 20
	UserFileLimit = conversations.MaxAttachmentSize
)

// Inbox stores received messages and the delivery of replies
// (conversations.Service).
type Inbox interface {
	InboundState(ctx context.Context, workspaceID, channelID uuid.UUID, thread, externalID string) (seen, open bool, err error)
	Receive(ctx context.Context, workspaceID uuid.UUID, in conversations.Inbound) (conversations.Received, error)
	// Replies (T-03).
	Outgoing(ctx context.Context, workspaceID, messageID uuid.UUID) (conversations.Outgoing, error)
	AddSentPart(ctx context.Context, workspaceID, messageID uuid.UUID, externalID string) error
	SetDelivery(ctx context.Context, workspaceID, messageID uuid.UUID, d conversations.Delivery) error
	MarkContactRead(ctx context.Context, workspaceID, channelID uuid.UUID, thread string, upTo int64) error
	ConversationThread(ctx context.Context, workspaceID, conversationID uuid.UUID) (conversations.Thread, error)
}

// Files keeps received media and gives the files of replies
// (storage.Storage).
type Files interface {
	Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error)
	Open(ctx context.Context, key string) (io.ReadCloser, storage.Object, error)
	Delete(ctx context.Context, key string) error
}

// fetchFunc downloads a file of a message through its connection.
type fetchFunc func(ctx context.Context, f telegram.File, w io.Writer, limit int64) error

// deliver stores one received message: files go to storage first (no
// network inside the transaction), then conversations records it. A
// message seen before, or an owner's message without an open conversation,
// is dropped before anything is downloaded.
func (s *Service) deliver(ctx context.Context, workspaceID, channelID uuid.UUID, m telegram.Message, fetch fetchFunc, limit int64) error {
	thread, ext := threadOf(m.ChatID), strconv.FormatInt(m.ID, 10)
	seen, open, err := s.opts.Inbox.InboundState(ctx, workspaceID, channelID, thread, ext)
	if err != nil {
		return err
	}
	if seen || (m.Out && !open) {
		return nil
	}
	in := conversations.Inbound{
		ChannelID: channelID, Thread: thread, ExternalID: ext, FromOwner: m.Out,
		Sender: conversations.InboundSender{
			ExternalUserID: strconv.FormatInt(m.From.ID, 10), Name: m.From.Name(), Username: m.From.Username,
		},
		Body: m.Text, Unsupported: m.Unsupported,
	}
	var stored []string
	for _, f := range m.Files {
		rf := s.fetchFile(ctx, workspaceID, channelID, f, fetch, limit)
		if rf.StorageKey != "" {
			stored = append(stored, rf.StorageKey)
		}
		in.Files = append(in.Files, rf)
	}
	res, err := s.opts.Inbox.Receive(ctx, workspaceID, in)
	if err != nil || !res.Stored {
		s.dropFiles(ctx, stored)
		return err
	}
	s.markInbound(ctx, workspaceID, channelID)
	return nil
}

// fetchFile streams a file from Telegram into storage. A file that is too
// large or cannot be fetched becomes a rejected attachment: the operator
// sees that the customer sent something.
func (s *Service) fetchFile(
	ctx context.Context, workspaceID, channelID uuid.UUID, f telegram.File, fetch fetchFunc, limit int64,
) conversations.ReceivedFile {
	ct := f.MIME
	if _, _, err := mime.ParseMediaType(ct); err != nil || ct == "" {
		ct = "application/octet-stream"
	}
	rf := conversations.ReceivedFile{FileName: f.Name, ContentType: ct, Size: f.Size, Rejected: true}
	if s.opts.Files == nil || f.Size > limit {
		return rf
	}
	key, err := storage.NewKey(workspaceID, storage.KindConversation)
	if err != nil {
		return rf
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(fetch(ctx, f, pw, limit)) }()
	obj, err := s.opts.Files.Put(ctx, key, pr, storage.PutOptions{ContentType: ct, MaxSize: limit})
	_ = pr.CloseWithError(errors.New("storage stopped reading"))
	if err != nil {
		s.log.Warn("telegram file not received", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
		s.dropFiles(ctx, []string{key})
		return rf
	}
	rf.StorageKey, rf.Size, rf.Rejected = key, obj.Size, false
	return rf
}

func (s *Service) dropFiles(ctx context.Context, keys []string) {
	ctx = context.WithoutCancel(ctx)
	for _, k := range keys {
		_ = s.opts.Files.Delete(ctx, k)
	}
}

// markInbound records that the channel works, best effort.
func (s *Service) markInbound(ctx context.Context, workspaceID, channelID uuid.UUID) {
	err := s.db.InTenantTx(ctx, workspaceID, func(tx pgx.Tx) error {
		now := s.now()
		return store.New(tx).MarkInbound(ctx, store.MarkInboundParams{WorkspaceID: workspaceID, ID: channelID, LastInboundAt: &now})
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("mark channel inbound", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
	}
}

// deliverRetrying retries a failed delivery (the database or storage is
// briefly away) until ctx ends: Telegram does not resend a message once
// the receiver moved past it. Invalid messages are skipped.
func (s *Service) deliverRetrying(ctx context.Context, workspaceID, channelID uuid.UUID, m telegram.Message, fetch fetchFunc, limit int64) error {
	wait := time.Second
	for {
		err := s.deliver(ctx, workspaceID, channelID, m, fetch, limit)
		if err == nil || ctx.Err() != nil {
			return err
		}
		if errors.Is(err, conversations.ErrInvalid) {
			s.log.Warn("telegram message skipped", "workspace_id", workspaceID, "channel_id", channelID, "error", err)
			return nil
		}
		s.log.Warn("telegram message not stored, retrying", "workspace_id", workspaceID, "channel_id", channelID, "error", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}
