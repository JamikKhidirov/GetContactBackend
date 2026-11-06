-- Consent-based client/device metadata for refresh-token sessions.
--
-- The API only stores values the mobile client explicitly sends in the
-- X-Device-* / X-OS-* / X-App-* headers (or the optional device body object).
-- It never reads contacts, photos, files, messages or any other device data.
-- IP and User-Agent are derived from the HTTP request.

ALTER TABLE refresh_tokens
    ADD COLUMN IF NOT EXISTS device_id       VARCHAR(128),
    ADD COLUMN IF NOT EXISTS device_name     VARCHAR(128),
    ADD COLUMN IF NOT EXISTS device_model    VARCHAR(128),
    ADD COLUMN IF NOT EXISTS manufacturer    VARCHAR(128),
    ADD COLUMN IF NOT EXISTS os              VARCHAR(64),
    ADD COLUMN IF NOT EXISTS os_version      VARCHAR(64),
    ADD COLUMN IF NOT EXISTS app_version     VARCHAR(64),
    ADD COLUMN IF NOT EXISTS app_build       VARCHAR(64),
    ADD COLUMN IF NOT EXISTS client          VARCHAR(32),
    ADD COLUMN IF NOT EXISTS locale          VARCHAR(32),
    ADD COLUMN IF NOT EXISTS timezone        VARCHAR(64),
    ADD COLUMN IF NOT EXISTS network_type    VARCHAR(32),
    ADD COLUMN IF NOT EXISTS last_seen_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_ip         VARCHAR(64),
    ADD COLUMN IF NOT EXISTS last_user_agent VARCHAR(256);

-- The sessions screen and admin user summary read active sessions newest-first.
CREATE INDEX IF NOT EXISTS ix_refresh_tokens_user_last_seen
    ON refresh_tokens (user_id, last_seen_at DESC NULLS LAST)
    WHERE revoked_at IS NULL;
