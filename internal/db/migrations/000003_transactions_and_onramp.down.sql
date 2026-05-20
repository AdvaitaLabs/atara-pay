-- 000003_transactions_and_onramp.down.sql
DROP TRIGGER IF EXISTS trg_onramp_touch ON onramp_orders;
DROP TRIGGER IF EXISTS trg_tx_touch     ON transactions;

DROP TABLE IF EXISTS onramp_orders;
DROP TABLE IF EXISTS transactions;
