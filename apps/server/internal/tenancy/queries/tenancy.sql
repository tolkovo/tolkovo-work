-- Every query on a tenant table filters by workspace_id explicitly, even
-- though RLS enforces it too (docs/00-engineering-rules.md §6.5).

-- name: CreateWorkspace :one
INSERT INTO workspaces (id, slug, name, locale, timezone)
VALUES (@id, @slug, @name, @locale, @timezone)
RETURNING id, slug, name, locale, timezone, created_at;

-- name: GetWorkspace :one
SELECT id, slug, name, locale, timezone, created_at
FROM workspaces
WHERE id = @workspace_id AND deleted_at IS NULL;

-- name: AddMember :execrows
-- Inserts nothing when the role key does not exist for this workspace.
INSERT INTO memberships (workspace_id, user_id, role_id)
SELECT @workspace_id, @user_id, r.id
FROM roles r
WHERE r.key = @role_key
  AND (r.workspace_id IS NULL OR r.workspace_id = @workspace_id);

-- name: ListMembers :many
SELECT m.user_id, u.email, u.name, r.key AS role_key,
       m.max_concurrent_chats, m.line_level, m.is_active
FROM memberships m
JOIN users u ON u.id = m.user_id
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = @workspace_id
ORDER BY m.created_at, m.user_id;
