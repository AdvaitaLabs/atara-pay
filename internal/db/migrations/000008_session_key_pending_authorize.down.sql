-- Reverse of 000008. Any pending_authorize rows must be removed first.
DELETE FROM session_keys WHERE status = 'pending_authorize';

ALTER TABLE session_keys DROP CONSTRAINT session_keys_status_check;
ALTER TABLE session_keys ADD  CONSTRAINT session_keys_status_check
    CHECK (status IN ('active', 'rotating', 'expired', 'revoked'));
