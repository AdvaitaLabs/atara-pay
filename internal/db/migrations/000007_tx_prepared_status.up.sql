-- 000007_tx_prepared_status.up.sql
-- Add 'prepared' to the transactions.status CHECK constraint so the
-- M14 prepare/submit flow (user-custody wallets) can persist an unsigned
-- transaction prior to the client returning the signed bytes.
--
-- A 'prepared' row carries the unsigned-tx blob in metadata.unsigned_tx
-- and has tx_hash IS NULL. Once the client submits a signed blob and we
-- broadcast, the row flips to 'broadcast' and tx_hash populates.

ALTER TABLE transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE transactions ADD  CONSTRAINT transactions_status_check
    CHECK (status IN ('prepared', 'pending', 'broadcast', 'succeeded', 'failed', 'cancelled'));

-- Extend the hot-pending partial index to include prepared rows. A prepare
-- followed by no submit is a "stuck" tx — operators want to see it in the
-- same view as broadcast / pending.
DROP INDEX IF EXISTS idx_tx_status;
CREATE INDEX idx_tx_status
    ON transactions(status)
    WHERE status IN ('prepared', 'pending', 'broadcast');
