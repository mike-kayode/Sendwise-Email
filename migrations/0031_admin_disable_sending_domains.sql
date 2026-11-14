ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS admin_disabled_at timestamptz;
