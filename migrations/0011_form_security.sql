-- Rate limiting and one-time confirmation links for public signup forms.

CREATE TABLE IF NOT EXISTS form_submission_rate_limits (
    form_id           uuid NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    ip_address        inet NOT NULL,
    window_started_at timestamptz NOT NULL,
    request_count     integer NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    PRIMARY KEY (form_id, ip_address)
);

CREATE TABLE IF NOT EXISTS form_confirmation_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    form_id         uuid NOT NULL REFERENCES forms(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    token_hash      text NOT NULL UNIQUE,
    expires_at      timestamptz NOT NULL,
    confirmed_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS form_confirmation_tokens_contact_idx
    ON form_confirmation_tokens (contact_id, created_at DESC);
CREATE INDEX IF NOT EXISTS form_confirmation_tokens_expiry_idx
    ON form_confirmation_tokens (expires_at)
    WHERE confirmed_at IS NULL;
