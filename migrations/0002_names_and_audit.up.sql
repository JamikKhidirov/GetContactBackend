-- Caller-ID names contributed by the community and indexes for the admin audit
-- view. Names are one vote per user per number: updating a vote replaces the
-- previous name instead of inflating its weight.

CREATE TABLE IF NOT EXISTS number_names (
    id           UUID PRIMARY KEY,
    phone        VARCHAR(20) NOT NULL REFERENCES numbers (phone) ON DELETE CASCADE,
    name         VARCHAR(64) NOT NULL,
    submitted_by UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A user can vote only once per number; the vote can be changed later.
CREATE UNIQUE INDEX IF NOT EXISTS ux_number_names_phone_submitter
    ON number_names (phone, submitted_by) WHERE submitted_by IS NOT NULL;

CREATE INDEX IF NOT EXISTS ix_number_names_phone
    ON number_names (phone, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_number_names_phone_name
    ON number_names (phone, LOWER(name));

-- The audit feed is read newest-first without a type/aggregate filter.
CREATE INDEX IF NOT EXISTS ix_audit_events_consumed
    ON audit_events (consumed_at DESC);

DROP TRIGGER IF EXISTS trg_number_names_updated_at ON number_names;
CREATE TRIGGER trg_number_names_updated_at BEFORE UPDATE ON number_names
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
