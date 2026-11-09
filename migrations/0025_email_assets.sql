CREATE TABLE IF NOT EXISTS email_assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    content_type text NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/gif')),
    content bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS email_assets_organization_idx
    ON email_assets (organization_id, created_at);
