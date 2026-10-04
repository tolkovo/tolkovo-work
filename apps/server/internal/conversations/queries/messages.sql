-- name: CreateMessage :one
INSERT INTO messages (
    id, workspace_id, conversation_id, direction, sender_type, sender_id, kind,
    body, attachments_count, is_private, delivery_status, idempotency_key, created_at
) VALUES (
    @id, @workspace_id, @conversation_id, @direction, @sender_type, sqlc.narg(sender_id), @kind,
    @body, @attachments_count, @is_private, sqlc.narg(delivery_status), sqlc.narg(idempotency_key), @created_at
)
RETURNING *;

-- name: GetMessageByIdempotencyKey :one
SELECT * FROM messages
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id AND idempotency_key = @idempotency_key;

-- name: ListMessages :many
-- Newest first; the cursor is the id of the last message of the previous
-- page (ids are UUIDv7, so id order is creation order).
SELECT * FROM messages
WHERE workspace_id = @workspace_id
  AND conversation_id = @conversation_id
  AND (sqlc.narg(before_id)::uuid IS NULL OR id < sqlc.narg(before_id)::uuid)
ORDER BY id DESC
LIMIT @lim;

-- name: CreateAttachment :one
INSERT INTO attachments (id, workspace_id, conversation_id, storage_key, file_name, mime, size, sha256, uploaded_by)
VALUES (@id, @workspace_id, @conversation_id, @storage_key, @file_name, @mime, @size, sqlc.narg(sha256), @uploaded_by)
RETURNING *;

-- name: GetAttachment :one
SELECT * FROM attachments
WHERE workspace_id = @workspace_id AND id = @id;

-- name: PendingAttachments :many
-- Uploads of one conversation not yet sent with a message.
SELECT * FROM attachments
WHERE workspace_id = @workspace_id
  AND conversation_id = @conversation_id
  AND id = ANY(@ids::uuid[])
  AND message_id IS NULL
  AND status IN ('uploading', 'ready');

-- name: LinkAttachments :execrows
-- Only rows still unsent are linked, so two messages cannot share a file.
UPDATE attachments
SET message_id = @message_id, status = 'ready', updated_at = now()
WHERE workspace_id = @workspace_id
  AND conversation_id = @conversation_id
  AND id = ANY(@ids::uuid[])
  AND message_id IS NULL
  AND status IN ('uploading', 'ready');

-- name: MessageAttachments :many
-- Attachments of a page of messages in one query (no N+1).
SELECT * FROM attachments
WHERE workspace_id = @workspace_id AND message_id = ANY(@message_ids::uuid[])
ORDER BY created_at, id;
