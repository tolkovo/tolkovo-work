-- name: NextConversationNumber :one
-- The row lock serialises numbering per workspace until the transaction ends.
INSERT INTO conversation_counters (workspace_id, last_number)
VALUES (@workspace_id, 1)
ON CONFLICT (workspace_id) DO UPDATE SET last_number = conversation_counters.last_number + 1
RETURNING last_number;

-- name: CreateConversation :one
INSERT INTO conversations (id, workspace_id, number, contact_id, status, priority, subject)
VALUES (@id, @workspace_id, @number, @contact_id, @status, @priority, sqlc.narg(subject))
RETURNING *;

-- name: GetConversation :one
-- One conversation as user_id sees it: unread counters are per operator.
SELECT sqlc.embed(c), ct.name AS contact_name,
       -- An empty last_kind: no visible message yet (sqlc cannot see that
       -- a LEFT JOIN LATERAL makes columns nullable, hence coalesce).
       coalesce(lm.sender_type, '')::text AS last_sender_type, coalesce(lm.kind, '')::text AS last_kind,
       coalesce(lm.body, '')::text AS last_body, coalesce(lm.attachments_count, 0)::integer AS last_attachments,
       coalesce(lm.created_at, c.created_at)::timestamptz AS last_created_at,
       coalesce(tg.ids, '{}')::uuid[] AS tag_ids,
       rd.last_read_message_id,
       (SELECT count(*) FROM (
           -- Unread for the caller: customer messages after their read mark,
           -- counted up to 100 (the panel shows "99+").
           SELECT 1 FROM messages um
           WHERE um.workspace_id = c.workspace_id AND um.conversation_id = c.id
             AND um.sender_type = 'contact' AND um.deleted_at IS NULL
             AND (rd.last_read_message_id IS NULL OR um.id > rd.last_read_message_id)
           LIMIT 100
       ) u)::integer AS unread_count
FROM conversations c
JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
LEFT JOIN conversation_reads rd
    ON rd.workspace_id = c.workspace_id AND rd.conversation_id = c.id AND rd.user_id = @user_id
LEFT JOIN LATERAL (
    SELECT array_agg(t.tag_id ORDER BY t.created_at, t.tag_id) AS ids
    FROM conversation_tags t
    WHERE t.workspace_id = c.workspace_id AND t.conversation_id = c.id
) tg ON true
LEFT JOIN LATERAL (
    -- The newest message the customer could see: the inbox preview.
    SELECT m.sender_type, m.kind, left(m.body, 200) AS body, m.attachments_count, m.created_at
    FROM messages m
    WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
      AND NOT m.is_private AND m.kind <> 'event' AND m.deleted_at IS NULL
    ORDER BY m.id DESC
    LIMIT 1
) lm ON true
WHERE c.workspace_id = @workspace_id AND c.id = @id;

-- name: LockConversation :one
-- Serialises state changes and new messages of one conversation.
SELECT * FROM conversations
WHERE workspace_id = @workspace_id AND id = @id
FOR UPDATE;

-- name: ListConversations :many
-- Newest activity first, unread counters for user_id. The cursor is (last_message_at, id) of the last
-- row of the previous page. by_assignee with a NULL assignee_id lists the
-- unassigned ones.
SELECT sqlc.embed(c), ct.name AS contact_name,
       -- An empty last_kind: no visible message yet (sqlc cannot see that
       -- a LEFT JOIN LATERAL makes columns nullable, hence coalesce).
       coalesce(lm.sender_type, '')::text AS last_sender_type, coalesce(lm.kind, '')::text AS last_kind,
       coalesce(lm.body, '')::text AS last_body, coalesce(lm.attachments_count, 0)::integer AS last_attachments,
       coalesce(lm.created_at, c.created_at)::timestamptz AS last_created_at,
       coalesce(tg.ids, '{}')::uuid[] AS tag_ids,
       rd.last_read_message_id,
       (SELECT count(*) FROM (
           -- Unread for the caller: customer messages after their read mark,
           -- counted up to 100 (the panel shows "99+").
           SELECT 1 FROM messages um
           WHERE um.workspace_id = c.workspace_id AND um.conversation_id = c.id
             AND um.sender_type = 'contact' AND um.deleted_at IS NULL
             AND (rd.last_read_message_id IS NULL OR um.id > rd.last_read_message_id)
           LIMIT 100
       ) u)::integer AS unread_count
FROM conversations c
JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
LEFT JOIN conversation_reads rd
    ON rd.workspace_id = c.workspace_id AND rd.conversation_id = c.id AND rd.user_id = @user_id
LEFT JOIN LATERAL (
    SELECT array_agg(t.tag_id ORDER BY t.created_at, t.tag_id) AS ids
    FROM conversation_tags t
    WHERE t.workspace_id = c.workspace_id AND t.conversation_id = c.id
) tg ON true
LEFT JOIN LATERAL (
    -- The newest message the customer could see: the inbox preview.
    SELECT m.sender_type, m.kind, left(m.body, 200) AS body, m.attachments_count, m.created_at
    FROM messages m
    WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
      AND NOT m.is_private AND m.kind <> 'event' AND m.deleted_at IS NULL
    ORDER BY m.id DESC
    LIMIT 1
) lm ON true
WHERE c.workspace_id = @workspace_id
  AND (cardinality(@statuses::text[]) = 0 OR c.status = ANY(@statuses::text[]))
  AND (NOT @by_assignee::boolean
       OR (sqlc.narg(assignee_id)::uuid IS NULL AND c.assignee_id IS NULL)
       OR c.assignee_id = sqlc.narg(assignee_id)::uuid)
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (c.last_message_at, c.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY c.last_message_at DESC, c.id DESC
LIMIT @lim;

-- name: CountInbox :one
-- Counters of the inbox tabs (docs/04 §3.1). The status sets mirror the
-- tabs of the panel: mine and the queue are work in progress, open is
-- everything not finished yet.
SELECT
    count(*) FILTER (WHERE assignee_id = @user_id AND status IN ('assigned', 'pending'))::bigint AS mine,
    count(*) FILTER (WHERE assignee_id IS NULL AND status = 'queued')::bigint AS unassigned,
    count(*) FILTER (WHERE status IN ('new', 'bot', 'queued', 'assigned', 'pending'))::bigint AS open
FROM conversations
WHERE workspace_id = @workspace_id AND status IN ('new', 'bot', 'queued', 'assigned', 'pending');

-- name: UpdateConversationState :execrows
-- Applies a transition computed in Go. The version guard makes a lost
-- update impossible even without the row lock (docs/02 §8).
UPDATE conversations
SET status = @status,
    assignee_id = sqlc.narg(assignee_id),
    resolved_at = sqlc.narg(resolved_at),
    closed_at = sqlc.narg(closed_at),
    version = version + 1,
    updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND version = @version;

-- name: TouchConversation :exec
-- Records a new visible message: inbox order and the first response time.
UPDATE conversations
SET last_message_at = @at,
    first_response_at = CASE WHEN @operator_reply::boolean THEN coalesce(first_response_at, @at) ELSE first_response_at END,
    version = version + 1,
    updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: IsActiveMember :one
-- Assignees must be active members. memberships belongs to tenancy; this
-- read-only check sits here so it runs in the same transaction as the
-- assignment.
SELECT EXISTS (
    SELECT 1 FROM memberships m
    WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id AND m.is_active
);

-- name: BumpConversation :exec
-- A change that is not a state transition (tags): clients compare versions.
UPDATE conversations
SET version = version + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;
