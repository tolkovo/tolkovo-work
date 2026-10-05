-- name: GetIntegration :one
SELECT * FROM integrations
WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertIssueLink :one
INSERT INTO issue_links (
    id, workspace_id, integration_id, conversation_id, provider, repo_id, repo_full_name, status,
    number, node_id, url, title, body, labels, created_by, created_at, updated_at, closed_at
) VALUES (
    @id, @workspace_id, @integration_id, @conversation_id, @provider, @repo_id, @repo_full_name, @status,
    sqlc.narg(number), sqlc.narg(node_id), sqlc.narg(url), @title, sqlc.narg(body), @labels, @created_by,
    @created_at, @created_at, sqlc.narg(closed_at)
)
RETURNING *;

-- name: GetIssueLink :one
SELECT * FROM issue_links
WHERE workspace_id = @workspace_id AND id = @id;

-- name: GetIssueLinkForUpdate :one
SELECT * FROM issue_links
WHERE workspace_id = @workspace_id AND id = @id
FOR UPDATE;

-- name: ConversationIssueLinks :many
SELECT * FROM issue_links
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
ORDER BY created_at, id;

-- name: SetIssueCreated :one
-- The job created the issue: the text now lives on GitHub.
UPDATE issue_links
SET status = 'open', number = @number, node_id = @node_id, url = @url, body = NULL, error_code = NULL,
    updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: SetIssueFailed :one
UPDATE issue_links
SET status = 'failed', error_code = @error_code, updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: SetIssueCreating :one
-- A failed issue is sent again.
UPDATE issue_links
SET status = 'creating', error_code = NULL, updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteIssueLink :exec
DELETE FROM issue_links
WHERE workspace_id = @workspace_id AND id = @id;

-- name: RecentOpenIssues :many
-- Open issues of the workspace, one row per issue, most recently linked
-- first: what an operator links another conversation to.
SELECT repo_full_name, number, title, url, updated_at FROM (
    SELECT DISTINCT ON (repo_id, number) repo_full_name, number, title, url, updated_at
    FROM issue_links
    WHERE workspace_id = @workspace_id AND integration_id = @integration_id AND status = 'open'
    ORDER BY repo_id, number, updated_at DESC
) AS issues
ORDER BY updated_at DESC
LIMIT 20;
