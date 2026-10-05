-- name: InsertSession :one
INSERT INTO widget_sessions (
    id, workspace_id, channel_id, contact_id, token_hash, claimed, claim_name, claim_email,
    created_at, last_seen_at, expires_at
) VALUES (
    @id, @workspace_id, @channel_id, sqlc.narg(contact_id), @token_hash, @claimed, sqlc.narg(claim_name),
    sqlc.narg(claim_email), @now, @now, @expires_at
)
RETURNING *;

-- name: SessionByHash :one
SELECT * FROM widget_sessions
WHERE workspace_id = @workspace_id AND token_hash = @token_hash;

-- name: SetSessionContact :one
-- The visitor's contact is known now (identify): pending claims are used
-- up.
UPDATE widget_sessions
SET contact_id = @contact_id, claimed = false, claim_name = NULL, claim_email = NULL, claim_phone = NULL,
    claim_consent = NULL
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: AttachSessionContact :one
-- The first message gave an anonymous session its contact; a concurrent
-- first message that won keeps its own (no row).
UPDATE widget_sessions
SET contact_id = @contact_id, claimed = false, claim_name = NULL, claim_email = NULL, claim_phone = NULL,
    claim_consent = NULL
WHERE workspace_id = @workspace_id AND id = @id AND contact_id IS NULL
RETURNING *;

-- name: GetSession :one
SELECT * FROM widget_sessions
WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetSessionClaim :one
-- An anonymous visitor without a contact claimed a name or an email.
UPDATE widget_sessions
SET claimed = true, claim_name = sqlc.narg(claim_name), claim_email = sqlc.narg(claim_email)
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: AddSessionIntro :one
-- An anonymous visitor without a contact filled in the pre-chat form: what
-- they typed adds to what they claimed before. No row once a concurrent
-- first message gave the session its contact.
UPDATE widget_sessions
SET claimed = claimed OR sqlc.narg(claim_name)::text IS NOT NULL OR sqlc.narg(claim_email)::text IS NOT NULL
        OR sqlc.narg(claim_phone)::text IS NOT NULL,
    claim_name = COALESCE(sqlc.narg(claim_name)::text, claim_name),
    claim_email = COALESCE(sqlc.narg(claim_email)::text, claim_email),
    claim_phone = COALESCE(sqlc.narg(claim_phone)::text, claim_phone),
    claim_consent = COALESCE(sqlc.narg(claim_consent)::jsonb, claim_consent)
WHERE workspace_id = @workspace_id AND id = @id AND contact_id IS NULL
RETURNING *;

-- name: TouchSession :exec
UPDATE widget_sessions
SET last_seen_at = @now, expires_at = @expires_at
WHERE workspace_id = @workspace_id AND id = @id;

-- name: DeleteSession :exec
DELETE FROM widget_sessions
WHERE workspace_id = @workspace_id AND id = @id;

-- name: PruneExpiredSessions :execrows
-- The worker's cleanup as tolkovo_system: the policy lets it see and
-- delete expired sessions only.
DELETE FROM widget_sessions
WHERE id IN (
    SELECT s.id FROM widget_sessions s WHERE s.expires_at < now() LIMIT 5000
);
