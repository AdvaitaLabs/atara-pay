-- TIP-1022 virtual addresses. (wallet_id, label) is the natural idempotency
-- key — the same label always resolves to the same on-chain address.

-- name: CreateVirtualAddress :one
INSERT INTO virtual_addresses (
    id, tenant_id, wallet_id, group_id, label, address, registration_tx_hash, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetVirtualAddressByLabel :one
-- Idempotency path: if the customer asks for label "invoice-42" twice we
-- return the same row (and skip re-registering on chain).
SELECT * FROM virtual_addresses
WHERE wallet_id = $1 AND label = $2;

-- name: ListVirtualAddressesForWallet :many
SELECT * FROM virtual_addresses
WHERE wallet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetVirtualAddressByAddress :one
-- Reverse lookup for incoming-deposit webhooks: "which virtual address
-- received this transfer?"
SELECT * FROM virtual_addresses
WHERE address = $1;
