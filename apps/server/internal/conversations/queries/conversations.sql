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
SELECT sqlc.embed(c), ct.name AS contact_name
FROM conversations c
JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
WHERE c.workspace_id = @workspace_id AND c.id = @id;

-- name: LockConversation :one
-- Serialises state changes and new messages of one conversation.
SELECT * FROM conversations
WHERE workspace_id = @workspace_id AND id = @id
FOR UPDATE;

-- name: ListConversations :many
-- Newest activity first. The cursor is (last_message_at, id) of the last
-- row of the previous page. by_assignee with a NULL assignee_id lists the
-- unassigned ones.
SELECT sqlc.embed(c), ct.name AS contact_name
FROM conversations c
JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
WHERE c.workspace_id = @workspace_id
  AND (cardinality(@statuses::text[]) = 0 OR c.status = ANY(@statuses::text[]))
  AND (NOT @by_assignee::boolean
       OR (sqlc.narg(assignee_id)::uuid IS NULL AND c.assignee_id IS NULL)
       OR c.assignee_id = sqlc.narg(assignee_id)::uuid)
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (c.last_message_at, c.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY c.last_message_at DESC, c.id DESC
LIMIT @lim;

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
