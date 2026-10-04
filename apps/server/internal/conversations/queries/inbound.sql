-- Inbound messages from channels (T-02, docs/02 §11.2).

-- name: GetIdentity :one
SELECT * FROM contact_identities
WHERE workspace_id = @workspace_id AND channel_id = @channel_id AND external_user_id = @external_user_id;

-- name: CreateIdentity :one
INSERT INTO contact_identities (id, workspace_id, contact_id, channel_id, external_user_id, username)
VALUES (@id, @workspace_id, @contact_id, @channel_id, @external_user_id, sqlc.narg(username))
RETURNING *;

-- name: UpdateIdentityUsername :exec
UPDATE contact_identities
SET username = sqlc.narg(username), updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: LockOpenThread :one
-- The open conversation of an external chat, locked: messages of one chat
-- are stored one at a time, in order (docs/02 §11.2).
SELECT * FROM conversations
WHERE workspace_id = @workspace_id AND channel_id = @channel_id AND external_thread_id = @external_thread_id
  AND status <> 'closed'
FOR UPDATE;

-- name: OpenThreadExists :one
SELECT EXISTS (
    SELECT 1 FROM conversations
    WHERE workspace_id = @workspace_id AND channel_id = @channel_id AND external_thread_id = @external_thread_id
      AND status <> 'closed'
);

-- name: ThreadMessageExists :one
-- A message of the chat is stored already, in any of its conversations.
SELECT EXISTS (
    SELECT 1 FROM messages m
    JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
    WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @external_thread_id
      AND m.external_id = @external_id
);

-- name: CreateThreadConversation :one
INSERT INTO conversations (id, workspace_id, number, contact_id, channel_id, external_thread_id, status)
VALUES (@id, @workspace_id, @number, @contact_id, @channel_id, @external_thread_id, @status)
RETURNING *;

-- name: CreateInboundMessage :one
INSERT INTO messages (
    id, workspace_id, conversation_id, direction, sender_type, sender_id, kind,
    body, attachments_count, external_id, delivery_status, meta, created_at
) VALUES (
    @id, @workspace_id, @conversation_id, @direction, @sender_type, sqlc.narg(sender_id), @kind,
    @body, @attachments_count, @external_id, sqlc.narg(delivery_status), @meta, @created_at
)
RETURNING *;

-- name: CreateReceivedAttachment :one
-- A file the channel fetched before the transaction; rejected ones have no
-- object in storage.
INSERT INTO attachments (id, workspace_id, conversation_id, message_id, storage_key, file_name, mime, size, status)
VALUES (@id, @workspace_id, @conversation_id, @message_id, @storage_key, @file_name, @mime, @size, @status)
RETURNING *;

-- name: ThreadMessageConversation :one
-- The conversation that holds a message of the chat (a retried report).
SELECT m.conversation_id FROM messages m
JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @external_thread_id
  AND m.external_id = @external_id
LIMIT 1;

-- name: LockExternalID :exec
-- Serialises the first reports of one verified user, so they find one
-- contact instead of creating two.
SELECT pg_advisory_xact_lock(hashtextextended(@workspace_id::text || ':' || @external_id::text, 0));

-- name: ContactByExternalID :one
-- The verified contact of an app or site user; the oldest wins if an
-- operator created a second one by hand.
SELECT * FROM contacts
WHERE workspace_id = @workspace_id AND external_id = @external_id AND deleted_at IS NULL
ORDER BY id
LIMIT 1;

-- name: FillContact :exec
-- A verified report fills what the contact lacks; it never overwrites
-- what an operator entered.
UPDATE contacts
SET name = CASE WHEN name = '' THEN @name::text ELSE name END,
    email = COALESCE(email, sqlc.narg(email)::citext),
    updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id
  AND (name = '' AND @name::text <> '' OR email IS NULL AND sqlc.narg(email)::citext IS NOT NULL);

-- name: ExpireAttachments :execrows
-- Files past the retention of their channel: the objects are deleted, the
-- rows stay so the conversation shows that a file was there.
UPDATE attachments
SET status = 'expired', updated_at = now()
WHERE workspace_id = @workspace_id AND storage_key = ANY(@storage_keys::text[]) AND status <> 'expired';
