-- 000004_session_keys_and_limits.up.sql
-- Spending-control plane: session keys (delegated signers with quotas) and
-- the limit policy / usage counter machinery.
--
-- Conceptual layering:
--
--   limit_policies   declarative rules (per_tx / daily / weekly / monthly
--                    caps, recipient lists, expiry). Attached to a scope:
--                    tenant_default | wallet_group | wallet | session_key.
--
--   usage_counters   periodic accumulators. Redis is the hot-path primary;
--                    this table is the durable mirror flushed periodically
--                    so audits and dashboards survive a Redis flush.
--
--   limit_violations append-only log of every rejected attempt.
--
--   session_keys     delegated signers each pointing at exactly one policy.

-- ─── limit_policies ───────────────────────────────────────────────────
CREATE TABLE limit_policies (
    id                  TEXT PRIMARY KEY,             -- pol_01J...
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Polymorphic scope: a policy is attached to one of:
    --   tenant_default  (scope_id NULL)            — fallback for the tenant
    --   wallet_group    (scope_id = wallet_groups.id)
    --   wallet          (scope_id = wallets.id)
    --   session_key     (scope_id = session_keys.id) — set after the
    --                                                  session_key row exists
    scope_type          TEXT NOT NULL,
    scope_id            TEXT,

    -- Per-transaction cap.
    per_tx_amount       NUMERIC(38, 18),
    per_tx_asset        TEXT,

    -- Period caps. NULL = unlimited for that period.
    daily_amount        NUMERIC(38, 18),
    weekly_amount       NUMERIC(38, 18),
    monthly_amount      NUMERIC(38, 18),
    period_asset        TEXT NOT NULL DEFAULT 'USDC',

    -- Period alignment.
    timezone            TEXT NOT NULL DEFAULT 'UTC',
    reset_day_of_week   SMALLINT NOT NULL DEFAULT 1,   -- 1 = Mon (ISO-8601)
    reset_day_of_month  SMALLINT NOT NULL DEFAULT 1,

    -- Recipient gating. JSON arrays of strings — each entry is either an
    -- on-chain address ("0xABC...") or a merchant identifier
    -- ("merchant:openai"). allowed list is whitelist mode when non-empty;
    -- otherwise denied list acts as blacklist.
    allowed_recipients  JSONB NOT NULL DEFAULT '[]'::jsonb,
    denied_recipients   JSONB NOT NULL DEFAULT '[]'::jsonb,

    expires_at          TIMESTAMPTZ,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,

    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (scope_type IN ('tenant_default', 'wallet_group', 'wallet', 'session_key')),
    CHECK ((scope_type = 'tenant_default' AND scope_id IS NULL)
        OR (scope_type <> 'tenant_default' AND scope_id IS NOT NULL)),
    CHECK (reset_day_of_week  BETWEEN 1 AND 7),
    CHECK (reset_day_of_month BETWEEN 1 AND 28)
);

CREATE INDEX idx_policy_tenant     ON limit_policies(tenant_id) WHERE enabled = TRUE;
CREATE INDEX idx_policy_scope      ON limit_policies(scope_type, scope_id) WHERE enabled = TRUE;
-- Tenant has at most one tenant_default policy.
CREATE UNIQUE INDEX idx_policy_default_unique
    ON limit_policies(tenant_id)
    WHERE scope_type = 'tenant_default' AND enabled = TRUE;

CREATE TRIGGER trg_policy_touch
    BEFORE UPDATE ON limit_policies
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- ─── session_keys ─────────────────────────────────────────────────────
CREATE TABLE session_keys (
    id                  TEXT PRIMARY KEY,             -- sk_01J...
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    wallet_id           TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    group_id            TEXT NOT NULL REFERENCES wallet_groups(id) ON DELETE CASCADE,

    name                TEXT NOT NULL,                -- "alice-ai-tutor"

    -- The delegated signer's public address (EVM-style 0x...).
    public_address      TEXT NOT NULL,
    -- AES-256-GCM ciphertext of the secp256k1 private key.
    encrypted_priv_key  BYTEA NOT NULL,
    key_version         SMALLINT NOT NULL,

    -- The limit policy attached to this session key. May be NULL initially
    -- (created in the same transaction as the session_key); enforced
    -- non-NULL at the application layer.
    policy_id           TEXT REFERENCES limit_policies(id) ON DELETE RESTRICT,

    -- Rotation strategy.
    rotation_mode       TEXT NOT NULL DEFAULT 'auto_rotate',
                        -- auto_rotate|notify_only|hard_expire
    rotation_interval_s INTEGER NOT NULL DEFAULT 86400,  -- 24h
    next_rotation_at    TIMESTAMPTZ,

    expires_at          TIMESTAMPTZ NOT NULL,
    status              TEXT NOT NULL DEFAULT 'active',
                        -- active|rotating|expired|revoked
    revoked_reason      TEXT,
    revoked_at          TIMESTAMPTZ,

    -- Whether the rail enforces this session key natively on-chain. True
    -- for Tempo (via AccountKeychain precompile); false for CrossMint.
    rail_native         BOOLEAN NOT NULL DEFAULT FALSE,
    -- Tempo authorizeKey tx hash, when applicable.
    on_chain_tx_hash    TEXT,

    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (rotation_mode IN ('auto_rotate', 'notify_only', 'hard_expire')),
    CHECK (status        IN ('active', 'rotating', 'expired', 'revoked'))
);

CREATE INDEX idx_sk_tenant            ON session_keys(tenant_id) WHERE status = 'active';
CREATE INDEX idx_sk_wallet            ON session_keys(wallet_id) WHERE status = 'active';
CREATE INDEX idx_sk_group             ON session_keys(group_id) WHERE status = 'active';
CREATE INDEX idx_sk_address           ON session_keys(public_address) WHERE status = 'active';
-- Background worker scans this to drive auto-rotate cron.
CREATE INDEX idx_sk_next_rotation
    ON session_keys(next_rotation_at)
    WHERE rotation_mode = 'auto_rotate' AND status = 'active';
-- Background worker scans this to flip 'expired' status post-expiry.
CREATE INDEX idx_sk_expires
    ON session_keys(expires_at)
    WHERE status IN ('active', 'rotating');

CREATE TRIGGER trg_sk_touch
    BEFORE UPDATE ON session_keys
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- Now that session_keys exists, complete the FK from transactions.
ALTER TABLE transactions
    ADD CONSTRAINT fk_tx_session_key
    FOREIGN KEY (session_key_id) REFERENCES session_keys(id) ON DELETE SET NULL;

CREATE INDEX idx_tx_session_key ON transactions(session_key_id)
    WHERE session_key_id IS NOT NULL;

-- ─── usage_counters ───────────────────────────────────────────────────
-- Durable mirror of the hot-path Redis counters. Atara flushes Redis
-- accumulators here on a slow cadence (e.g. every 60s) so a Redis flush
-- doesn't lose audit history. The (policy_id, period_key) tuple is the
-- composite key.
CREATE TABLE usage_counters (
    policy_id           TEXT NOT NULL REFERENCES limit_policies(id) ON DELETE CASCADE,
    -- period_key formats:
    --   "daily:2026-05-20"
    --   "weekly:2026-W21"
    --   "monthly:2026-05"
    period_key          TEXT NOT NULL,

    used_amount         NUMERIC(38, 18) NOT NULL DEFAULT 0,
    used_asset          TEXT NOT NULL,

    last_tx_id          TEXT REFERENCES transactions(id) ON DELETE SET NULL,
    flushed_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (policy_id, period_key)
);

CREATE INDEX idx_usage_period ON usage_counters(period_key);

-- ─── limit_violations ─────────────────────────────────────────────────
-- Append-only audit log of every rejected attempt. High write volume but
-- read mostly by tenants in the dashboard ("show me when AI agent X got
-- blocked"), so BIGSERIAL is fine — we never need cross-tenant ordering.
CREATE TABLE limit_violations (
    id                  BIGSERIAL PRIMARY KEY,
    tenant_id           TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    policy_id           TEXT REFERENCES limit_policies(id) ON DELETE SET NULL,
    wallet_id           TEXT REFERENCES wallets(id) ON DELETE SET NULL,
    session_key_id      TEXT REFERENCES session_keys(id) ON DELETE SET NULL,
    transaction_id      TEXT REFERENCES transactions(id) ON DELETE SET NULL,

    violation_type      TEXT NOT NULL,
                        -- per_tx_exceeded|daily_exceeded|weekly_exceeded|
                        -- monthly_exceeded|recipient_denied|
                        -- recipient_not_allowlisted|policy_expired|
                        -- session_key_expired

    attempted_amount    NUMERIC(38, 18),
    attempted_asset     TEXT,
    attempted_recipient TEXT,
    limit_value         NUMERIC(38, 18),
    current_used        NUMERIC(38, 18),

    metadata            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (violation_type IN (
        'per_tx_exceeded', 'daily_exceeded', 'weekly_exceeded',
        'monthly_exceeded', 'recipient_denied', 'recipient_not_allowlisted',
        'policy_expired', 'session_key_expired'
    ))
);

CREATE INDEX idx_violation_tenant_time ON limit_violations(tenant_id, created_at DESC);
CREATE INDEX idx_violation_wallet      ON limit_violations(wallet_id) WHERE wallet_id IS NOT NULL;
CREATE INDEX idx_violation_sk          ON limit_violations(session_key_id) WHERE session_key_id IS NOT NULL;
