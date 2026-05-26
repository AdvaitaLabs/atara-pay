-- 000008_session_key_pending_authorize.up.sql
-- Add 'pending_authorize' to session_keys.status CHECK. This status covers
-- the window between (a) Atara generating + persisting a session keypair
-- for a user-custody parent wallet and (b) the wallet owner broadcasting
-- the on-chain authorizeKey tx. While in this state the key is INACTIVE
-- — gateway-level limit checks reject it, and the precompile would reject
-- any spend signed with its private bytes anyway.

ALTER TABLE session_keys DROP CONSTRAINT session_keys_status_check;
ALTER TABLE session_keys ADD  CONSTRAINT session_keys_status_check
    CHECK (status IN ('pending_authorize', 'active', 'rotating', 'expired', 'revoked'));

-- The hot tenant / wallet / group indices already filter on status='active';
-- pending_authorize rows are deliberately excluded from those so a half-
-- finished mint doesn't pollute "active session keys" UIs.
