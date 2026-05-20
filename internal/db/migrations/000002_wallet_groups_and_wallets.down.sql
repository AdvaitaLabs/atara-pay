-- 000002_wallet_groups_and_wallets.down.sql
DROP TRIGGER IF EXISTS trg_wallets_touch ON wallets;
DROP TRIGGER IF EXISTS trg_wg_touch      ON wallet_groups;

DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS wallet_groups;
