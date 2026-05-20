-- Tenants are ATARA-Pay's primary isolation unit.
-- Queries here are used by signup, billing, and the customer self-service
-- account page.

-- name: CreateTenant :one
INSERT INTO tenants (
    id, name, primary_email, plan, status, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetTenantByID :one
SELECT * FROM tenants
WHERE id = $1;

-- name: GetTenantByEmail :one
SELECT * FROM tenants
WHERE primary_email = $1;

-- name: UpdateTenantPlan :one
UPDATE tenants
SET plan = $2
WHERE id = $1
RETURNING *;

-- name: UpdateTenantStatus :one
UPDATE tenants
SET status = $2
WHERE id = $1
RETURNING *;

-- name: ListActiveTenants :many
-- Used by background sweepers and the admin console.
SELECT * FROM tenants
WHERE status = 'active'
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
