ALTER TABLE campaign_recipient_tracking
    ADD COLUMN IF NOT EXISTS journey_status text NOT NULL DEFAULT 'queued',
    ADD COLUMN IF NOT EXISTS journey_last_event_at timestamptz,
    ADD COLUMN IF NOT EXISTS journey_last_stalwart_event_at timestamptz,
    ADD COLUMN IF NOT EXISTS journey_last_reconciled_at timestamptz,
    ADD COLUMN IF NOT EXISTS journey_next_retry_at timestamptz,
    ADD COLUMN IF NOT EXISTS journey_remote_host text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS journey_smtp_code integer,
    ADD COLUMN IF NOT EXISTS journey_enhanced_code text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS journey_reason text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS journey_attempts integer NOT NULL DEFAULT 0;

UPDATE campaign_recipient_tracking crt
SET journey_status = CASE dj.status
        WHEN 'processing' THEN 'processing'
        WHEN 'deferred' THEN 'deferred_by_sendwise'
        WHEN 'submitted' THEN 'submitted_to_mail_server'
        WHEN 'accepted' THEN 'submitted_to_mail_server'
        WHEN 'bounced' THEN 'bounced'
        WHEN 'complained' THEN 'complained'
        WHEN 'unsubscribed' THEN 'unsubscribed'
        WHEN 'failed' THEN 'failed'
        WHEN 'cancelled' THEN CASE
            WHEN dj.last_error = 'unsubscribed' THEN 'unsubscribed'
            ELSE 'failed'
        END
        ELSE 'queued'
    END,
    journey_last_event_at = CASE
        WHEN dj.status = 'queued' THEN dj.created_at
        ELSE dj.updated_at
    END,
    journey_next_retry_at = CASE
        WHEN dj.status = 'deferred' THEN dj.next_attempt_at
        ELSE NULL
    END,
    journey_reason = COALESCE(dj.last_error, '')
FROM delivery_jobs dj
WHERE dj.id = crt.delivery_job_id;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'campaign_recipient_tracking_journey_status_check'
          AND conrelid = 'campaign_recipient_tracking'::regclass
    ) THEN
        ALTER TABLE campaign_recipient_tracking
            ADD CONSTRAINT campaign_recipient_tracking_journey_status_check
            CHECK (journey_status IN (
                'queued', 'processing', 'deferred_by_sendwise',
                'submitted_to_mail_server', 'relaying', 'deferred_by_recipient',
                'accepted_by_recipient_server', 'bounced', 'expired', 'failed',
                'complained', 'unsubscribed', 'accepted_final_state_unknown'
            )) NOT VALID;
    END IF;
END
$$;

ALTER TABLE campaign_recipient_tracking
    VALIDATE CONSTRAINT campaign_recipient_tracking_journey_status_check;

CREATE INDEX IF NOT EXISTS campaign_recipient_tracking_journey_stuck_idx
    ON campaign_recipient_tracking (journey_last_stalwart_event_at, journey_last_event_at)
    WHERE journey_status IN ('submitted_to_mail_server', 'relaying');

CREATE TABLE IF NOT EXISTS delivery_journey_events (
    id bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    recipient_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    delivery_job_id uuid NOT NULL REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN (
        'queued', 'processing', 'deferred_by_sendwise',
        'submitted_to_mail_server', 'relaying', 'deferred_by_recipient',
        'accepted_by_recipient_server', 'bounced', 'expired', 'failed',
        'complained', 'unsubscribed', 'accepted_final_state_unknown',
        'opened', 'clicked'
    )),
    source text NOT NULL CHECK (source IN (
        'sendwise', 'stalwart', 'dsn', 'reconciliation', 'backfill', 'tracking'
    )),
    source_event_id text NOT NULL DEFAULT '',
    event_timestamp timestamptz NOT NULL,
    remote_mx_host text NOT NULL DEFAULT '',
    smtp_code integer,
    enhanced_code text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    attempt_number integer,
    next_retry_at timestamptz,
    event_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    event_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, event_hash)
);

CREATE INDEX IF NOT EXISTS delivery_journey_events_recipient_time_idx
    ON delivery_journey_events (organization_id, campaign_id, recipient_id, event_timestamp, id);
CREATE INDEX IF NOT EXISTS delivery_journey_events_job_time_idx
    ON delivery_journey_events (organization_id, delivery_job_id, event_timestamp DESC);

CREATE TABLE IF NOT EXISTS delivery_journey_stalwart_spans (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    span_id text NOT NULL,
    delivery_job_id uuid NOT NULL REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    recipient_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (organization_id, span_id, delivery_job_id, recipient_id)
);

CREATE INDEX IF NOT EXISTS delivery_journey_stalwart_spans_expiry_idx
    ON delivery_journey_stalwart_spans (expires_at);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'message_events_journey_event_type_check'
          AND conrelid = 'message_events'::regclass
    ) THEN
        ALTER TABLE message_events
            DROP CONSTRAINT IF EXISTS message_events_event_type_check;
        ALTER TABLE message_events
            ADD CONSTRAINT message_events_journey_event_type_check
            CHECK (event_type IN (
                'submitted', 'accepted', 'delivered', 'deferred', 'bounced',
                'complained', 'opened', 'clicked', 'unsubscribed', 'failed',
                'processing', 'deferred_by_sendwise', 'submitted_to_mail_server',
                'relaying', 'deferred_by_recipient', 'accepted_by_recipient_server',
                'expired', 'accepted_final_state_unknown'
            )) NOT VALID;
    END IF;
END
$$;

ALTER TABLE message_events VALIDATE CONSTRAINT message_events_journey_event_type_check;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'email_tracking_events_journey_event_type_check'
          AND conrelid = 'email_tracking_events'::regclass
    ) THEN
        ALTER TABLE email_tracking_events
            DROP CONSTRAINT IF EXISTS email_tracking_events_event_type_check;
        ALTER TABLE email_tracking_events
            ADD CONSTRAINT email_tracking_events_journey_event_type_check
            CHECK (event_type IN (
                'queued', 'accepted', 'delivered', 'deferred', 'bounced',
                'complained', 'unsubscribed', 'failed', 'opened', 'clicked',
                'processing', 'deferred_by_sendwise', 'submitted_to_mail_server',
                'relaying', 'deferred_by_recipient', 'accepted_by_recipient_server',
                'expired', 'accepted_final_state_unknown'
            )) NOT VALID;
    END IF;
END
$$;

ALTER TABLE email_tracking_events
    VALIDATE CONSTRAINT email_tracking_events_journey_event_type_check;

INSERT INTO delivery_journey_events
    (organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
     event_type, source, event_timestamp, reason, event_metadata, event_hash)
SELECT crt.organization_id, crt.campaign_id, crt.recipient_id, crt.contact_id,
       crt.delivery_job_id, 'queued', 'backfill',
       COALESCE(crt.queued_at, dj.created_at), '', '{"reconstructed":true}'::jsonb,
       md5(crt.organization_id::text || ':' || crt.delivery_job_id::text || ':journey:queued')
FROM campaign_recipient_tracking crt
JOIN delivery_jobs dj ON dj.id = crt.delivery_job_id
ON CONFLICT (organization_id, event_hash) DO NOTHING;

INSERT INTO delivery_journey_events
    (organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
     event_type, source, event_timestamp, reason, next_retry_at,
     event_metadata, event_hash)
SELECT crt.organization_id, crt.campaign_id, crt.recipient_id, crt.contact_id,
       crt.delivery_job_id, crt.journey_status, 'backfill',
       COALESCE(crt.journey_last_event_at, dj.updated_at),
       crt.journey_reason, crt.journey_next_retry_at,
       '{"reconstructed":true}'::jsonb,
       md5(crt.organization_id::text || ':' || crt.delivery_job_id::text ||
           ':journey:current:' || crt.journey_status)
FROM campaign_recipient_tracking crt
JOIN delivery_jobs dj ON dj.id = crt.delivery_job_id
WHERE crt.journey_status <> 'queued'
ON CONFLICT (organization_id, event_hash) DO NOTHING;
