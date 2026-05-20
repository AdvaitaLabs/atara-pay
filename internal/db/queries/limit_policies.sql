-- Declarative spending rules attached to a scope.
-- scope_type ∈ {tenant_default, wallet_group, wallet, session_key}.

-- name: CreateLimitPolicy :one
INSERT INTO limit_policies (
    id, tenant_id, scope_type, scope_id,
    per_tx_amount, per_tx_asset,
    daily_amount, weekly_amount, monthly_amount, period_asset,
    timezone, reset_day_of_week, reset_day_of_month,
    allowed_recipients, denied_recipients,
    expires_at, enabled, metadata
) VALUES (
    $1, $2, $3, $4,
    $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13,
    $14, $15,
    $16, $17, $18
)
RETURNING *;

-- name: GetLimitPolicyByID :one
SELECT * FROM limit_policies
WHERE id = $1;

-- name: GetTenantDefaultPolicy :one
-- The "fallback" policy applied when no more specific policy exists for a
-- scope. The migration's partial-unique index guarantees at most one
-- enabled tenant_default per tenant.
SELECT * FROM limit_policies
WHERE tenant_id = $1
  AND scope_type = 'tenant_default'
  AND enabled = TRUE;

-- name: GetPolicyForScope :one
-- Resolve the policy attached to a specific scope_id (wallet_group, wallet,
-- or session_key). Returns the most recently-enabled one if duplicates
-- exist (defensive — schema doesn't enforce uniqueness here).
SELECT * FROM limit_policies
WHERE tenant_id = $1
  AND scope_type = $2
  AND scope_id = $3
  AND enabled = TRUE
ORDER BY created_at DESC
LIMIT 1;

-- name: ListEnabledPoliciesForTenant :many
SELECT * FROM limit_policies
WHERE tenant_id = $1 AND enabled = TRUE
ORDER BY created_at DESC;

-- name: DisableLimitPolicy :one
-- Soft-off: row stays so historical violations still reference it.
UPDATE limit_policies
SET enabled = FALSE
WHERE id = $1
RETURNING *;

-- name: UpdateLimitPolicyCaps :one
-- Replace the spending caps. Recipient lists, scope, and other invariants
-- are NOT mutable here — for those, customers create a new policy and
-- disable the old one.
UPDATE limit_policies
SET per_tx_amount   = $2,
    daily_amount    = $3,
    weekly_amount   = $4,
    monthly_amount  = $5
WHERE id = $1 AND enabled = TRUE
RETURNING *;
