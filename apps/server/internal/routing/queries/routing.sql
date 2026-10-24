-- name: GetMode :one
SELECT COALESCE((SELECT mode FROM routing_settings WHERE workspace_id = @workspace_id), 'manual')::text;

-- name: GetSettings :one
-- The defaults while the workspace has no row: manual, no limit.
SELECT COALESCE(s.mode, 'manual')::text AS mode, s.max_chats
FROM (SELECT @workspace_id::uuid AS workspace_id) w
LEFT JOIN routing_settings s ON s.workspace_id = w.workspace_id;

-- name: SetSettings :exec
INSERT INTO routing_settings (workspace_id, mode, max_chats, updated_at)
VALUES (@workspace_id, @mode, sqlc.narg(max_chats), @updated_at)
ON CONFLICT (workspace_id) DO UPDATE
SET mode = EXCLUDED.mode, max_chats = EXCLUDED.max_chats, updated_at = EXCLUDED.updated_at;

-- name: ListLimits :many
-- The members who answer customers, with their own limit (NULL: the
-- workspace's).
SELECT m.user_id, os.max_chats
FROM memberships m
JOIN roles r ON r.id = m.role_id
LEFT JOIN operator_states os ON os.workspace_id = m.workspace_id AND os.user_id = m.user_id
WHERE m.workspace_id = @workspace_id AND m.is_active AND r.key = ANY(@answering::text[])
ORDER BY m.user_id;

-- name: SetLimit :execrows
-- Only for an active member who answers customers.
INSERT INTO operator_states (workspace_id, user_id, max_chats, updated_at)
SELECT m.workspace_id, m.user_id, sqlc.narg(max_chats)::smallint, @updated_at::timestamptz
FROM memberships m
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id AND m.is_active
  AND r.key = ANY(@answering::text[])
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET max_chats = EXCLUDED.max_chats, updated_at = EXCLUDED.updated_at;

-- name: GetStates :many
-- States of the given members; members without a row have the defaults.
SELECT user_id, status, seen_at
FROM operator_states
WHERE workspace_id = @workspace_id AND user_id = ANY(@user_ids::uuid[]);

-- name: ListStates :many
SELECT user_id, status, seen_at
FROM operator_states
WHERE workspace_id = @workspace_id;

-- name: SetStatus :exec
INSERT INTO operator_states (workspace_id, user_id, status, updated_at)
VALUES (@workspace_id, @user_id, @status, @updated_at)
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET status = EXCLUDED.status, updated_at = EXCLUDED.updated_at;

-- name: MarkSeen :exec
-- The heartbeat of open panels. Only active members get a row: the
-- gateway closes the connections of the others at its next re-check.
INSERT INTO operator_states (workspace_id, user_id, seen_at)
SELECT m.workspace_id, m.user_id, @seen_at::timestamptz
FROM memberships m
WHERE m.workspace_id = @workspace_id AND m.user_id = ANY(@user_ids::uuid[]) AND m.is_active
-- Rows are locked in user_id order, as LockAvailable does: no deadlock.
ORDER BY m.user_id
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET seen_at = EXCLUDED.seen_at;

-- name: IsAvailable :one
SELECT EXISTS (
    SELECT 1 FROM operator_states os
    JOIN memberships m ON m.workspace_id = os.workspace_id AND m.user_id = os.user_id
    JOIN roles r ON r.id = m.role_id
    WHERE os.workspace_id = @workspace_id AND os.user_id = @user_id AND m.is_active
      AND r.key = ANY(@answering::text[])
      AND os.status = 'online' AND os.seen_at > @seen_after::timestamptz
);

-- name: LockAvailable :many
-- Members who take conversations now, whose turn came first. answering
-- are the roles that answer customers (ADR-0020: not the programmer), as
-- in IsAvailable and AnyAvailable. The row
-- locks keep a parallel assignment from reading a stale turn order; they
-- are taken in user_id order, as MarkSeen does, so the two never deadlock.
WITH locked AS MATERIALIZED (
    SELECT os.user_id, os.last_assigned_at, os.max_chats
    FROM operator_states os
    JOIN memberships m ON m.workspace_id = os.workspace_id AND m.user_id = os.user_id
    JOIN roles r ON r.id = m.role_id
    WHERE os.workspace_id = @workspace_id AND m.is_active
      AND r.key = ANY(@answering::text[])
      AND os.status = 'online' AND os.seen_at > @seen_after::timestamptz
    ORDER BY os.user_id
    FOR UPDATE OF os
)
SELECT user_id, max_chats FROM locked
ORDER BY last_assigned_at ASC NULLS FIRST, user_id;

-- name: SetLastAssigned :exec
UPDATE operator_states
SET last_assigned_at = @assigned_at::timestamptz
WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: AnyAvailable :one
SELECT EXISTS (
    SELECT 1 FROM operator_states os
    JOIN memberships m ON m.workspace_id = os.workspace_id AND m.user_id = os.user_id
    JOIN roles r ON r.id = m.role_id
    WHERE os.workspace_id = @workspace_id AND m.is_active
      AND r.key = ANY(@answering::text[])
      AND os.status = 'online' AND os.seen_at > @seen_after::timestamptz
);

-- name: AnyOwnLimit :one
SELECT EXISTS (
    SELECT 1 FROM operator_states WHERE workspace_id = @workspace_id AND max_chats IS NOT NULL
);
