ALTER TABLE transactional_api_keys
    ADD COLUMN IF NOT EXISTS expires_at timestamptz;

CREATE INDEX IF NOT EXISTS transactional_api_keys_expiry_idx
    ON transactional_api_keys (expires_at)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;
