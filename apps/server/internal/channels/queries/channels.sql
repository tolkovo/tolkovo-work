-- name: ListChannels :many
SELECT * FROM channels
WHERE workspace_id = $1 AND deleted_at IS NULL
ORDER BY created_at, id;

-- name: GetChannel :one
SELECT * FROM channels
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: GetChannelForUpdate :one
SELECT * FROM channels
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertChannel :one
INSERT INTO channels (id, workspace_id, type, name, status, external_id, config, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
RETURNING *;

-- name: UpdateChannelState :one
UPDATE channels
SET status = $3, error_code = $4, external_id = $5, config = $6, updated_at = $7
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: RenameChannel :one
UPDATE channels
SET name = $3, updated_at = $4
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteChannel :exec
UPDATE channels
SET deleted_at = $3, updated_at = $3
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL;

-- name: DeletePendingChannel :exec
DELETE FROM channels
WHERE workspace_id = $1 AND id = $2 AND status = 'pending';

-- name: GetSecret :one
SELECT sealed FROM channel_secrets
WHERE workspace_id = $1 AND channel_id = $2;

-- name: PutSecret :exec
INSERT INTO channel_secrets (workspace_id, channel_id, sealed, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, channel_id) DO UPDATE SET sealed = EXCLUDED.sealed, updated_at = EXCLUDED.updated_at;

-- name: DeleteSecret :exec
DELETE FROM channel_secrets
WHERE workspace_id = $1 AND channel_id = $2;

-- name: PruneStaleLogins :execrows
-- Run by tolkovo_system: unfinished personal-account logins of every workspace.
DELETE FROM channels
WHERE status = 'pending' AND created_at < $1;
