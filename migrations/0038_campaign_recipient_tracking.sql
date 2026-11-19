CREATE TABLE IF NOT EXISTS campaign_recipient_tracking (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    recipient_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    delivery_job_id uuid NOT NULL REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    tracking_id text NOT NULL,
    email text NOT NULL,
    delivery_status text NOT NULL DEFAULT 'queued',
    queued_at timestamptz,
    accepted_at timestamptz,
    delivered_at timestamptz,
    deferred_at timestamptz,
    bounced_at timestamptz,
    complained_at timestamptz,
    unsubscribed_at timestamptz,
    failed_at timestamptz,
    first_opened_at timestamptz,
    last_opened_at timestamptz,
    open_count bigint NOT NULL DEFAULT 0,
    first_clicked_at timestamptz,
    last_clicked_at timestamptz,
    click_count bigint NOT NULL DEFAULT 0,
    unique_link_count bigint NOT NULL DEFAULT 0,
    engagement_classification text NOT NULL DEFAULT 'unknown'
        CHECK (engagement_classification IN ('human_like', 'suspected_automated', 'privacy_proxy', 'unknown')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, contact_id),
    UNIQUE (tracking_id)
);

CREATE INDEX IF NOT EXISTS campaign_recipient_tracking_campaign_status_idx
    ON campaign_recipient_tracking (campaign_id, delivery_status);
CREATE INDEX IF NOT EXISTS campaign_recipient_tracking_campaign_activity_idx
    ON campaign_recipient_tracking (campaign_id, last_opened_at DESC, last_clicked_at DESC);
CREATE INDEX IF NOT EXISTS campaign_recipient_tracking_email_idx
    ON campaign_recipient_tracking (campaign_id, lower(email));

CREATE TABLE IF NOT EXISTS email_tracking_events (
    id bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    recipient_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    delivery_job_id uuid NOT NULL,
    tracking_id text NOT NULL,
    event_type text NOT NULL
        CHECK (event_type IN (
            'queued', 'accepted', 'delivered', 'deferred', 'bounced',
            'complained', 'unsubscribed', 'failed', 'opened', 'clicked'
        )),
    event_timestamp timestamptz NOT NULL DEFAULT now(),
    ip_address inet,
    user_agent text,
    device_type text,
    browser text,
    operating_system text,
    country text,
    region text,
    city text,
    destination_url text,
    event_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_automated boolean NOT NULL DEFAULT false,
    automation_type text NOT NULL DEFAULT 'unknown',
    event_hash text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS email_tracking_events_recipient_time_idx
    ON email_tracking_events (recipient_id, event_timestamp DESC);
CREATE INDEX IF NOT EXISTS email_tracking_events_campaign_time_idx
    ON email_tracking_events (campaign_id, event_timestamp DESC);
CREATE INDEX IF NOT EXISTS email_tracking_events_tracking_idx
    ON email_tracking_events (tracking_id, event_timestamp DESC);
CREATE UNIQUE INDEX IF NOT EXISTS email_tracking_events_hash_idx
    ON email_tracking_events (event_hash)
    WHERE event_hash IS NOT NULL;

CREATE TABLE IF NOT EXISTS email_tracking_links (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    recipient_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    delivery_job_id uuid NOT NULL REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    tracking_id text NOT NULL,
    link_token text NOT NULL,
    destination_url text NOT NULL,
    click_count bigint NOT NULL DEFAULT 0,
    first_clicked_at timestamptz,
    last_clicked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (link_token),
    UNIQUE (delivery_job_id, destination_url)
);

CREATE INDEX IF NOT EXISTS email_tracking_links_recipient_idx
    ON email_tracking_links (recipient_id);

INSERT INTO campaign_recipient_tracking
    (organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
     tracking_id, email, delivery_status, queued_at, accepted_at, updated_at)
SELECT r.organization_id, r.campaign_id, r.id, r.contact_id, dj.id,
       'trk_' || encode(gen_random_bytes(24), 'hex'), r.email,
       dj.status,
       dj.created_at,
       dj.accepted_at,
       now()
FROM campaign_recipients r
JOIN delivery_jobs dj
  ON dj.campaign_id = r.campaign_id AND dj.contact_id = r.contact_id
ON CONFLICT (campaign_id, contact_id) DO NOTHING;
