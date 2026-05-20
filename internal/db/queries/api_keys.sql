-- API keys are the machine credentials customers attach to their HTTP
-- requests (Bearer sk_test_… / sk_live_…). The raw secret is shown to the
-- user exactly once at creation; from then on we look up by SHA-256 hash.

-- name: CreateAPIKey :one
INSERT INTO api_keys (
    id, tenant_id, created_by, name, environment, key_prefix, key_hash
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetAPIKeyByID :one
SELECT * FROM api_keys
WHERE id = $1;

-- name: GetAPIKeyByHash :one
-- Hot path on every API request. The index on key_hash makes this O(1).
-- Returns the key only if it has not been revoked.
SELECT * FROM api_keys
WHERE key_hash = $1 AND revoked_at IS NULL;

-- name: TouchAPIKeyUsage :exec
UPDATE api_keys
SET last_used_at = NOW()
WHERE id = $1;

-- name: ListAPIKeysInTenant :many
-- Dashboard listing. Includes revoked keys so customers can audit.
SELECT * FROM api_keys
WHERE tenant_id = $1
ORDER BY created_at DESC;

-- name: RevokeAPIKey :one
UPDATE api_keys
SET revoked_at = NOW(),
    revoked_reason = $2
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;
