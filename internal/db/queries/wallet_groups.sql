-- Wallet groups bundle a logical "owner" (user / agent / merchant / treasury)
-- and the per-rail wallets that belong to it. One group typically holds
-- 2-3 wallets: one CrossMint, one Tempo, eventually one Loka-LN.

-- name: CreateWalletGroup :one
INSERT INTO wallet_groups (
    id, tenant_id, owner_type, owner_ref, parent_group_id,
    custody, display_name, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetWalletGroupByID :one
SELECT * FROM wallet_groups
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetWalletGroupByOwnerRef :one
-- The lookup the dual-create endpoint needs to honor idempotency: if the
-- customer POSTs /v1/wallet-groups twice with the same owner_ref, we
-- return the existing group instead of failing.
SELECT * FROM wallet_groups
WHERE tenant_id = $1 AND owner_ref = $2 AND deleted_at IS NULL;

-- name: ListWalletGroupsInTenant :many
-- Dashboard listing. Filter by owner_type (e.g. only show agents) is
-- handled at the application layer.
SELECT * FROM wallet_groups
WHERE tenant_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAgentGroupsForParent :many
-- All agent groups belonging to a particular user group (parent → child).
-- Used by the dashboard to show "Alice's agents" under Alice's user group.
SELECT * FROM wallet_groups
WHERE parent_group_id = $1 AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: UpdateWalletGroupDisplayName :one
UPDATE wallet_groups
SET display_name = $2
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteWalletGroup :exec
-- Soft delete. The row stays so audit / reporting can still find it.
-- Cascades to the wallets table via the foreign key (which then go
-- status='deleted' through the same touch trigger).
UPDATE wallet_groups
SET deleted_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
