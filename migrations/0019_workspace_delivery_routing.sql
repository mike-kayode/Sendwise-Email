-- Stalwart-only campaign delivery.
--
-- Campaign sending domains are hosted on Stalwart only: there is no second
-- provider to select per workspace, so this migration adds Stalwart state and
-- a non-destructive ownership conflict marker used by the API.
-- Provider-specific campaign configuration is not part of this schema.

ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS stalwart_authenticated boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS owner_conflict boolean NOT NULL DEFAULT false;

WITH ranked_domains AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY lower(domain)
               ORDER BY (status = 'verified') DESC, created_at ASC, id ASC
           ) AS owner_rank
    FROM sending_domains
)
UPDATE sending_domains AS domain
SET owner_conflict = ranked_domains.owner_rank > 1
FROM ranked_domains
WHERE domain.id = ranked_domains.id;

CREATE UNIQUE INDEX IF NOT EXISTS sending_domains_domain_global_idx
    ON sending_domains (lower(domain))
    WHERE NOT owner_conflict;
