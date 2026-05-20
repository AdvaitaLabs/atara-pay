-- Money-movement audit table. Append-mostly: status fields advance via
-- UPDATE but rows are never deleted.

-- name: CreateTransaction :one
INSERT INTO transactions (
    id, tenant_id, wallet_id, group_id, rail, chain,
    direction, counterparty,
    amount, asset, amount_usd,
    status, tx_hash, provider_tx_id,
    session_key_id, initiated_by_user, initiated_by_apikey,
    idempotency_key, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8,
    $9, $10, $11,
    $12, $13, $14,
    $15, $16, $17,
    $18, $19
)
RETURNING *;

-- name: GetTransactionByID :one
SELECT * FROM transactions
WHERE id = $1;

-- name: GetTransactionByIdempotencyKey :one
SELECT * FROM transactions
WHERE tenant_id = $1 AND idempotency_key = $2;

-- name: UpdateTransactionStatus :one
UPDATE transactions
SET status      = $2,
    tx_hash     = COALESCE($3, tx_hash),
    block_number = COALESCE($4, block_number),
    confirmed_at = CASE WHEN $2 IN ('succeeded', 'failed') THEN NOW() ELSE confirmed_at END
WHERE id = $1
RETURNING *;

-- name: ListTransactionsByTenant :many
SELECT * FROM transactions
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListTransactionsByWallet :many
SELECT * FROM transactions
WHERE wallet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListTransactionsByGroup :many
SELECT * FROM transactions
WHERE group_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
