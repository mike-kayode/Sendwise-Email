-- 0004_delivery_enhancements.sql
-- Delivery enhancements: bounce classification, dead-letter queue, quotas,
-- per-domain concurrency, and maximum message age.

ALTER TABLE delivery_jobs
    ADD COLUMN IF NOT EXISTS bounce_type text
        CHECK (bounce_type IN ('hard', 'soft', 'blocked')),
    ADD COLUMN IF NOT EXISTS smtp_code text,
    ADD COLUMN IF NOT EXISTS smtp_enhanced_code text,
    ADD COLUMN IF NOT EXISTS dead_lettered_at timestamptz;

CREATE INDEX IF NOT EXISTS delivery_jobs_bounce_idx
    ON delivery_jobs (organization_id, bounce_type) WHERE bounce_type IS NOT NULL;
CREATE INDEX IF NOT EXISTS delivery_jobs_dead_lettered_idx
    ON delivery_jobs (dead_lettered_at) WHERE dead_lettered_at IS NOT NULL;

-- Dead-letter queue for exhausted or aged-out delivery jobs.
CREATE TABLE IF NOT EXISTS dead_letter_queue (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id     uuid REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL,
    delivery_job_id uuid REFERENCES delivery_jobs(id) ON DELETE CASCADE,
    reason          text NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    replayed_at     timestamptz
);

CREATE INDEX dead_letter_queue_org_idx
    ON dead_letter_queue (organization_id, created_at DESC);
CREATE INDEX dead_letter_queue_replayed_idx
    ON dead_letter_queue (replayed_at) WHERE replayed_at IS NULL;

-- Organization-level sending settings, quotas, and abuse pause.
CREATE TABLE IF NOT EXISTS organization_settings (
    organization_id           uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    max_daily_emails          integer NOT NULL DEFAULT 10000,
    max_bounce_rate           real NOT NULL DEFAULT 0.05,
    max_complaint_rate        real NOT NULL DEFAULT 0.001,
    paused_until              timestamptz,
    domain_concurrency_limit  integer NOT NULL DEFAULT 10,
    updated_at                timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER organization_settings_set_updated_at
    BEFORE UPDATE ON organization_settings
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();