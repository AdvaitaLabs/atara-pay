-- 000001_tenants_and_auth.up.sql
-- Tenants (customer companies), their human users (dashboard accounts),
-- and machine API keys (sk_test_* / sk_live_*).

-- ─── tenants ──────────────────────────────────────────────────────────
-- One row per customer company. The tenant_id is foreign-keyed by almost
-- every other table — it is ATARA-Pay's primary isolation boundary.
CREATE TABLE tenants (
    id              TEXT PRIMARY KEY,                -- ULID, e.g. tn_01J...
    name            TEXT NOT NULL,                   -- "AI-Tutor Inc"
    primary_email   TEXT NOT NULL UNIQUE,            -- billing + recovery
    plan            TEXT NOT NULL DEFAULT 'free',    -- free|starter|growth|enterprise|custom
    status          TEXT NOT NULL DEFAULT 'active',  -- active|suspended|deleted
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenants_status ON tenants(status);

-- ─── users ────────────────────────────────────────────────────────────
-- Humans who log into the dashboard. Always belong to exactly one tenant.
-- A user logs in with email + password; API access happens through api_keys.
CREATE TABLE users (
    id              TEXT PRIMARY KEY,                -- ULID, u_01J...
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email           TEXT NOT NULL,
    password_hash   TEXT NOT NULL,                   -- argon2id encoded string
    role            TEXT NOT NULL DEFAULT 'owner',   -- owner|admin|developer|finance|viewer
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at   TIMESTAMPTZ,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- An email can be reused across tenants, but must be unique inside one.
    UNIQUE (tenant_id, email)
);

CREATE INDEX idx_users_tenant ON users(tenant_id);

-- ─── api_keys ─────────────────────────────────────────────────────────
-- Machine credentials. We NEVER store the raw secret — only a SHA-256 hash
-- for lookup. The first 12 chars are kept in clear (key_prefix) to let users
-- recognize their keys in the dashboard ("sk_test_ab12cd34…").
CREATE TABLE api_keys (
    id              TEXT PRIMARY KEY,                -- ULID, ak_01J...
    tenant_id       TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    created_by      TEXT REFERENCES users(id) ON DELETE SET NULL,

    name            TEXT NOT NULL,                   -- "production-backend"
    environment     TEXT NOT NULL,                   -- 'test' | 'live'
    key_prefix      TEXT NOT NULL,                   -- visible portion, e.g. 'sk_test_ab12cd34'
    key_hash        BYTEA NOT NULL UNIQUE,           -- SHA-256 of the full key

    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (environment IN ('test', 'live'))
);

CREATE INDEX idx_api_keys_tenant ON api_keys(tenant_id);
CREATE INDEX idx_api_keys_active ON api_keys(tenant_id, environment) WHERE revoked_at IS NULL;

-- ─── audit trigger helpers (lightweight) ──────────────────────────────
-- Auto-bump updated_at on row changes. Used by tenants + users (api_keys is
-- effectively append-only modulo revoked_at, so no auto-touch needed).
CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_tenants_touch
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER trg_users_touch
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
