-- name: GetLiveIntegration :one
SELECT * FROM integrations
WHERE workspace_id = @workspace_id AND provider = @provider AND status <> 'disabled';

-- name: GetLiveIntegrationForUpdate :one
SELECT * FROM integrations
WHERE workspace_id = @workspace_id AND provider = @provider AND status <> 'disabled'
FOR UPDATE;

-- name: InsertIntegration :one
INSERT INTO integrations (id, workspace_id, provider, status, state_hash, state_expires_at, created_by, created_at, updated_at)
VALUES (@id, @workspace_id, @provider, @status, @state_hash, @state_expires_at, @created_by, @created_at, @created_at)
RETURNING *;

-- name: DeleteIntegration :exec
DELETE FROM integrations
WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetIntegrationApp :one
UPDATE integrations
SET status = 'install_pending', app_id = @app_id, app_slug = @app_slug, app_owner = @app_owner, app_url = @app_url,
    state_hash = NULL, state_expires_at = NULL, updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: SetIntegrationInstallation :one
UPDATE integrations
SET status = 'active', installation_id = @installation_id, account = @account, updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: UpdateIntegrationSettings :one
UPDATE integrations
SET repo_id = sqlc.narg(repo_id), repo_full_name = sqlc.narg(repo_full_name), repo_private = sqlc.narg(repo_private),
    labels = @labels,
    issue_title_template = sqlc.narg(issue_title_template), issue_body_template = sqlc.narg(issue_body_template),
    reply_template_ru = sqlc.narg(reply_template_ru), reply_template_en = sqlc.narg(reply_template_en),
    updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DisableIntegration :exec
UPDATE integrations
SET status = 'disabled', state_hash = NULL, state_expires_at = NULL, disabled_at = @disabled_at, updated_at = @disabled_at
WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetIntegrationSecret :one
SELECT sealed FROM integration_secrets
WHERE workspace_id = @workspace_id AND integration_id = @integration_id;

-- name: PutIntegrationSecret :exec
INSERT INTO integration_secrets (workspace_id, integration_id, sealed, updated_at)
VALUES (@workspace_id, @integration_id, @sealed, @updated_at)
ON CONFLICT (workspace_id, integration_id) DO UPDATE SET sealed = EXCLUDED.sealed, updated_at = EXCLUDED.updated_at;

-- name: DeleteIntegrationSecret :exec
DELETE FROM integration_secrets
WHERE workspace_id = @workspace_id AND integration_id = @integration_id;
