-- Per-(domain-event, subscribed-endpoint) delivery records. The delivery
-- worker iterates pending/failed rows whose next_attempt_at has arrived.

-- name: CreateWebhookEvent :one
INSERT INTO webhook_events (
    id, tenant_id, endpoint_id,
    event_type, event_data,
    related_wallet_id, related_group_id, related_transaction_id,
    related_onramp_order_id, related_session_key_id,
    status, attempts, next_attempt_at,
    signature_version, metadata
) VALUES (
    $1, $2, $3,
    $4, $5,
    $6, $7, $8,
    $9, $10,
    $11, $12, $13,
    $14, $15
)
RETURNING *;

-- name: GetWebhookEventByID :one
SELECT * FROM webhook_events
WHERE id = $1;

-- name: ListWebhookEventsForTenant :many
SELECT * FROM webhook_events
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListWebhookEventsForEndpoint :many
SELECT * FROM webhook_events
WHERE endpoint_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: ListDueWebhookEvents :many
-- Hot path for the delivery worker. Hits the partial index on
-- next_attempt_at WHERE status IN ('pending','failed'). LIMIT enforces
-- per-tick budget.
SELECT * FROM webhook_events
WHERE status IN ('pending', 'failed')
  AND next_attempt_at <= $1
ORDER BY next_attempt_at ASC
LIMIT $2;

-- name: MarkWebhookEventDelivering :one
-- Take ownership: flip status to 'delivering' so a parallel worker
-- doesn't pick the same row up. Returns the row only when the transition
-- succeeded — concurrent workers see no rows and skip.
UPDATE webhook_events
SET status = 'delivering'
WHERE id = $1 AND status IN ('pending', 'failed')
RETURNING *;

-- name: MarkWebhookEventDelivered :one
UPDATE webhook_events
SET status            = 'delivered',
    last_response_code = $2,
    delivered_at      = NOW()
WHERE id = $1
RETURNING *;

-- name: MarkWebhookEventFailed :one
-- One failed delivery attempt. Bumps attempts + schedules next retry.
-- The worker computes next_attempt_at via exponential backoff and
-- passes it in.
UPDATE webhook_events
SET status             = 'failed',
    attempts           = attempts + 1,
    next_attempt_at    = $2,
    last_response_code = $3,
    last_response_body = $4,
    last_error         = $5
WHERE id = $1
RETURNING *;

-- name: MarkWebhookEventDead :exec
-- Terminal state after the retry budget is exhausted.
UPDATE webhook_events
SET status = 'dead'
WHERE id = $1;
