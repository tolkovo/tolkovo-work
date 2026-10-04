-- Every query on a tenant table filters by workspace_id explicitly, even
-- though RLS enforces it too (docs/00-engineering-rules.md §6.5).

-- name: CreateContact :one
INSERT INTO contacts (id, workspace_id, name, email, phone, locale, external_id)
VALUES (@id, @workspace_id, @name, sqlc.narg(email), sqlc.narg(phone), sqlc.narg(locale), sqlc.narg(external_id))
RETURNING *;

-- name: GetContact :one
SELECT * FROM contacts
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL;

-- name: ListContacts :many
-- Newest first; the cursor is the id of the last contact of the previous page.
SELECT * FROM contacts
WHERE workspace_id = @workspace_id
  AND deleted_at IS NULL
  AND (sqlc.narg(before_id)::uuid IS NULL OR id < sqlc.narg(before_id)::uuid)
ORDER BY id DESC
LIMIT @lim;

-- name: UpdateContact :one
UPDATE contacts
SET name = @name, email = sqlc.narg(email), phone = sqlc.narg(phone), locale = sqlc.narg(locale), updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL
RETURNING *;
