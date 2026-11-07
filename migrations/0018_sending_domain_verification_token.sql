ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS verification_token text NOT NULL DEFAULT gen_random_uuid()::text;

CREATE UNIQUE INDEX IF NOT EXISTS sending_domains_verification_token_idx
    ON sending_domains (verification_token);
