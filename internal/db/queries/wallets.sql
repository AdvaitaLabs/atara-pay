-- One wallet = one on-chain account on one rail (CrossMint / Tempo /
-- Loka-LN). Wallets belong to exactly one wallet_group via group_id.

-- name: CreateWallet :one
INSERT INTO wallets (
    id, group_id, tenant_id, rail, chain, address, provider_locator,
    custody, encrypted_private_key, key_version, status, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: GetWalletByID :one
SELECT * FROM wallets
WHERE id = $1 AND status <> 'deleted';

-- name: GetWalletByGroupAndRail :one
-- Hot path: when /v1/transactions or /v1/onramp resolves a group to a
-- specific rail's wallet, this query is what runs. The
-- (group_id, rail, chain) UNIQUE index makes it O(1).
SELECT * FROM wallets
WHERE group_id = $1 AND rail = $2 AND status = 'active';

-- name: ListWalletsByGroup :many
-- Returns every wallet of a group, ordered so CrossMint comes before Tempo
-- before Loka-LN — gives the dashboard a stable display order without
-- per-rail logic in the frontend.
SELECT * FROM wallets
WHERE group_id = $1 AND status <> 'deleted'
ORDER BY
    CASE rail
        WHEN 'crossmint' THEN 1
        WHEN 'tempo'     THEN 2
        WHEN 'loka-ln'   THEN 3
        ELSE 99
    END;

-- name: GetWalletByAddress :one
-- Reverse lookup for incoming-transfer webhooks. The address index is
-- partial on status='active' so deleted wallets don't clutter it.
SELECT * FROM wallets
WHERE address = $1 AND status = 'active';

-- name: GetWalletByProviderLocator :one
-- Reverse lookup for provider webhooks that hand us a CrossMint cm_xxx
-- or similar opaque id.
SELECT * FROM wallets
WHERE rail = $1 AND provider_locator = $2 AND status = 'active';

-- name: UpdateWalletStatus :one
UPDATE wallets
SET status = $2
WHERE id = $1
RETURNING *;

-- name: UpdateWalletEncryptedKey :exec
-- Used by the keystore when the master key rotates. We keep encrypted_
-- private_key + key_version together to avoid ever decrypting with the
-- wrong master generation.
UPDATE wallets
SET encrypted_private_key = $2,
    key_version = $3
WHERE id = $1 AND custody = 'platform' AND rail = 'tempo';
