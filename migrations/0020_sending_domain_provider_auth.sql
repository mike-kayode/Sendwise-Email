ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS stalwart_authenticated boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS resend_authenticated boolean NOT NULL DEFAULT false;

UPDATE sending_domains
SET stalwart_authenticated = true
WHERE provider = 'stalwart' AND status = 'verified' AND stalwart_authenticated = false;

UPDATE sending_domains
SET resend_authenticated = true
WHERE provider = 'resend' AND status = 'verified' AND resend_authenticated = false;
