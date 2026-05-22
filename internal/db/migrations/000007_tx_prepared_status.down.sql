-- Reverse of 000007. Any 'prepared' rows must be removed first or the new
-- CHECK will reject them.
DELETE FROM transactions WHERE status = 'prepared';

ALTER TABLE transactions DROP CONSTRAINT transactions_status_check;
ALTER TABLE transactions ADD  CONSTRAINT transactions_status_check
    CHECK (status IN ('pending', 'broadcast', 'succeeded', 'failed', 'cancelled'));

DROP INDEX IF EXISTS idx_tx_status;
CREATE INDEX idx_tx_status
    ON transactions(status)
    WHERE status IN ('pending', 'broadcast');
