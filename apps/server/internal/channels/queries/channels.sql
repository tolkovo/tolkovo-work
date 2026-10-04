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

-- name: ListReceivers :many
-- Run by tolkovo_system (policy system_see_active): the channels of every
-- workspace the worker holds a connection for — personal accounts and
-- bots without a webhook.
SELECT workspace_id, id, type FROM channels
WHERE status = 'active' AND deleted_at IS NULL
  AND (type = 'telegram_user' OR config->>'delivery' = 'polling')
ORDER BY id;

-- name: GetActiveChannel :one
SELECT * FROM channels
WHERE workspace_id = $1 AND id = $2 AND status = 'active' AND deleted_at IS NULL;

-- name: MarkInbound :exec
-- At most once a minute: it serves the wizard's check and channel health.
UPDATE channels
SET last_inbound_at = $3
WHERE workspace_id = $1 AND id = $2
  AND (last_inbound_at IS NULL OR last_inbound_at < $3::timestamptz - interval '1 minute');

-- name: SetChannelError :exec
UPDATE channels
SET status = 'error', error_code = $3, updated_at = $4
WHERE workspace_id = $1 AND id = $2 AND status = 'active' AND deleted_at IS NULL;

-- name: GetUpdateState :one
SELECT pts, qts, date, seq FROM channel_update_state
WHERE workspace_id = $1 AND channel_id = $2;

-- name: PutUpdateState :exec
INSERT INTO channel_update_state (workspace_id, channel_id, pts, qts, date, seq, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (workspace_id, channel_id) DO UPDATE
SET pts = EXCLUDED.pts, qts = EXCLUDED.qts, date = EXCLUDED.date, seq = EXCLUDED.seq, updated_at = now();

-- name: DeleteUpdateState :exec
DELETE FROM channel_update_state
WHERE workspace_id = $1 AND channel_id = $2;

-- name: GetPeer :one
SELECT access_hash FROM channel_peers
WHERE workspace_id = $1 AND channel_id = $2 AND user_id = $3;

-- name: PutPeer :exec
INSERT INTO channel_peers (workspace_id, channel_id, user_id, access_hash, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (workspace_id, channel_id, user_id) DO UPDATE
SET access_hash = EXCLUDED.access_hash, updated_at = EXCLUDED.updated_at
WHERE channel_peers.access_hash <> EXCLUDED.access_hash;

-- name: DeletePeers :exec
DELETE FROM channel_peers
WHERE workspace_id = $1 AND channel_id = $2;

-- name: ListIngestKeys :many
SELECT * FROM ingest_keys
WHERE workspace_id = $1 AND channel_id = $2
ORDER BY created_at DESC, id DESC;

-- name: CountActiveIngestKeys :one
SELECT count(*) FROM ingest_keys
WHERE workspace_id = $1 AND channel_id = $2 AND revoked_at IS NULL;

-- name: InsertIngestKey :one
INSERT INTO ingest_keys (id, workspace_id, channel_id, name, key_hash, prefix, created_by, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: RevokeIngestKey :one
-- Revoking twice keeps the first time.
UPDATE ingest_keys
SET revoked_at = COALESCE(revoked_at, $4)
WHERE workspace_id = $1 AND channel_id = $2 AND id = $3
RETURNING *;

-- name: RevokeChannelIngestKeys :exec
UPDATE ingest_keys
SET revoked_at = $3
WHERE workspace_id = $1 AND channel_id = $2 AND revoked_at IS NULL;

-- name: UpdateChannelConfig :one
UPDATE channels
SET config = $3, updated_at = $4
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
RETURNING *;
