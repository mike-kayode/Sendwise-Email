CREATE TABLE IF NOT EXISTS event_outbox (
    event_id         uuid PRIMARY KEY,
    subject          text NOT NULL CHECK (length(trim(subject)) > 0),
    payload          jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at       timestamptz NOT NULL DEFAULT now(),
    published_at     timestamptz,
    attempts         integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    lease_token      uuid,
    lease_expires_at timestamptz,
    last_error       text,
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX IF NOT EXISTS event_outbox_pending_idx
    ON event_outbox (next_attempt_at, created_at)
    WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS event_inbox (
    consumer_name text NOT NULL CHECK (length(trim(consumer_name)) > 0),
    event_id      uuid NOT NULL,
    received_at   timestamptz NOT NULL DEFAULT now(),
    processed_at  timestamptz,
    PRIMARY KEY (consumer_name, event_id)
);

CREATE INDEX IF NOT EXISTS event_inbox_received_idx
    ON event_inbox (received_at);
