-- name: GetSettings :one
SELECT * FROM ai_settings
WHERE workspace_id = @workspace_id;

-- name: GetSettingsForUpdate :one
SELECT * FROM ai_settings
WHERE workspace_id = @workspace_id
FOR UPDATE;

-- name: EnsureSettings :exec
INSERT INTO ai_settings (workspace_id) VALUES (@workspace_id)
ON CONFLICT (workspace_id) DO NOTHING;

-- name: UpdateSettings :one
UPDATE ai_settings
SET enabled = @enabled,
    provider = sqlc.narg('provider'),
    model = sqlc.narg('model'),
    base_url = sqlc.narg('base_url'),
    credential_id = sqlc.narg('credential_id'),
    input_price_micros = @input_price_micros,
    output_price_micros = @output_price_micros,
    monthly_budget_micros = sqlc.narg('monthly_budget_micros'),
    updated_at = @updated_at
WHERE workspace_id = @workspace_id
RETURNING *;

-- name: GetCredential :one
SELECT * FROM ai_credentials
WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertCredential :exec
INSERT INTO ai_credentials (id, workspace_id, provider, sealed, hint, created_by, created_at)
VALUES (@id, @workspace_id, @provider, @sealed, @hint, sqlc.narg('created_by'), @created_at);

-- name: DeleteCredentialsExcept :exec
-- A workspace keeps only the key its settings point at.
DELETE FROM ai_credentials
WHERE workspace_id = @workspace_id AND id IS DISTINCT FROM sqlc.narg('keep');

-- name: SpentSince :one
SELECT COALESCE(SUM(cost_micros), 0)::bigint AS spent
FROM ai_calls
WHERE workspace_id = @workspace_id AND created_at >= @since;

-- name: InsertCall :exec
INSERT INTO ai_calls (
    id, workspace_id, conversation_id, purpose, provider, model,
    input_tokens, output_tokens, cached_tokens, cost_micros, latency_ms,
    status, error_code, request_redacted, response_redacted, created_at
) VALUES (
    @id, @workspace_id, sqlc.narg('conversation_id'), @purpose, @provider, @model,
    @input_tokens, @output_tokens, @cached_tokens, @cost_micros, @latency_ms,
    @status, sqlc.narg('error_code'), @request_redacted, @response_redacted, @created_at
);

-- name: ListCalls :many
SELECT * FROM ai_calls
WHERE workspace_id = @workspace_id
ORDER BY created_at DESC, id DESC
LIMIT @lim;

-- name: ClearOldTexts :execrows
-- The worker's cleanup as tolkovo_system: the policy lets it see and
-- change calls older than 30 days only, and only their texts.
UPDATE ai_calls
SET request_redacted = NULL, response_redacted = NULL
WHERE id IN (
    SELECT c.id FROM ai_calls c
    WHERE c.created_at < now() - interval '30 days'
      AND (c.request_redacted IS NOT NULL OR c.response_redacted IS NOT NULL)
    LIMIT 5000
);
