-- 000001_tenants_and_auth.down.sql
DROP TRIGGER IF EXISTS trg_users_touch   ON users;
DROP TRIGGER IF EXISTS trg_tenants_touch ON tenants;
DROP FUNCTION IF EXISTS touch_updated_at();

DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
