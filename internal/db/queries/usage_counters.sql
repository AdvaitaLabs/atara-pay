-- Durable mirror of the hot-path Redis counters. Redis is the canonical
-- source for live values; PG is the persisted copy flushed periodically
-- so audits, reports, and Redis-outage recovery work.
--
-- period_key formats:
--   daily:2026-05-20
--   weekly:2026-W21
--   monthly:2026-05

-- name: UpsertUsageCounter :one
-- Used both by the periodic Redis → PG flush and as a fallback path when
-- Redis is unavailable. ON CONFLICT updates used_amount monotonically;
-- we never go backwards (Redis is authoritative for live values).
INSERT INTO usage_counters (
    policy_id, period_key, used_amount, used_asset, last_tx_id
) VALUES (
    $1, $2, $3, $4, $5
)
ON CONFLICT (policy_id, period_key) DO UPDATE
SET used_amount = GREATEST(usage_counters.used_amount, EXCLUDED.used_amount),
    used_asset  = EXCLUDED.used_asset,
    last_tx_id  = EXCLUDED.last_tx_id,
    flushed_at  = NOW()
RETURNING *;

-- name: GetUsageCounter :one
SELECT * FROM usage_counters
WHERE policy_id = $1 AND period_key = $2;

-- name: ListUsageCountersForPolicy :many
-- Used by the dashboard "remaining today / this week / this month" view.
SELECT * FROM usage_counters
WHERE policy_id = $1
ORDER BY period_key DESC;
