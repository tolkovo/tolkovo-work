-- name: BotLines :many
-- What the bot reads of a conversation: what the customer, the bot and
-- operators wrote to each other, the newest first. Notes and events stay out.
SELECT id, sender_type, body, attachments_count, created_at FROM messages
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
  AND sender_type IN ('contact', 'operator', 'bot') AND kind IN ('text', 'media')
  AND NOT is_private AND deleted_at IS NULL
ORDER BY id DESC
LIMIT @lim;

-- name: CreateBotMessage :one
-- A reply of the bot (sender_id NULL) or its note for operators.
INSERT INTO messages (
    id, workspace_id, conversation_id, direction, sender_type, kind, body, is_private, delivery_status, created_at
) VALUES (
    @id, @workspace_id, @conversation_id, 'out', 'bot', @kind, @body, @is_private, sqlc.narg(delivery_status), @created_at
)
RETURNING *;
