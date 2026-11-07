ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS sender_postal_address text NOT NULL DEFAULT '';

ALTER TABLE delivery_jobs
    ADD COLUMN IF NOT EXISTS warmup_reserved_on date;

CREATE TABLE IF NOT EXISTS sending_domain_daily_stats (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain text NOT NULL,
    sending_day date NOT NULL,
    reserved_count integer NOT NULL DEFAULT 0 CHECK (reserved_count >= 0),
    successful_count integer NOT NULL DEFAULT 0 CHECK (successful_count >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, domain, sending_day)
);

CREATE TABLE IF NOT EXISTS campaign_alerts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    alert_type text NOT NULL,
    message text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at timestamptz
);

CREATE INDEX IF NOT EXISTS campaign_alerts_campaign_idx
    ON campaign_alerts (organization_id, campaign_id, created_at DESC);
