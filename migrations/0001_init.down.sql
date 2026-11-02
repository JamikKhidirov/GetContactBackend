DROP TRIGGER IF EXISTS trg_numbers_updated_at ON numbers;
DROP TRIGGER IF EXISTS trg_contacts_updated_at ON contacts;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
DROP FUNCTION IF EXISTS set_updated_at();
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS spam_reports;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS numbers;
DROP TABLE IF EXISTS contacts;
DROP TABLE IF EXISTS users;
-- pg_trgm is intentionally left installed; other schemas may use it.
