-- Operator replies on their way to a channel (T-03, docs/02 §11.4).

-- name: GetOutgoing :one
-- waiting: an earlier reply of the conversation is not delivered yet, and
-- this one must not overtake it. Only replies of the last two hours count:
-- retries end after about 40 minutes, so an older pending reply lost its
-- job (or was retried by hand long after) and must not hold the rest.
SELECT sqlc.embed(m), c.channel_id AS thread_channel_id, c.external_thread_id,
    EXISTS (
        SELECT 1 FROM messages p
        WHERE p.workspace_id = m.workspace_id AND p.conversation_id = m.conversation_id
          AND p.delivery_status = 'pending' AND p.id < m.id
          AND p.created_at > m.created_at - interval '2 hours'
    ) AS waiting
FROM messages m
JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
WHERE m.workspace_id = @workspace_id AND m.id = @id;

-- name: AddSentPart :execrows
-- One more piece of a reply reached the channel (a long text is split, every
-- file goes on its own): a retry continues after it.
UPDATE messages
SET meta = jsonb_set(meta, '{sent_parts}', COALESCE(meta->'sent_parts', '[]'::jsonb) || to_jsonb(@part::text))
WHERE workspace_id = @workspace_id AND id = @id AND delivery_status = 'pending';

-- name: SetDelivery :one
-- Only a pending reply moves on: a late worker cannot undo a later state.
UPDATE messages
SET delivery_status = @delivery_status,
    delivery_error = sqlc.narg(delivery_error),
    external_id = COALESCE(sqlc.narg(external_id), external_id)
WHERE workspace_id = @workspace_id AND id = @id AND delivery_status = 'pending'
RETURNING conversation_id;

-- name: RetryDelivery :one
UPDATE messages
SET delivery_status = 'pending', delivery_error = NULL
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id AND id = @id
  AND delivery_status = 'failed'
RETURNING *;

-- name: MarkContactRead :many
-- The customer read the chat up to a Telegram message id: replies sent
-- before it are read. Ids are compared as numbers; CASE keeps the cast away
-- from ids that are not numbers.
UPDATE messages m
SET delivery_status = 'read'
FROM conversations c
WHERE c.workspace_id = @workspace_id AND c.channel_id = @channel_id AND c.external_thread_id = @external_thread_id
  AND m.workspace_id = c.workspace_id AND m.conversation_id = c.id
  AND m.direction = 'out' AND m.delivery_status = 'sent'
  AND (CASE WHEN m.external_id ~ '^[0-9]{1,18}$' THEN m.external_id::bigint END) <= @up_to::bigint
RETURNING m.id, m.conversation_id;

-- name: GetConversationThread :one
-- The chat of a conversation and the newest customer message in it.
SELECT c.channel_id, c.external_thread_id,
    COALESCE((
        SELECT max(CASE WHEN m.external_id ~ '^[0-9]{1,18}$' THEN m.external_id::bigint END)
        FROM messages m
        WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id AND m.direction = 'in'
    ), 0)::bigint AS last_inbound
FROM conversations c
WHERE c.workspace_id = @workspace_id AND c.id = @id;
