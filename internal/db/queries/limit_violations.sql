-- Append-only audit log of every rejected attempt.

-- name: CreateLimitViolation :one
INSERT INTO limit_violations (
    tenant_id, policy_id, wallet_id, session_key_id, transaction_id,
    violation_type,
    attempted_amount, attempted_asset, attempted_recipient,
    limit_value, current_used,
    metadata
) VALUES (
    $1, $2, $3, $4, $5,
    $6,
    $7, $8, $9,
    $10, $11,
    $12
)
RETURNING *;

-- name: ListLimitViolationsByTenant :many
SELECT * FROM limit_violations
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListLimitViolationsByWallet :many
SELECT * FROM limit_violations
WHERE wallet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
