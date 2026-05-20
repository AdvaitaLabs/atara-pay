-- 000002_wallet_groups_and_wallets.up.sql
-- Wallet groups + their constituent wallets.
--
-- The "dual-rail by default" decision means a single owner (user, agent,
-- merchant, treasury) gets ONE wallet_group, which contains MULTIPLE
-- wallets — one per rail (CrossMint, Tempo, eventually Loka-LN).
--
--    customer's "wallet"  =  wallet_group
--    actual on-chain account  =  wallets row

-- ─── wallet_groups ────────────────────────────────────────────────────
CREATE TABLE wallet_groups (
    id              TEXT PRIMARY KEY,                -- wg_01J...
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Who owns this group. owner_ref is the customer's external identifier
    -- — we never assign meaning to it, it's their key for lookups.
    owner_type      TEXT NOT NULL,                   -- user|agent|merchant|treasury
    owner_ref       TEXT NOT NULL,                   -- e.g. "user_alice", "agent_alice_tutor"

    -- If owner_type='agent', parent_group_id points at the user_group that
    -- pays for the agent. NULL for everything else.
    parent_group_id TEXT REFERENCES wallet_groups(id) ON DELETE SET NULL,

    -- Default custody mode for new wallets created in this group. Individual
    -- wallets may override this (see wallets.custody).
    custody         TEXT NOT NULL DEFAULT 'platform', -- platform|user|mpc

    display_name    TEXT,                            -- "Alice's Wallets"
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,

    CHECK (owner_type IN ('user', 'agent', 'merchant', 'treasury')),
    CHECK (custody    IN ('platform', 'user', 'mpc')),

    -- One owner_ref per tenant. Re-registering should reuse the same group.
    UNIQUE (tenant_id, owner_ref)
);

CREATE INDEX idx_wg_tenant         ON wallet_groups(tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_wg_owner_type     ON wallet_groups(tenant_id, owner_type) WHERE deleted_at IS NULL;
CREATE INDEX idx_wg_parent         ON wallet_groups(parent_group_id) WHERE parent_group_id IS NOT NULL;

CREATE TRIGGER trg_wg_touch
    BEFORE UPDATE ON wallet_groups
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

-- ─── wallets ──────────────────────────────────────────────────────────
-- One wallet = one on-chain account on one rail.
-- A wallet_group typically holds 2-3 wallets (one per supported rail).
CREATE TABLE wallets (
    id                    TEXT PRIMARY KEY,           -- wlt_01J...
    group_id              TEXT NOT NULL REFERENCES wallet_groups(id) ON DELETE CASCADE,
    tenant_id             TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
                          -- denormalized so every tenant-scoped query can
                          -- filter without joining wallet_groups

    rail                  TEXT NOT NULL,              -- crossmint|tempo|loka-ln
    chain                 TEXT NOT NULL,              -- base|ethereum|solana|tempo|...
    address               TEXT NOT NULL,              -- 0xA1B2... or solana-style
    provider_locator      TEXT NOT NULL,              -- CrossMint cm_xxx; Tempo = address

    custody               TEXT NOT NULL DEFAULT 'platform',  -- platform|user|mpc

    -- Encrypted private key, ONLY for rails we self-custody (currently Tempo
    -- when custody='platform'). Encryption is AES-256-GCM with a master key
    -- — see internal/keystore. key_version tracks the master-key generation
    -- for rotation.
    encrypted_private_key BYTEA,
    key_version           SMALLINT,

    status                TEXT NOT NULL DEFAULT 'active',  -- active|frozen|deleted
    metadata              JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (rail    IN ('crossmint', 'tempo', 'loka-ln')),
    CHECK (custody IN ('platform', 'user', 'mpc')),
    CHECK (status  IN ('active', 'frozen', 'deleted')),
    -- A group has at most one wallet per (rail, chain). Re-creating with
    -- different chain on the same rail is legal (e.g. CrossMint Base +
    -- CrossMint Solana).
    UNIQUE (group_id, rail, chain),
    -- Encrypted key only makes sense when we self-custody.
    CHECK (
        (custody = 'platform' AND rail = 'tempo' AND encrypted_private_key IS NOT NULL)
        OR encrypted_private_key IS NULL
    )
);

CREATE INDEX idx_wallets_tenant      ON wallets(tenant_id) WHERE status = 'active';
CREATE INDEX idx_wallets_group       ON wallets(group_id);
CREATE INDEX idx_wallets_rail        ON wallets(tenant_id, rail) WHERE status = 'active';
-- Lookup by on-chain address (e.g. incoming-transfer webhook needs to find
-- "which wallet does this address belong to?").
CREATE INDEX idx_wallets_address     ON wallets(address) WHERE status = 'active';
-- Lookup by provider locator (e.g. CrossMint webhook hands us cm_xxx).
CREATE INDEX idx_wallets_provider    ON wallets(rail, provider_locator) WHERE status = 'active';

CREATE TRIGGER trg_wallets_touch
    BEFORE UPDATE ON wallets
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
