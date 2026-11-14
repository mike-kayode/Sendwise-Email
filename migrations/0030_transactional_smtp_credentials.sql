ALTER TABLE transactional_smtp_credentials
    ADD COLUMN IF NOT EXISTS username text;

ALTER TABLE transactional_smtp_credentials
    ADD COLUMN IF NOT EXISTS rate_window_started_at timestamptz;

ALTER TABLE transactional_smtp_credentials
    ADD COLUMN IF NOT EXISTS rate_window_count integer NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS transactional_smtp_credentials_username_uidx
    ON transactional_smtp_credentials (username)
    WHERE username IS NOT NULL;
