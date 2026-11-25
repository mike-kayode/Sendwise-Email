-- Extend delivery journey timeline for full per-message event history, delayed/stuck
-- visibility, egress health, and campaign-stage summaries.

DO $$
DECLARE
    c record;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'delivery_journey_events'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%event_type%'
    LOOP
        EXECUTE format('ALTER TABLE delivery_journey_events DROP CONSTRAINT %I', c.conname);
    END LOOP;
END
$$;

ALTER TABLE delivery_journey_events
    ADD CONSTRAINT delivery_journey_events_event_type_check CHECK (event_type IN (
        'queued', 'picked_up', 'processing', 'deferred_by_sendwise',
        'submitted_to_mail_server', 'smtp_attempt', 'relaying', 'deferred_by_recipient',
        'accepted_by_recipient_server', 'bounced', 'expired', 'failed',
        'complained', 'unsubscribed', 'accepted_final_state_unknown',
        'opened', 'clicked'
    )) NOT VALID;
ALTER TABLE delivery_journey_events VALIDATE CONSTRAINT delivery_journey_events_event_type_check;

DO $$
DECLARE
    c record;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'delivery_journey_events'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%source%'
    LOOP
        EXECUTE format('ALTER TABLE delivery_journey_events DROP CONSTRAINT %I', c.conname);
    END LOOP;
END
$$;

ALTER TABLE delivery_journey_events
    ADD CONSTRAINT delivery_journey_events_source_check CHECK (source IN (
        'sendwise', 'stalwart', 'stalwart_webhook', 'dsn', 'reconciliation', 'backfill', 'tracking'
    )) NOT VALID;
ALTER TABLE delivery_journey_events VALIDATE CONSTRAINT delivery_journey_events_source_check;

CREATE TABLE IF NOT EXISTS delivery_egress_health (
    id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    status text NOT NULL CHECK (status IN ('ok', 'blocked', 'unknown')),
    checked_at timestamptz NOT NULL DEFAULT now(),
    last_ok_at timestamptz,
    last_blocked_at timestamptz,
    detail text NOT NULL DEFAULT '',
    checks jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO delivery_egress_health (id, status, detail)
VALUES (1, 'unknown', 'No egress check has run yet.')
ON CONFLICT (id) DO NOTHING;
