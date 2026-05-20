-- M9 accounting / statements queries.
--
-- These are pure read aggregates over the existing transactions +
-- onramp_orders tables. No new schema needed — we compute totals on the
-- fly. Once volume justifies it, M9.2 can pre-materialize a
-- monthly_statements rollup.

-- name: GroupBalanceSummary :many
-- Sum of succeeded outbound transfers BY asset for a wallet group across
-- all rails. Plus inbound (received) totals for completeness. Caller
-- subtracts to compute "net spent" in the dashboard if they want.
SELECT
    asset,
    direction,
    COUNT(*)        AS tx_count,
    SUM(amount)::TEXT AS total_amount
FROM transactions
WHERE group_id = $1
  AND status = 'succeeded'
GROUP BY asset, direction
ORDER BY asset ASC, direction ASC;

-- name: TenantMonthlyStatement :many
-- Per-(asset, direction) totals for a tenant in one month. The month
-- boundary is computed by the caller; pass start (inclusive) and end
-- (exclusive). Index hit: tenant_id is the most-selective FK on
-- transactions, then created_at limits the scan further.
SELECT
    asset,
    direction,
    COUNT(*)        AS tx_count,
    SUM(amount)::TEXT AS total_amount
FROM transactions
WHERE tenant_id = $1
  AND status = 'succeeded'
  AND created_at >= $2
  AND created_at <  $3
GROUP BY asset, direction
ORDER BY asset ASC, direction ASC;

-- name: TenantMonthlyOnramp :many
-- Same window applied to onramp_orders. Fiat IN totals separately from
-- crypto delivered so customers can see "you bought $X of USDC via N
-- transactions" without joining tables.
SELECT
    fiat_currency,
    asset,
    COUNT(*)            AS order_count,
    SUM(fiat_amount)::TEXT   AS fiat_total,
    SUM(crypto_amount)::TEXT AS crypto_delivered
FROM onramp_orders
WHERE tenant_id = $1
  AND status = 'completed'
  AND created_at >= $2
  AND created_at <  $3
GROUP BY fiat_currency, asset
ORDER BY fiat_currency ASC, asset ASC;
