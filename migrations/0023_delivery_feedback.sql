CREATE TABLE IF NOT EXISTS delivery_verp_tokens (
    token            text PRIMARY KEY CHECK (token ~ '^v1[a-f0-9]{32}$'),
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id      uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    delivery_job_id  uuid NOT NULL REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    contact_id       uuid NOT NULL,
    email            text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS delivery_verp_tokens_job_idx
    ON delivery_verp_tokens (delivery_job_id);

CREATE TABLE IF NOT EXISTS delivery_feedback_ingest (
    event_hash       text PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id      uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    delivery_job_id  uuid NOT NULL REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    contact_id       uuid NOT NULL,
    email            text NOT NULL,
    event_type       text NOT NULL CHECK (event_type IN ('hard_bounce', 'soft_bounce', 'complaint')),
    enhanced_code    text NOT NULL DEFAULT '',
    reason           text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS delivery_feedback_job_type_idx
    ON delivery_feedback_ingest (delivery_job_id, event_type, created_at);
