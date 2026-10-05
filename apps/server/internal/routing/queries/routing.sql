-- name: GetMode :one
SELECT COALESCE((SELECT mode FROM routing_settings WHERE workspace_id = @workspace_id), 'manual')::text;

-- name: SetMode :exec
INSERT INTO routing_settings (workspace_id, mode, updated_at)
VALUES (@workspace_id, @mode, @updated_at)
ON CONFLICT (workspace_id) DO UPDATE
SET mode = EXCLUDED.mode, updated_at = EXCLUDED.updated_at;

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
ON CONFLICT (workspace_id, user_id) DO UPDATE
SET seen_at = EXCLUDED.seen_at;

-- name: IsAvailable :one
SELECT EXISTS (
    SELECT 1 FROM operator_states os
    JOIN memberships m ON m.workspace_id = os.workspace_id AND m.user_id = os.user_id
    WHERE os.workspace_id = @workspace_id AND os.user_id = @user_id AND m.is_active
      AND os.status = 'online' AND os.seen_at > @seen_after::timestamptz
);

-- name: LockAvailable :many
-- Members who take conversations now, whose turn came first. The row
-- locks keep a parallel assignment from reading a stale turn order.
SELECT os.user_id
FROM operator_states os
JOIN memberships m ON m.workspace_id = os.workspace_id AND m.user_id = os.user_id
WHERE os.workspace_id = @workspace_id AND m.is_active
  AND os.status = 'online' AND os.seen_at > @seen_after::timestamptz
ORDER BY os.last_assigned_at ASC NULLS FIRST, os.user_id
FOR UPDATE OF os;

-- name: SetLastAssigned :exec
UPDATE operator_states
SET last_assigned_at = @assigned_at::timestamptz
WHERE workspace_id = @workspace_id AND user_id = @user_id;
