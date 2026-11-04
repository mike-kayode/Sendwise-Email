ALTER TABLE contact_imports
    ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS lease_token uuid;

CREATE INDEX IF NOT EXISTS contact_imports_org_created_idx
    ON contact_imports (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS contact_imports_due_idx
    ON contact_imports (next_attempt_at, created_at)
    WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS contact_imports_lease_idx
    ON contact_imports (lease_expires_at)
    WHERE status = 'processing';

DROP TRIGGER IF EXISTS contact_imports_set_updated_at ON contact_imports;
CREATE TRIGGER contact_imports_set_updated_at
    BEFORE UPDATE ON contact_imports
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
