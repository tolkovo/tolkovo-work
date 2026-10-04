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
