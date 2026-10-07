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

-- name: CreateEventMessage :one
-- A system line of the feed (status change, assignment). The panel renders
-- it from meta in the reader's language; body stays empty.
INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, sender_id, kind, meta, created_at)
VALUES (@id, @workspace_id, @conversation_id, 'out', 'system', sqlc.narg(sender_id), 'event', @meta, @created_at)
RETURNING *;

-- name: MessageExists :one
SELECT EXISTS (
    SELECT 1 FROM messages
    WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id AND id = @id
);

-- name: MarkRead :execrows
-- Moves the caller's read mark forward only: a late request from another
-- tab must not mark newer messages unread again.
INSERT INTO conversation_reads (workspace_id, conversation_id, user_id, last_read_message_id, read_at)
VALUES (@workspace_id, @conversation_id, @user_id, @message_id, @read_at)
ON CONFLICT (workspace_id, conversation_id, user_id) DO UPDATE
SET last_read_message_id = EXCLUDED.last_read_message_id, read_at = EXCLUDED.read_at
WHERE conversation_reads.last_read_message_id < EXCLUDED.last_read_message_id;

-- name: CreateKeyedEventMessage :many
-- A system line recorded once per key: a retried job does not repeat it.
-- No row back means it is there already.
INSERT INTO messages (id, workspace_id, conversation_id, direction, sender_type, sender_id, kind, meta, idempotency_key, created_at)
VALUES (@id, @workspace_id, @conversation_id, 'out', 'system', sqlc.narg(sender_id), 'event', @meta, @idempotency_key, @created_at)
ON CONFLICT (workspace_id, conversation_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
RETURNING id;

-- name: FirstCustomerText :many
-- The customer's first message with text and its meta (the app report):
-- what an issue draft starts from.
SELECT body, meta FROM messages
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
  AND sender_type = 'contact' AND body <> '' AND deleted_at IS NULL
ORDER BY id
LIMIT 1;

-- name: ReplySource :one
-- Who a reply about a fix is drafted for, and when an operator last wrote
-- to them (the conversation's start when never): the draft is offered
-- until the next reply.
SELECT ct.name AS contact_name, ct.locale AS contact_locale,
       coalesce((
           SELECT max(m.created_at) FROM messages m
           WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
             AND m.sender_type = 'operator' AND NOT m.is_private AND m.kind <> 'event' AND m.deleted_at IS NULL
       ), c.created_at)::timestamptz AS last_reply_at
FROM conversations c
JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
WHERE c.workspace_id = @workspace_id AND c.id = @id;

-- name: RecentCustomerTexts :many
-- What the customer wrote, the newest first: what a draft of a task or an
-- issue is written from.
SELECT body FROM messages
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
  AND sender_type = 'contact' AND kind = 'text' AND body <> '' AND deleted_at IS NULL
ORDER BY id DESC
LIMIT @lim;
