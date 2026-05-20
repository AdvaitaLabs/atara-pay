-- Fiat → crypto purchases. The provider's hosted checkout URL is captured
-- at creation; status advances via webhook updates from the provider.

-- name: CreateOnrampOrder :one
INSERT INTO onramp_orders (
    id, tenant_id, wallet_id, group_id,
    provider, provider_order_id, checkout_url,
    fiat_amount, fiat_currency,
    asset, chain, crypto_amount,
    status, initiated_by_apikey, idempotency_key,
    expires_at, metadata
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17
)
RETURNING *;

-- name: GetOnrampOrderByID :one
SELECT * FROM onramp_orders
WHERE id = $1;

-- name: GetOnrampOrderByProviderID :one
SELECT * FROM onramp_orders
WHERE provider = $1 AND provider_order_id = $2;

-- name: UpdateOnrampOrderStatus :one
UPDATE onramp_orders
SET status        = $2,
    crypto_amount = COALESCE($3, crypto_amount),
    error_code    = COALESCE($4, error_code),
    error_message = COALESCE($5, error_message),
    completed_at  = CASE WHEN $2 IN ('completed', 'failed', 'expired')
                          AND completed_at IS NULL
                         THEN NOW() ELSE completed_at END
WHERE id = $1
RETURNING *;

-- name: ListOnrampOrdersByTenant :many
SELECT * FROM onramp_orders
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListOnrampOrdersByGroup :many
SELECT * FROM onramp_orders
WHERE group_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
