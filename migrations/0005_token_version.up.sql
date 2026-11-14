-- Token versioning makes access-token revocation immediate.
--
-- The JWT carries the value of users.token_version. Every request compares it
-- with the current row, so blocking a user, deleting an account, changing a
-- password or promoting an account invalidates every already-issued access
-- token without waiting for the 15-minute JWT TTL.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS token_version INTEGER NOT NULL DEFAULT 0;
