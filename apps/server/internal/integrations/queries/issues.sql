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

-- name: IssueLinksByNumberForUpdate :many
-- Every conversation linked to one issue of the integration (the webhook,
-- GH-03), locked in id order.
SELECT * FROM issue_links
WHERE workspace_id = @workspace_id AND integration_id = @integration_id AND repo_id = @repo_id AND number = @number
ORDER BY id
FOR UPDATE;

-- name: SetIssueState :one
-- GitHub closed or reopened the issue.
UPDATE issue_links
SET status = @status, close_reason = sqlc.narg(close_reason), closed_at = sqlc.narg(closed_at), updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: HasOtherOpenIssue :one
-- Whether the conversation waits for a fix besides except_id: another open issue.
SELECT EXISTS (
    SELECT 1 FROM issue_links
    WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id AND status = 'open' AND id <> @except_id
);

-- name: FixedIssue :many
-- The issue of the conversation closed as completed most recently: the
-- reply about the fix is drafted from it.
SELECT * FROM issue_links
WHERE workspace_id = @workspace_id AND conversation_id = @conversation_id
  AND status = 'closed' AND close_reason = 'completed'
ORDER BY closed_at DESC, id DESC
LIMIT 1;

-- name: InsertDelivery :many
-- A webhook delivery seen for the first time; no row back means a repeat.
INSERT INTO integration_deliveries (workspace_id, integration_id, delivery_id, received_at)
VALUES (@workspace_id, @integration_id, @delivery_id, @received_at)
ON CONFLICT DO NOTHING
RETURNING delivery_id;

-- name: PruneDeliveries :exec
DELETE FROM integration_deliveries
WHERE workspace_id = @workspace_id AND integration_id = @integration_id AND received_at < @before;

-- name: IntegrationHasIssues :one
-- An integration with issues is disabled, never deleted: the issues keep it.
SELECT EXISTS (
    SELECT 1 FROM issue_links WHERE workspace_id = @workspace_id AND integration_id = @integration_id
);

-- name: DetachInstallation :execrows
-- The App was uninstalled on GitHub: the integration waits to be installed again.
UPDATE integrations
SET status = 'install_pending', installation_id = NULL, account = NULL, updated_at = @updated_at
WHERE workspace_id = @workspace_id AND id = @id AND status = 'active' AND installation_id = @installation_id;
