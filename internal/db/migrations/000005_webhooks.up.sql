-- 000005_webhooks.up.sql
-- Webhook subscriptions and the per-attempt delivery log.
--
-- Customers register endpoints (HTTPS URLs) and a list of event types they
-- care about. Every domain event Atara emits is recorded once per matching
-- endpoint in webhook_events, then a worker delivers it with exponential
-- backoff. Customers verify authenticity via an HMAC signature using the
-- endpoint's secret.

-- ─── webhook_endpoints ────────────────────────────────────────────────
CREATE TABLE webhook_endpoints (
    id                      TEXT PRIMARY KEY,         -- we_01J...
    tenant_id               TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    url                     TEXT NOT NULL,            -- https://customer.com/atara
    -- Random secret per endpoint. We sign each delivery with
    -- HMAC-SHA256(secret, body) and put it in the X-Atara-Signature header.
    secret                  BYTEA NOT NULL,
    -- HMAC secret version, supports rotation without dropping in-flight
    -- deliveries.
    secret_version          SMALLINT NOT NULL DEFAULT 1,

    -- JSON array of event type strings the customer subscribes to. An
    -- empty array means "all events".
    subscribed_events       JSONB NOT NULL DEFAULT '[]'::jsonb,

    status                  TEXT NOT NULL DEFAULT 'active',
                            -- active|paused|deleted

    description             TEXT,                     -- optional human label

    -- Operational health snapshot, updated as deliveries land.
    last_success_at         TIMESTAMPTZ,
    last_failure_at         TIMESTAMPTZ,
    consecutive_failures    INTEGER NOT NULL DEFAULT 0,

    metadata                JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (status IN ('active', 'paused', 'deleted'))
);

CREATE INDEX idx_we_tenant ON webhook_endpoints(tenant_id) WHERE status = 'active';

CREATE TRIGGER trg_we_touch
    BEFORE UPDATE ON webhook_endpoints
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- ─── webhook_events ───────────────────────────────────────────────────
-- One row per (event × subscribed_endpoint). The event_id itself serves
-- as the idempotency key — we put it in the X-Atara-Event-Id header so
-- customers can dedupe on their side.
CREATE TABLE webhook_events (
    id                  TEXT PRIMARY KEY,             -- evt_01J...
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    endpoint_id         TEXT REFERENCES webhook_endpoints(id) ON DELETE SET NULL,

    event_type          TEXT NOT NULL,                -- onramp.completed, etc.
    event_data          JSONB NOT NULL,               -- full payload

    -- Resource pointers (denormalized for indexable lookup from the
    -- dashboard "events related to wallet X" view).
    related_wallet_id      TEXT,                      -- not FK: events outlive wallets
    related_group_id       TEXT,
    related_transaction_id TEXT,
    related_onramp_order_id TEXT,
    related_session_key_id TEXT,

    -- Delivery state machine.
    status              TEXT NOT NULL DEFAULT 'pending',
                        -- pending|delivering|delivered|failed|dead
    attempts            INTEGER NOT NULL DEFAULT 0,
    next_attempt_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_response_code  INTEGER,
    last_response_body  TEXT,                         -- truncated to ~2KB
    last_error          TEXT,
    delivered_at        TIMESTAMPTZ,

    -- Signature versioning lets us tell customers which secret hashed this
    -- request (helps during rotation).
    signature_version   SMALLINT,

    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (status IN ('pending', 'delivering', 'delivered', 'failed', 'dead'))
);

-- Dashboard listing: latest events for a tenant.
CREATE INDEX idx_event_tenant_time ON webhook_events(tenant_id, created_at DESC);

-- The retry worker scans this constantly: "give me all events whose status
-- is pending/failed AND whose next_attempt_at is due."
CREATE INDEX idx_event_due
    ON webhook_events(next_attempt_at)
    WHERE status IN ('pending', 'failed');

-- Per-endpoint debug: "show me all events sent to this endpoint."
CREATE INDEX idx_event_endpoint ON webhook_events(endpoint_id, created_at DESC)
    WHERE endpoint_id IS NOT NULL;

-- Resource cross-reference (dashboard: "events on this wallet/tx/order").
CREATE INDEX idx_event_wallet ON webhook_events(related_wallet_id)
    WHERE related_wallet_id IS NOT NULL;
CREATE INDEX idx_event_tx     ON webhook_events(related_transaction_id)
    WHERE related_transaction_id IS NOT NULL;
CREATE INDEX idx_event_onramp ON webhook_events(related_onramp_order_id)
    WHERE related_onramp_order_id IS NOT NULL;

CREATE TRIGGER trg_event_touch
    BEFORE UPDATE ON webhook_events
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
