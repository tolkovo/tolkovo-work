-- Every query on a tenant table filters by workspace_id explicitly, even
-- though RLS enforces it too (docs/00-engineering-rules.md §6.5).

-- name: CreateContact :one
-- A contact without a name gets a guest name in the workspace's language.
INSERT INTO contacts (id, workspace_id, name, name_placeholder, email, phone, locale, external_id, identity_unverified, attributes)
VALUES (
    @id, @workspace_id,
    CASE WHEN @name::text = ''
        THEN contact_guest_name((SELECT w.locale FROM workspaces w WHERE w.id = @workspace_id), @id)
        ELSE @name::text END,
    @name::text = '',
    sqlc.narg(email), sqlc.narg(phone), sqlc.narg(locale), sqlc.narg(external_id), @identity_unverified,
    CASE WHEN sqlc.narg(consent)::jsonb IS NULL THEN '{}'::jsonb
        ELSE jsonb_build_object('consent', sqlc.narg(consent)::jsonb) END
)
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
-- A cleared name becomes the guest name again; the guest name saved as it
-- was stays a guest name.
UPDATE contacts
SET name = CASE WHEN @name::text = ''
        THEN contact_guest_name((SELECT w.locale FROM workspaces w WHERE w.id = @workspace_id), @id)
        ELSE @name::text END,
    name_placeholder = CASE WHEN @name::text = '' THEN true ELSE name_placeholder AND name = @name::text END,
    email = sqlc.narg(email), phone = sqlc.narg(phone), locale = sqlc.narg(locale), updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id AND deleted_at IS NULL
RETURNING *;
