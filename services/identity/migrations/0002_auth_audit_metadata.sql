-- Forward repair for auth_audit.metadata.
--
-- Databases created before this column carried a default could reject inserts
-- that omitted it (NOT NULL without DEFAULT). The application always binds an
-- explicit value, but this migration makes the schema safe for any client and
-- for databases upgraded from older releases. Idempotent: re-running changes
-- nothing.
ALTER TABLE auth_audit ALTER COLUMN metadata SET DEFAULT '{}'::jsonb;
UPDATE auth_audit SET metadata = '{}'::jsonb WHERE metadata IS NULL;
ALTER TABLE auth_audit ALTER COLUMN metadata SET NOT NULL;
