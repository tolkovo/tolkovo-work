-- name: VisitorMessagesAfter :many
-- What a widget visitor sees of their chat (W-01): their own messages and
-- the replies of operators and the bot in every conversation of their
-- thread, oldest first after the cursor. Notes and events stay inside.
SELECT m.* FROM messages m
JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @thread
  AND m.workspace_id = @workspace_id
  AND m.sender_type IN ('contact', 'operator', 'bot') AND m.kind IN ('text', 'media')
  AND NOT m.is_private AND m.deleted_at IS NULL
  AND m.id > @after_id::uuid
ORDER BY m.id
LIMIT @lim;

-- name: VisitorMessagesLatest :many
-- The newest messages of the visitor's chat, newest first: the widget
-- opens on them.
SELECT m.* FROM messages m
JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @thread
  AND m.workspace_id = @workspace_id
  AND m.sender_type IN ('contact', 'operator', 'bot') AND m.kind IN ('text', 'media')
  AND NOT m.is_private AND m.deleted_at IS NULL
ORDER BY m.id DESC
LIMIT @lim;

-- name: MarkVisitorReceipt :many
-- The visitor's widget got (delivered) or showed (read) the replies up to
-- a message id: replies of the thread not yet at that status move to it,
-- never back (read stays read). A reply may still be pending: the push
-- reaches the widget before the delivery job marks it sent, and the job
-- then leaves it alone. Message ids are UUIDv7, so they order by time.
UPDATE messages m
SET delivery_status = @status
FROM conversations c
WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @thread
  AND m.workspace_id = c.workspace_id AND m.conversation_id = c.id
  AND m.direction = 'out' AND m.id <= @up_to::uuid
  AND (m.delivery_status IN ('pending', 'sent') OR (@status = 'read' AND m.delivery_status = 'delivered'))
RETURNING m.id, m.conversation_id;

-- name: VisitorMessageByExternalID :one
-- A message of the visitor's chat by its key, for a retried send.
SELECT m.* FROM messages m
JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @thread
  AND m.workspace_id = @workspace_id AND m.external_id = @external_id AND m.direction = 'in'
LIMIT 1;

-- name: VisitorConversation :one
-- The newest conversation of the visitor's chat: whether to ask for a
-- rating.
SELECT * FROM conversations
WHERE workspace_id = @workspace_id AND channel_id = @channel_id AND external_thread_id = @thread
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: LockVisitorConversation :one
-- A conversation of the visitor's chat, locked for their rating; none for
-- a conversation of anyone else.
SELECT * FROM conversations
WHERE workspace_id = @workspace_id AND id = @id AND channel_id = @channel_id AND external_thread_id = @thread
FOR UPDATE;

-- name: ConversationRepliers :one
-- Who replied in a conversation: operators, the bot, or both.
SELECT
    EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = @workspace_id AND m.conversation_id = @conversation_id
            AND m.sender_type = 'operator' AND m.deleted_at IS NULL) AS operator,
    EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = @workspace_id AND m.conversation_id = @conversation_id
            AND m.sender_type = 'bot' AND m.deleted_at IS NULL) AS bot;

-- name: SetConversationRating :exec
UPDATE conversations
SET rating = @rating::smallint, rating_comment = sqlc.narg(comment), rating_resolved_by = @resolved_by::text,
    rated_at = @rated_at::timestamptz, updated_at = @rated_at::timestamptz
WHERE workspace_id = @workspace_id AND id = @id;
