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

-- name: LockQueue :many
-- The head of the queue for the router (docs/02 §9.2): higher priority
-- first, then the oldest. Rows another transaction holds are skipped, so
-- an operator taking one by hand never waits for the router. Only
-- conversations someone of available may take: a topic with members goes
-- to them alone, so one nobody online may take does not block the rest.
SELECT * FROM conversations c
WHERE c.workspace_id = @workspace_id AND c.status = 'queued'
  AND (c.topic_id IS NULL
       OR NOT EXISTS (SELECT 1 FROM topic_members tm WHERE tm.workspace_id = c.workspace_id AND tm.topic_id = c.topic_id)
       OR EXISTS (SELECT 1 FROM topic_members tm WHERE tm.workspace_id = c.workspace_id AND tm.topic_id = c.topic_id
                    AND tm.user_id = ANY(@available::uuid[])))
ORDER BY c.priority DESC, c.created_at, c.id
LIMIT @lim
FOR UPDATE OF c SKIP LOCKED;

-- name: LockOpenOf :many
-- The open conversations of a member whose role changes or who leaves the
-- team (ADR-0020), oldest first.
SELECT * FROM conversations
WHERE workspace_id = @workspace_id AND assignee_id = @user_id AND status IN ('assigned', 'pending')
ORDER BY created_at, id
FOR UPDATE;

-- name: CountOpenOf :many
-- The load of each of user_ids: their conversations in work, assigned or
-- pending (ADR-0021). Served by conversations_assignee_idx.
SELECT assignee_id::uuid AS user_id, count(*)::integer AS open
FROM conversations
WHERE workspace_id = @workspace_id AND assignee_id = ANY(@user_ids::uuid[]) AND status IN ('assigned', 'pending')
GROUP BY assignee_id;

-- name: HasQueued :one
-- Whether the queue still holds something someone of available may take,
-- locked rows included.
SELECT EXISTS (
    SELECT 1 FROM conversations c
    WHERE c.workspace_id = @workspace_id AND c.status = 'queued'
      AND (c.topic_id IS NULL
           OR NOT EXISTS (SELECT 1 FROM topic_members tm WHERE tm.workspace_id = c.workspace_id AND tm.topic_id = c.topic_id)
           OR EXISTS (SELECT 1 FROM topic_members tm WHERE tm.workspace_id = c.workspace_id AND tm.topic_id = c.topic_id
                        AND tm.user_id = ANY(@available::uuid[])))
);

-- name: ListConversations :many
-- Newest activity first, unread counters for user_id. The cursor is (last_message_at, id) of the last
-- row of the previous page. by_assignee with a NULL assignee_id lists the
-- unassigned ones, by_topic with a NULL topic_id those without a topic.
-- contact_id narrows it to one customer's history (served by
-- conversations_contact_idx: a contact has few conversations to sort).
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
  AND (sqlc.narg(contact_id)::uuid IS NULL OR c.contact_id = sqlc.narg(contact_id)::uuid)
  AND (NOT @by_topic::boolean
       OR (sqlc.narg(topic_id)::uuid IS NULL AND c.topic_id IS NULL)
       OR c.topic_id = sqlc.narg(topic_id)::uuid)
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (c.last_message_at, c.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY c.last_message_at DESC, c.id DESC
LIMIT @lim;

-- name: CountInbox :one
-- Counters of the inbox tabs (docs/04 §3.1). The status sets mirror the
-- tabs of the panel: mine and the queue are work in progress, open is
-- everything not finished yet.
-- mine_unread: the caller's conversations with a customer message after
-- their read mark (the "(3)" in the tab title, docs/04 §5.5).
SELECT
    count(*) FILTER (WHERE c.assignee_id = @user_id AND c.status IN ('assigned', 'pending'))::bigint AS mine,
    count(*) FILTER (WHERE c.assignee_id = @user_id AND c.status IN ('assigned', 'pending') AND EXISTS (
        SELECT 1 FROM messages um
        WHERE um.workspace_id = c.workspace_id AND um.conversation_id = c.id
          AND um.sender_type = 'contact' AND um.deleted_at IS NULL
          AND um.id > coalesce((
              SELECT rd.last_read_message_id FROM conversation_reads rd
              WHERE rd.workspace_id = c.workspace_id AND rd.conversation_id = c.id AND rd.user_id = @user_id
          ), '00000000-0000-0000-0000-000000000000'::uuid)
    ))::bigint AS mine_unread,
    count(*) FILTER (WHERE c.assignee_id IS NULL AND c.status = 'queued')::bigint AS unassigned,
    count(*) FILTER (WHERE c.status IN ('new', 'bot', 'queued', 'assigned', 'pending'))::bigint AS open
FROM conversations c
WHERE c.workspace_id = @workspace_id AND c.status IN ('new', 'bot', 'queued', 'assigned', 'pending');

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

-- name: CanAnswer :one
-- Assignees must be active members whose role answers customers
-- (answering, ADR-0020). memberships belongs to tenancy; this read-only
-- check sits here so it runs in the same transaction as the assignment.
SELECT EXISTS (
    SELECT 1 FROM memberships m
    JOIN roles r ON r.id = m.role_id
    WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id AND m.is_active
      AND r.key = ANY(@answering::text[])
);

-- name: BumpConversation :exec
-- A change that is not a state transition (tags): clients compare versions.
UPDATE conversations
SET version = version + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetAwaitingFix :exec
-- An open issue is linked to the conversation, or the last one was closed
-- (ADR-0012): the inbox shows the mark, clients compare versions.
UPDATE conversations
SET awaiting_fix = @awaiting_fix, version = version + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;
