-- 000006_virtual_addresses.up.sql
-- TIP-1022 virtual addresses: deterministic per-(merchant-wallet, label)
-- deposit addresses. The merchant publishes a unique address per customer
-- (or invoice, or whatever business shard), and Tempo's AddressRegistry
-- precompile routes deposits back to the parent wallet.

CREATE TABLE virtual_addresses (
    id              TEXT PRIMARY KEY,                 -- vaddr_01J…
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    wallet_id       TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    group_id        TEXT NOT NULL REFERENCES wallet_groups(id) ON DELETE CASCADE,

    -- Customer-supplied shard label. Free-form for the customer but
    -- (wallet_id, label) is unique — the same label maps to the same
    -- on-chain address every time, which is the whole point.
    label           TEXT NOT NULL,

    -- The derived on-chain address (0x-prefixed). Returned by the
    -- precompile; we cache it so reads don't require an RPC roundtrip.
    address         TEXT NOT NULL,

    -- The transaction that registered the address with the precompile.
    -- Empty for rows minted before on-chain registration succeeded.
    registration_tx_hash TEXT,

    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (wallet_id, label)
);

CREATE INDEX idx_vaddr_tenant  ON virtual_addresses(tenant_id);
CREATE INDEX idx_vaddr_group   ON virtual_addresses(group_id);
CREATE INDEX idx_vaddr_address ON virtual_addresses(address);
