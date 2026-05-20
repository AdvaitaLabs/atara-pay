-- 000004_session_keys_and_limits.down.sql
DROP INDEX IF EXISTS idx_tx_session_key;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS fk_tx_session_key;

DROP TRIGGER IF EXISTS trg_sk_touch     ON session_keys;
DROP TRIGGER IF EXISTS trg_policy_touch ON limit_policies;

DROP TABLE IF EXISTS limit_violations;
DROP TABLE IF EXISTS usage_counters;
DROP TABLE IF EXISTS session_keys;
DROP TABLE IF EXISTS limit_policies;
