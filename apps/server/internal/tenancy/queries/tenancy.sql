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

-- name: UpdateWorkspace :one
UPDATE workspaces
SET name = @name, locale = @locale, timezone = @timezone, updated_at = now()
WHERE id = @workspace_id AND deleted_at IS NULL
RETURNING id, slug, name, locale, timezone, created_at;

-- name: GetMembership :one
-- The access check of every authenticated request: an active member of a
-- live workspace whose account is not disabled.
SELECT u.id AS user_id, u.email, u.name AS user_name, u.locale AS user_locale,
       u.email_verified_at,
       w.id AS workspace_id, w.slug, w.name AS workspace_name,
       w.locale AS workspace_locale, w.timezone, w.created_at AS workspace_created_at,
       r.key AS role_key
FROM memberships m
JOIN users u ON u.id = m.user_id
JOIN workspaces w ON w.id = m.workspace_id
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = @workspace_id
  AND m.user_id = @user_id
  AND m.is_active
  AND u.disabled_at IS NULL
  AND w.deleted_at IS NULL;

-- name: ListUserWorkspaces :many
-- Runs in db.InUserTx: the own_memberships policy shows the user's rows in
-- every workspace (migration 00003). The query filters by user_id as well.
SELECT w.id, w.slug, w.name, r.key AS role_key
FROM memberships m
JOIN workspaces w ON w.id = m.workspace_id
JOIN roles r ON r.id = m.role_id
WHERE m.user_id = @user_id
  AND m.is_active
  AND w.deleted_at IS NULL
ORDER BY m.created_at, w.id;

-- name: ListMembers :many
SELECT m.user_id, u.email, u.name, r.key AS role_key,
       m.max_concurrent_chats, m.line_level, m.is_active
FROM memberships m
JOIN users u ON u.id = m.user_id
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = @workspace_id
ORDER BY m.created_at, m.user_id;

-- name: LockMember :one
-- The member a role change or a removal acts on (ADR-0020), locked so two
-- admins cannot act on one member at once.
SELECT r.key AS role_key, m.is_active
FROM memberships m
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id
FOR UPDATE OF m;

-- name: SetMemberRole :execrows
UPDATE memberships m
SET role_id = r.id, updated_at = now()
FROM roles r
WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id AND m.is_active
  AND r.key = @role_key AND (r.workspace_id IS NULL OR r.workspace_id = @workspace_id);

-- name: DeactivateMember :execrows
UPDATE memberships
SET is_active = false, updated_at = now()
WHERE workspace_id = @workspace_id AND user_id = @user_id AND is_active;

-- name: GetMember :one
SELECT m.user_id, u.email, u.name, r.key AS role_key,
       m.max_concurrent_chats, m.line_level, m.is_active
FROM memberships m
JOIN users u ON u.id = m.user_id
JOIN roles r ON r.id = m.role_id
WHERE m.workspace_id = @workspace_id AND m.user_id = @user_id;
