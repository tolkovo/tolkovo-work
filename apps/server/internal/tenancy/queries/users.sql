-- users is a global table without RLS (docs/adr/0003-multitenancy-rls.md).

-- name: CreateUser :one
INSERT INTO users (id, email, password_hash, name, locale)
VALUES (@id, @email, @password_hash, @name, @locale)
RETURNING id, email, name, locale;

-- name: GetUserCredentials :one
SELECT id, password_hash, disabled_at
FROM users
WHERE email = @email;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = @id;

-- name: GetUserPasswordHash :one
SELECT id, password_hash, disabled_at
FROM users
WHERE id = @id;

-- name: SetUserPasswordHash :execrows
UPDATE users
SET password_hash = @password_hash, updated_at = now()
WHERE id = @id AND disabled_at IS NULL;

-- name: UpdateUserProfile :one
-- Absent (NULL) fields keep their value.
UPDATE users
SET name       = COALESCE(sqlc.narg(name), name),
    locale     = COALESCE(sqlc.narg(locale), locale),
    updated_at = now()
WHERE id = @id AND disabled_at IS NULL
RETURNING id, email, name, locale;
