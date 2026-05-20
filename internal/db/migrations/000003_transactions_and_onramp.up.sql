-- 000003_transactions_and_onramp.up.sql
-- Money-movement audit tables.
--
--   transactions   = every transfer (crypto), inbound + outbound + internal
--   onramp_orders  = every fiat → crypto purchase (CrossMint hosted checkout)
--
-- These are append-mostly; status fields advance via UPDATE but rows are
-- never deleted. Used for: dashboard, customer reports, reconciliation,
-- compliance audit.

-- ─── transactions ─────────────────────────────────────────────────────
CREATE TABLE transactions (
    id                  TEXT PRIMARY KEY,             -- tx_01J... (Atara id)
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- The wallet originating (or receiving) the transfer.
    wallet_id           TEXT NOT NULL REFERENCES wallets(id),
    group_id            TEXT NOT NULL REFERENCES wallet_groups(id),
    rail                TEXT NOT NULL,                 -- crossmint|tempo|loka-ln
    chain               TEXT NOT NULL,

    -- direction:
    --   outbound  = wallet → external (or another tenant's wallet)
    --   inbound   = external → wallet (discovered via webhook / poll)
    --   internal  = atara-internal book transfer (zero-fee)
    direction           TEXT NOT NULL,
    counterparty        TEXT NOT NULL,                 -- address or wallet id

    -- Amount in the asset's native precision (decimal string).
    amount              NUMERIC(38, 18) NOT NULL,
    asset               TEXT NOT NULL,                 -- USDC|pathUSD|sats|...
    -- Snapshot of USD-equivalent at submission time, for reporting only.
    -- NULL until pricing oracle resolves; not authoritative for refunds.
    amount_usd          NUMERIC(38, 18),

    status              TEXT NOT NULL DEFAULT 'pending',
                        -- pending|broadcast|succeeded|failed|cancelled

    -- On-chain artifacts (nullable until broadcast).
    tx_hash             TEXT,
    block_number        BIGINT,
    -- Provider-side id (CrossMint tx id, etc.). For self-broadcast Tempo
    -- transfers we set this = tx_hash for symmetry.
    provider_tx_id      TEXT,

    -- If a session key authorized this transfer.
    session_key_id      TEXT,                          -- FK added in 000004

    -- Audit: who initiated.
    initiated_by_user   TEXT REFERENCES users(id) ON DELETE SET NULL,
    initiated_by_apikey TEXT REFERENCES api_keys(id) ON DELETE SET NULL,

    -- Idempotency: client supplies a unique key per logical request; if the
    -- same key is replayed we return the previously-stored result instead
    -- of double-charging.
    idempotency_key     TEXT,

    error_code          TEXT,
    error_message       TEXT,

    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    broadcast_at        TIMESTAMPTZ,
    confirmed_at        TIMESTAMPTZ,

    CHECK (direction IN ('outbound', 'inbound', 'internal')),
    CHECK (status    IN ('pending', 'broadcast', 'succeeded', 'failed', 'cancelled'))
);

-- Idempotency is enforced per (tenant, key). Two different tenants can use
-- the same key; the same tenant cannot replay theirs.
CREATE UNIQUE INDEX idx_tx_idempotency
    ON transactions(tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Hot dashboard query: "latest transactions of this tenant".
CREATE INDEX idx_tx_tenant_time  ON transactions(tenant_id, created_at DESC);
CREATE INDEX idx_tx_wallet_time  ON transactions(wallet_id, created_at DESC);
CREATE INDEX idx_tx_group_time   ON transactions(group_id, created_at DESC);
CREATE INDEX idx_tx_status       ON transactions(status) WHERE status IN ('pending', 'broadcast');
CREATE INDEX idx_tx_hash         ON transactions(tx_hash) WHERE tx_hash IS NOT NULL;
CREATE INDEX idx_tx_provider     ON transactions(rail, provider_tx_id) WHERE provider_tx_id IS NOT NULL;

CREATE TRIGGER trg_tx_touch
    BEFORE UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- ─── onramp_orders ────────────────────────────────────────────────────
CREATE TABLE onramp_orders (
    id                  TEXT PRIMARY KEY,             -- ord_01J... (Atara id)
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Destination wallet — onramp always credits an Atara-managed wallet.
    wallet_id           TEXT NOT NULL REFERENCES wallets(id),
    group_id            TEXT NOT NULL REFERENCES wallet_groups(id),

    provider            TEXT NOT NULL,                 -- crossmint|moonpay|...
    provider_order_id   TEXT,                          -- e.g. CrossMint orderId
    checkout_url        TEXT,                          -- hosted page for the user

    -- Fiat side (what the user pays).
    fiat_amount         NUMERIC(38, 18) NOT NULL,
    fiat_currency       TEXT NOT NULL,                 -- ISO 4217: USD|EUR|...

    -- Crypto side (what we deliver to the wallet).
    asset               TEXT NOT NULL,                 -- USDC|pathUSD|...
    chain               TEXT NOT NULL,
    crypto_amount       NUMERIC(38, 18),               -- final delivered amount

    -- Status flow:
    --   pending → checkout_opened → paid → delivering → completed
    --                                              \→  failed
    --                                              \→  expired
    status              TEXT NOT NULL DEFAULT 'pending',

    initiated_by_apikey TEXT REFERENCES api_keys(id) ON DELETE SET NULL,
    idempotency_key     TEXT,

    error_code          TEXT,
    error_message       TEXT,
    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ,
    completed_at        TIMESTAMPTZ,

    CHECK (status IN ('pending', 'checkout_opened', 'paid', 'delivering',
                      'completed', 'failed', 'expired'))
);

CREATE UNIQUE INDEX idx_onramp_idempotency
    ON onramp_orders(tenant_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX idx_onramp_tenant_time ON onramp_orders(tenant_id, created_at DESC);
CREATE INDEX idx_onramp_wallet_time ON onramp_orders(wallet_id, created_at DESC);
CREATE INDEX idx_onramp_status      ON onramp_orders(status)
    WHERE status IN ('pending', 'checkout_opened', 'paid', 'delivering');
CREATE INDEX idx_onramp_provider    ON onramp_orders(provider, provider_order_id)
    WHERE provider_order_id IS NOT NULL;

CREATE TRIGGER trg_onramp_touch
    BEFORE UPDATE ON onramp_orders
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
