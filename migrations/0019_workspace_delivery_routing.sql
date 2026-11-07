DO $$
DECLARE
    duplicate_domains text;
BEGIN
    SELECT string_agg(
        format('%s (organizations: %s)', lower(domain), organizations),
        '; ' ORDER BY lower(domain)
    )
    INTO duplicate_domains
    FROM (
        SELECT lower(domain) AS domain,
               string_agg(organization_id::text, ', ' ORDER BY organization_id::text) AS organizations
        FROM sending_domains
        GROUP BY lower(domain)
        HAVING count(*) > 1
    ) duplicates;

    IF duplicate_domains IS NOT NULL THEN
        RAISE EXCEPTION 'Cannot enforce single-owner sending domains; resolve duplicate domain ownership first: %', duplicate_domains;
    END IF;
END $$;

ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT 'stalwart',
    ADD COLUMN IF NOT EXISTS resend_domain_id text,
    ADD COLUMN IF NOT EXISTS resend_dns_records jsonb NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'sending_domains_provider_check'
          AND conrelid = 'sending_domains'::regclass
    ) THEN
        ALTER TABLE sending_domains
            ADD CONSTRAINT sending_domains_provider_check
            CHECK (provider IN ('stalwart', 'resend'));
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS sending_domains_domain_global_idx
    ON sending_domains (lower(domain));

CREATE TABLE IF NOT EXISTS workspace_delivery_settings (
    workspace_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    provider text NOT NULL DEFAULT 'stalwart' CHECK (provider IN ('stalwart', 'resend')),
    resend_api_key_enc bytea,
    fallback_provider text CHECK (fallback_provider IS NULL OR fallback_provider IN ('stalwart', 'resend')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT workspace_delivery_settings_fallback_distinct
        CHECK (fallback_provider IS NULL OR fallback_provider <> provider)
);
