-- Stalwart-only campaign delivery.
--
-- Campaign sending domains are hosted on Stalwart only: there is no second
-- provider to select per workspace, so this migration adds the one column the
-- API reads stalwart_authenticated; provider-specific campaign configuration
-- is not part of this schema.
--
-- The duplicate-ownership guard below is intentionally strict until the next
-- migration revision adds non-destructive conflict handling.

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
    ADD COLUMN IF NOT EXISTS stalwart_authenticated boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS sending_domains_domain_global_idx
    ON sending_domains (lower(domain));
