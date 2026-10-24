-- name: ListLines :many
-- Every line of the workspace by level, then name, with its members; the
-- number is bounded by MaxLines.
SELECT sqlc.embed(l),
       coalesce((SELECT array_agg(m.user_id ORDER BY m.user_id) FROM line_members m
                 WHERE m.workspace_id = l.workspace_id AND m.line_id = l.id), '{}')::uuid[] AS member_ids
FROM lines l
WHERE l.workspace_id = @workspace_id
ORDER BY l.level, l.name, l.id
LIMIT @lim;

-- name: GetLine :one
SELECT * FROM lines WHERE workspace_id = @workspace_id AND id = @id;

-- name: CountLines :one
SELECT count(*) FROM lines WHERE workspace_id = @workspace_id;

-- name: CreateLine :one
-- No row when the name is taken (case-insensitive).
INSERT INTO lines (id, workspace_id, name, level, created_at, updated_at)
VALUES (@id, @workspace_id, @name, @level, @now, @now)
ON CONFLICT (workspace_id, name) DO NOTHING
RETURNING *;

-- name: UpdateLine :one
UPDATE lines
SET name = @name, level = @level, updated_at = @now
WHERE workspace_id = @workspace_id AND id = @id
RETURNING *;

-- name: DeleteLine :execrows
DELETE FROM lines WHERE workspace_id = @workspace_id AND id = @id;

-- name: ClearLineMembers :exec
DELETE FROM line_members WHERE workspace_id = @workspace_id AND line_id = @line_id;

-- name: AddLineMembers :execrows
-- Only active members of the workspace are added.
INSERT INTO line_members (workspace_id, line_id, user_id)
SELECT m.workspace_id, @line_id, m.user_id
FROM memberships m
WHERE m.workspace_id = @workspace_id AND m.user_id = ANY(@user_ids::uuid[]) AND m.is_active
ON CONFLICT DO NOTHING;

-- name: LineMemberIDs :many
SELECT user_id FROM line_members
WHERE workspace_id = @workspace_id AND line_id = @line_id;

-- name: LineMembersOf :many
-- The members of the given lines, for the router.
SELECT line_id, user_id FROM line_members
WHERE workspace_id = @workspace_id AND line_id = ANY(@line_ids::uuid[]);

-- name: RemoveLineMember :exec
-- A member who leaves the team leaves every line (ADR-0020).
DELETE FROM line_members WHERE workspace_id = @workspace_id AND user_id = @user_id;

-- name: SetConversationLine :exec
-- line_level is the line's level, 1 on the first line. Clients compare
-- versions.
UPDATE conversations
SET line_id = sqlc.narg(line_id), line_level = @line_level, version = version + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: SetLineLevelOf :exec
-- The conversations of a line whose level changed take it; with release
-- they go back to the first line (the line is being deleted).
UPDATE conversations
SET line_level = @level,
    line_id = CASE WHEN @release::boolean THEN NULL ELSE line_id END,
    version = version + 1, updated_at = now()
WHERE workspace_id = @workspace_id AND line_id = sqlc.narg(line_id)::uuid;
