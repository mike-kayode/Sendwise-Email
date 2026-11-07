CREATE TABLE IF NOT EXISTS mail_delivery_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    action text NOT NULL,
    encrypted_message bytea NOT NULL,
    attempts integer NOT NULL DEFAULT 1 CHECK (attempts BETWEEN 1 AND 5),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_token uuid,
    lease_expires_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX IF NOT EXISTS mail_delivery_outbox_pending_idx
    ON mail_delivery_outbox (next_attempt_at, created_at)
    WHERE attempts < 5;
