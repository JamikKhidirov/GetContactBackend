-- Email OTP authentication and reliable email delivery.
--
-- Codes are never stored in plaintext: code_hash is an HMAC used for constant
-- time verification, and code_ciphertext is AES-GCM encrypted so only the
-- worker can decrypt it right before handing it to the SMTP provider.
--
-- email_outbox implements the transactional outbox pattern: the API writes the
-- verification and the Kafka event in one database transaction. A relay then
-- publishes pending rows to Kafka, so a crash between DB commit and Kafka
-- publish cannot lose the email.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS email_verified    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS email_verifications (
    id                 UUID PRIMARY KEY,
    email              VARCHAR(254) NOT NULL,
    user_id            UUID REFERENCES users(id) ON DELETE CASCADE,
    purpose            VARCHAR(24)  NOT NULL CHECK (purpose IN ('signup', 'login', 'verify_email')),
    code_hash          CHAR(64)     NOT NULL,
    code_ciphertext    TEXT         NOT NULL,
    attempts           INTEGER      NOT NULL DEFAULT 0,
    max_attempts       INTEGER      NOT NULL DEFAULT 5,
    delivery_attempts  INTEGER      NOT NULL DEFAULT 0,
    request_ip         VARCHAR(64),
    user_agent         VARCHAR(256),
    expires_at         TIMESTAMPTZ  NOT NULL,
    consumed_at        TIMESTAMPTZ,
    delivered_at       TIMESTAMPTZ,
    delivery_error     TEXT,
    last_attempt_at    TIMESTAMPTZ,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ix_email_verifications_lookup
    ON email_verifications (LOWER(email), purpose, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_email_verifications_expiry
    ON email_verifications (expires_at) WHERE consumed_at IS NULL;
CREATE INDEX IF NOT EXISTS ix_email_verifications_pending_delivery
    ON email_verifications (created_at) WHERE delivered_at IS NULL;

CREATE TABLE IF NOT EXISTS email_outbox (
    event_id     UUID PRIMARY KEY,
    event_type   VARCHAR(64)  NOT NULL,
    event_version INTEGER     NOT NULL DEFAULT 1,
    aggregate_id VARCHAR(128) NOT NULL,
    payload      JSONB        NOT NULL,
    occurred_at  TIMESTAMPTZ  NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER      NOT NULL DEFAULT 0,
    last_error   TEXT
);

CREATE INDEX IF NOT EXISTS ix_email_outbox_pending
    ON email_outbox (created_at) WHERE published_at IS NULL;
