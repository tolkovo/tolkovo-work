package conversations

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/tolkovo/tolkovo/apps/server/internal/conversations/store"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/storage"
)

// Attachment statuses (docs/02 §7.2). Phase 1 uses uploading and ready;
// scanning and rejected arrive with the optional antivirus (C-02).
const (
	AttachmentUploading = "uploading"
	AttachmentReady     = "ready"
)

// allowedTypes is the whitelist of attachment content types operators may
// upload (docs/00 §9.4). Downloads always come as attachments, so even an
// allowed type never renders as a page.
var allowedTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true, "text/plain": true, "text/csv": true,
	"application/json": true, "application/zip": true, "application/gzip": true,
	"application/msword": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
	"application/vnd.ms-excel": true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": true,
}

// Attachment is a file of a message. The storage key is internal: clients
// get short-lived signed links instead.
type Attachment struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	MessageID      *uuid.UUID
	FileName       string
	ContentType    string
	Size           int64
	Status         string
	CreatedAt      time.Time
}

// UploadInput describes a file the client is about to upload.
type UploadInput struct {
	FileName    string
	ContentType string
	Size        int64
	SHA256      []byte // optional; when set the storage enforces it
}

// Upload is a registered attachment and the signed request that uploads
// its file.
type Upload struct {
	Attachment Attachment
	Request    storage.SignedRequest
}

// CreateUpload registers a file for the conversation and returns a signed
// PUT for it (docs/02 §5.4). The key is fresh and inside the workspace's
// prefix; the link fixes the size, the type and, when given, the digest.
// The file becomes part of a message when PostMessage lists its id.
func (s *Service) CreateUpload(
	ctx context.Context, workspaceID uuid.UUID, actor Actor, conversationID uuid.UUID, in UploadInput,
) (Upload, error) {
	ct, err := in.validate()
	if err != nil {
		return Upload{}, err
	}
	key, err := storage.NewKey(workspaceID, storage.KindConversation)
	if err != nil {
		return Upload{}, err
	}
	id, err := newID()
	if err != nil {
		return Upload{}, err
	}
	var row store.Attachment
	err = s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		conv, err := q.GetConversation(ctx, store.GetConversationParams{WorkspaceID: workspaceID, ID: conversationID})
		if err != nil {
			return err
		}
		if Status(conv.Conversation.Status) == StatusClosed {
			return ErrClosed
		}
		row, err = q.CreateAttachment(ctx, store.CreateAttachmentParams{
			ID: id, WorkspaceID: workspaceID, ConversationID: conversationID, StorageKey: key,
			FileName: storage.SanitizeFilename(in.FileName), Mime: ct, Size: in.Size,
			Sha256: in.SHA256, UploadedBy: &actor.UserID,
		})
		return err
	})
	if err != nil {
		return Upload{}, wrap(err, "create upload")
	}
	// Signing is local for both drivers, but stays outside the transaction
	// like every storage call.
	req, err := s.files.PresignPut(ctx, key, storage.PresignPutOptions{
		ContentType: ct, MaxSize: MaxAttachmentSize, Size: in.Size, SHA256: in.SHA256,
	})
	if err != nil {
		return Upload{}, fmt.Errorf("presign upload: %w", err)
	}
	return Upload{Attachment: attachmentFrom(row), Request: req}, nil
}

// DownloadLink returns a signed GET for a sent attachment.
func (s *Service) DownloadLink(ctx context.Context, workspaceID, id uuid.UUID) (storage.SignedRequest, error) {
	var row store.Attachment
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		row, err = q.GetAttachment(ctx, store.GetAttachmentParams{WorkspaceID: workspaceID, ID: id})
		return err
	})
	if err != nil {
		return storage.SignedRequest{}, wrap(err, "get attachment")
	}
	if row.Status != AttachmentReady {
		return storage.SignedRequest{}, ErrNotFound
	}
	req, err := s.files.PresignGet(ctx, row.StorageKey, storage.PresignGetOptions{
		Filename: row.FileName, ContentType: row.Mime,
	})
	if err != nil {
		return storage.SignedRequest{}, fmt.Errorf("presign download: %w", err)
	}
	return req, nil
}

// AttachmentConversation returns the conversation of an attachment, so
// the api can check that the caller sees it before DownloadLink.
func (s *Service) AttachmentConversation(ctx context.Context, workspaceID, id uuid.UUID) (uuid.UUID, error) {
	var row store.Attachment
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		row, err = q.GetAttachment(ctx, store.GetAttachmentParams{WorkspaceID: workspaceID, ID: id})
		return err
	})
	if err != nil {
		return uuid.Nil, wrap(err, "get attachment")
	}
	return row.ConversationID, nil
}

// checkUploads verifies that every listed attachment is an unsent upload of
// the conversation and that its file is in storage with the declared size.
// It runs before the message transaction; LinkAttachments re-checks the
// rows inside it, so a concurrent send cannot reuse a file.
func (s *Service) checkUploads(ctx context.Context, workspaceID, conversationID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	var rows []store.Attachment
	err := s.tx(ctx, workspaceID, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		rows, err = q.PendingAttachments(ctx, store.PendingAttachmentsParams{
			WorkspaceID: workspaceID, ConversationID: conversationID, Ids: ids,
		})
		return err
	})
	if err != nil {
		return wrap(err, "load attachments")
	}
	if len(rows) != len(ids) {
		return fmt.Errorf("%w: an attachment is unknown or already sent", ErrInvalid)
	}
	for _, a := range rows {
		obj, err := s.files.Stat(ctx, a.StorageKey)
		if errors.Is(err, storage.ErrNotFound) || (err == nil && obj.Size != a.Size) {
			return ErrNotUploaded
		}
		if err != nil {
			return fmt.Errorf("stat attachment: %w", err)
		}
	}
	return nil
}

func (in UploadInput) validate() (string, error) {
	ct, _, err := mime.ParseMediaType(in.ContentType)
	switch {
	case in.FileName == "" || len(in.FileName) > 1024:
		return "", fmt.Errorf("%w: file_name must be 1-1024 bytes", ErrInvalid)
	case err != nil || !allowedTypes[ct]:
		return "", fmt.Errorf("%w: content type is not allowed", ErrInvalid)
	case in.Size <= 0 || in.Size > MaxAttachmentSize:
		return "", fmt.Errorf("%w: size must be between 1 and %d bytes", ErrInvalid, MaxAttachmentSize)
	case len(in.SHA256) != 0 && len(in.SHA256) != 32:
		return "", fmt.Errorf("%w: sha256 must be 32 bytes", ErrInvalid)
	}
	return ct, nil
}

func attachmentFrom(a store.Attachment) Attachment {
	return Attachment{
		ID: a.ID, ConversationID: a.ConversationID, MessageID: a.MessageID, FileName: a.FileName,
		ContentType: a.Mime, Size: a.Size, Status: a.Status, CreatedAt: a.CreatedAt,
	}
}
