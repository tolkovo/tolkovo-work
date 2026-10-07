-- Personal access tokens (migration 00032, ADR-0016). A token is live while
-- it has not expired; revoking deletes it.

-- name: InsertAccessToken :one
INSERT INTO access_tokens (id, workspace_id, user_id, name, scopes, token_hash, prefix, created_at, expires_at)
VALUES (@id, @workspace_id, @user_id, @name, @scopes, @token_hash, @prefix, @created_at, sqlc.narg(expires_at))
RETURNING id, user_id, name, scopes, prefix, created_at, expires_at, last_used_at;

-- name: LockMemberTokens :exec
-- Serializes issuing tokens of one member, so the limit holds under
-- parallel requests.
SELECT pg_advisory_xact_lock(hashtextextended('access_tokens:' || (@workspace_id::uuid)::text || ':' || (@user_id::uuid)::text, 0));

-- name: CountLiveAccessTokens :one
SELECT count(*) FROM access_tokens
WHERE workspace_id = @workspace_id AND user_id = @user_id
  AND (expires_at IS NULL OR expires_at > @now::timestamptz);

-- name: DropExpiredAccessTokens :exec
-- Expired tokens of a member, removed when they issue a new one.
DELETE FROM access_tokens
WHERE workspace_id = @workspace_id AND user_id = @user_id AND expires_at <= @now::timestamptz;

-- name: ListAccessTokens :many
-- Live tokens, newest first; of one member when user_id is set.
SELECT t.id, t.user_id, t.name, t.scopes, t.prefix, t.created_at, t.expires_at, t.last_used_at,
       coalesce(m.display_name, u.name) AS user_name
FROM access_tokens t
JOIN memberships m ON m.workspace_id = t.workspace_id AND m.user_id = t.user_id
JOIN users u ON u.id = t.user_id
WHERE t.workspace_id = @workspace_id
  AND (sqlc.narg(user_id)::uuid IS NULL OR t.user_id = sqlc.narg(user_id)::uuid)
  AND (t.expires_at IS NULL OR t.expires_at > @now::timestamptz)
ORDER BY t.created_at DESC, t.id DESC;

-- name: DeleteAccessToken :execrows
-- Of one member when user_id is set (their own), any otherwise (an admin).
DELETE FROM access_tokens
WHERE workspace_id = @workspace_id AND id = @id
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid);

-- name: DeleteMemberAccessTokens :exec
DELETE FROM access_tokens WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: GetAccessTokenByHash :one
SELECT id, user_id, scopes, expires_at, last_used_at
FROM access_tokens
WHERE workspace_id = @workspace_id AND token_hash = @token_hash;

-- name: TouchAccessToken :exec
UPDATE access_tokens SET last_used_at = @last_used_at
WHERE workspace_id = @workspace_id AND id = @id;
