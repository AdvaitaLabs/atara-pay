-- Users are the humans logging into the dashboard. Always tenant-scoped.

-- name: CreateUser :one
INSERT INTO users (
    id, tenant_id, email, password_hash, role
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1;

-- name: GetUserByEmailInTenant :one
-- Login flow: email + tenant_id is the unique pair.
SELECT * FROM users
WHERE tenant_id = $1 AND email = $2;

-- name: GetUserByEmail :many
-- Some sign-in flows look up across tenants (e.g. user belongs to multiple
-- companies). Returns 0..n rows.
SELECT * FROM users
WHERE email = $1
ORDER BY created_at ASC;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2
WHERE id = $1;

-- name: MarkEmailVerified :exec
UPDATE users
SET email_verified = TRUE
WHERE id = $1;

-- name: TouchUserLogin :exec
UPDATE users
SET last_login_at = NOW()
WHERE id = $1;

-- name: ListUsersInTenant :many
SELECT * FROM users
WHERE tenant_id = $1
ORDER BY created_at ASC;
