-- Session keys: delegated signers attached to a wallet, each carrying its
-- own limit_policy and rotation schedule. The DB invariants are enforced
-- by the schema (see migration 000004); these queries are kept minimal so
-- the orchestration service can compose them in transactions.

-- name: CreateSessionKey :one
INSERT INTO session_keys (
    id, tenant_id, wallet_id, group_id, name,
    public_address, encrypted_priv_key, key_version,
    policy_id,
    rotation_mode, rotation_interval_s, next_rotation_at,
    expires_at, status,
    rail_native, on_chain_tx_hash,
    metadata
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9,
    $10, $11, $12,
    $13, $14,
    $15, $16,
    $17
)
RETURNING *;

-- name: GetSessionKeyByID :one
SELECT * FROM session_keys
WHERE id = $1;

-- name: GetActiveSessionKeyByPublicAddress :one
-- Hot path: when a transaction is signed with a session key, we identify
-- the signer by its 0x-address and need the row's encrypted_priv_key +
-- policy_id + status in one lookup. The partial index on (public_address)
-- WHERE status='active' makes this O(1).
SELECT * FROM session_keys
WHERE public_address = $1 AND status = 'active';

-- name: ListSessionKeysForWallet :many
-- Dashboard listing. Includes revoked / expired so customers can audit
-- history; the UI strikes through inactive rows.
SELECT * FROM session_keys
WHERE wallet_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListSessionKeysForTenant :many
SELECT * FROM session_keys
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: RevokeSessionKey :one
-- Idempotent at the SQL layer via the WHERE status='active' guard —
-- repeat revokes don't overwrite the original revoked_at.
UPDATE session_keys
SET status         = 'revoked',
    revoked_reason = $2,
    revoked_at     = NOW()
WHERE id = $1 AND status = 'active'
RETURNING *;

-- name: ListSessionKeysDueForRotation :many
-- Background worker scans this every cycle. The partial index on
-- next_rotation_at filtered to (rotation_mode='auto_rotate' AND
-- status='active') keeps the scan tight regardless of total population.
SELECT * FROM session_keys
WHERE rotation_mode = 'auto_rotate'
  AND status = 'active'
  AND next_rotation_at <= $1
ORDER BY next_rotation_at ASC
LIMIT $2;

-- name: ListExpiredSessionKeys :many
-- Sweep: keys whose expires_at has passed but status still says
-- active/rotating. The cron flips them to 'expired'.
SELECT * FROM session_keys
WHERE status IN ('active', 'rotating')
  AND expires_at <= $1
ORDER BY expires_at ASC
LIMIT $2;

-- name: UpdateSessionKeyStatus :one
UPDATE session_keys
SET status = $2
WHERE id = $1
RETURNING *;

-- name: UpdateSessionKeyNextRotation :exec
-- Atomically reset the auto-rotation cursor after the worker hands out a
-- replacement key.
UPDATE session_keys
SET next_rotation_at = $2
WHERE id = $1;
