ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS dns_check_details jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS sender_profiles (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    company_name text NOT NULL DEFAULT '',
    street_address text NOT NULL DEFAULT '',
    city text NOT NULL DEFAULT '',
    region text NOT NULL DEFAULT '',
    postal_code text NOT NULL DEFAULT '',
    country text NOT NULL DEFAULT '',
    website text NOT NULL DEFAULT '',
    support_email text NOT NULL DEFAULT '',
    footer_text text NOT NULL DEFAULT '',
    social_links jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sender_identities (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain text NOT NULL,
    local_part text NOT NULL CHECK (local_part ~ '^[a-z0-9._+-]{1,64}$'),
    display_name text NOT NULL,
    reply_to text NOT NULL DEFAULT '',
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS sender_identities_domain_local_part_uidx
    ON sender_identities (lower(domain), lower(local_part));
CREATE INDEX IF NOT EXISTS sender_identities_organization_idx
    ON sender_identities (organization_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS sender_identities_one_default_uidx
    ON sender_identities (organization_id) WHERE is_default;

ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS sender_identity_id uuid
        REFERENCES sender_identities(id) ON DELETE SET NULL;
ALTER TABLE templates
    ADD COLUMN IF NOT EXISTS sender_identity_id uuid
        REFERENCES sender_identities(id) ON DELETE SET NULL;
ALTER TABLE templates
    ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'html';
ALTER TABLE templates
    ADD COLUMN IF NOT EXISTS theme jsonb NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'templates_mode_check'
          AND conrelid = 'templates'::regclass
    ) THEN
        ALTER TABLE templates
            ADD CONSTRAINT templates_mode_check CHECK (mode IN ('plain', 'html'));
    END IF;
END
$$;
