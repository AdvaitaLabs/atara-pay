-- Customer-registered webhook destinations. The HMAC secret is opaque
-- bytes; the application minted it and showed it once at creation.

-- name: CreateWebhookEndpoint :one
INSERT INTO webhook_endpoints (
    id, tenant_id, url, secret, secret_version,
    subscribed_events, status, description, metadata
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9
)
RETURNING *;

-- name: GetWebhookEndpointByID :one
SELECT * FROM webhook_endpoints
WHERE id = $1;

-- name: ListWebhookEndpointsForTenant :many
SELECT * FROM webhook_endpoints
WHERE tenant_id = $1 AND status <> 'deleted'
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListActiveEndpointsForTenant :many
-- The publisher fans out one domain event to every active subscriber of
-- this tenant. Filtering on subscribed_events happens in app code (JSONB
-- containment) so each event is matched against the array.
SELECT * FROM webhook_endpoints
WHERE tenant_id = $1 AND status = 'active';

-- name: UpdateWebhookEndpoint :one
-- Mutable surface: URL, subscribed_events, description, status. The
-- secret is rotated via RotateWebhookEndpointSecret only — keeping the
-- updates separate so a tenant can't accidentally race a URL change with
-- a secret rotation in the same call.
--
-- sqlc.narg() forces each argument to be a nullable pgtype so PATCH-style
-- semantics (omit field = no change) work cleanly. Columns that are NOT
-- NULL in the table (url, status) would otherwise be inferred as Go
-- string and lose their COALESCE behavior.
UPDATE webhook_endpoints
SET url               = COALESCE(sqlc.narg('url')::text,       url),
    subscribed_events = COALESCE(sqlc.narg('subscribed_events')::jsonb, subscribed_events),
    description       = COALESCE(sqlc.narg('description')::text, description),
    status            = COALESCE(sqlc.narg('status')::text,     status)
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: RotateWebhookEndpointSecret :one
-- Bumps the secret and its version atomically. Deliveries already in
-- flight keep using the prior version via webhook_events.signature_version.
UPDATE webhook_endpoints
SET secret         = $2,
    secret_version = secret_version + 1
WHERE id = $1
RETURNING *;

-- name: DeleteWebhookEndpoint :exec
-- Soft delete via status='deleted' so historical webhook_events still
-- resolve their endpoint_id back to a row.
UPDATE webhook_endpoints
SET status = 'deleted'
WHERE id = $1 AND status <> 'deleted';

-- name: TouchWebhookEndpointSuccess :exec
UPDATE webhook_endpoints
SET last_success_at      = NOW(),
    consecutive_failures = 0
WHERE id = $1;

-- name: TouchWebhookEndpointFailure :one
-- Bumps the failure counter and returns the new value so the worker can
-- decide whether to auto-pause the endpoint (e.g. after 30 in a row).
UPDATE webhook_endpoints
SET last_failure_at      = NOW(),
    consecutive_failures = consecutive_failures + 1
WHERE id = $1
RETURNING consecutive_failures;

-- name: PauseWebhookEndpoint :exec
UPDATE webhook_endpoints
SET status = 'paused'
WHERE id = $1 AND status = 'active';
