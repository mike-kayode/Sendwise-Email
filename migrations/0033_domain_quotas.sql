CREATE TABLE IF NOT EXISTS platform_quota_defaults (
    stream text PRIMARY KEY CHECK (stream IN ('marketing', 'transactional')),
    daily_limit bigint CHECK (daily_limit IS NULL OR daily_limit > 0),
    monthly_limit bigint CHECK (monthly_limit IS NULL OR monthly_limit > 0),
    per_second_limit bigint NOT NULL CHECK (per_second_limit > 0),
    seeded_from_env boolean NOT NULL DEFAULT false,
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO platform_quota_defaults
    (stream, daily_limit, monthly_limit, per_second_limit, seeded_from_env)
VALUES
    ('marketing', 10000, NULL, 20, false),
    ('transactional', 1000, 10000, 5, false)
ON CONFLICT (stream) DO NOTHING;

CREATE TABLE IF NOT EXISTS domain_quotas (
    domain_id uuid NOT NULL REFERENCES sending_domains(id) ON DELETE CASCADE,
    stream text NOT NULL CHECK (stream IN ('marketing', 'transactional')),
    daily_limit bigint CHECK (daily_limit IS NULL OR daily_limit > 0),
    monthly_limit bigint CHECK (monthly_limit IS NULL OR monthly_limit > 0),
    per_second_limit bigint CHECK (per_second_limit IS NULL OR per_second_limit > 0),
    unlimited boolean NOT NULL DEFAULT false,
    notes text NOT NULL DEFAULT '',
    updated_by uuid,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (domain_id, stream)
);

CREATE TABLE IF NOT EXISTS domain_usage (
    domain_id uuid NOT NULL REFERENCES sending_domains(id) ON DELETE CASCADE,
    stream text NOT NULL CHECK (stream IN ('marketing', 'transactional')),
    period text NOT NULL CHECK (period IN ('day', 'month')),
    period_start date NOT NULL,
    sent_count bigint NOT NULL DEFAULT 0 CHECK (sent_count >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (domain_id, stream, period, period_start)
);

CREATE TABLE IF NOT EXISTS domain_rate_usage (
    domain_id uuid NOT NULL REFERENCES sending_domains(id) ON DELETE CASCADE,
    stream text NOT NULL CHECK (stream IN ('marketing', 'transactional')),
    second_start timestamptz NOT NULL,
    sent_count bigint NOT NULL DEFAULT 0 CHECK (sent_count >= 0),
    PRIMARY KEY (domain_id, stream, second_start)
);

CREATE INDEX IF NOT EXISTS domain_rate_usage_expiry_idx
    ON domain_rate_usage (second_start);

CREATE TABLE IF NOT EXISTS domain_quota_reservations (
    stream text NOT NULL CHECK (stream IN ('marketing', 'transactional')),
    source text NOT NULL CHECK (source IN ('campaign_delivery', 'transactional_recipient')),
    source_id uuid NOT NULL,
    domain_id uuid NOT NULL REFERENCES sending_domains(id) ON DELETE CASCADE,
    daily_period date NOT NULL,
    monthly_period date NOT NULL,
    second_period timestamptz NOT NULL,
    reserved_count bigint NOT NULL CHECK (reserved_count > 0),
    status text NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'retained', 'released')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (stream, source, source_id)
);

CREATE INDEX IF NOT EXISTS domain_quota_reservations_domain_idx
    ON domain_quota_reservations (domain_id, stream, status, created_at DESC);

CREATE TABLE IF NOT EXISTS domain_quota_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id uuid NOT NULL REFERENCES sending_domains(id) ON DELETE CASCADE,
    requested_by uuid NOT NULL,
    stream text NOT NULL CHECK (stream IN ('marketing', 'transactional')),
    requested_daily_limit bigint CHECK (requested_daily_limit IS NULL OR requested_daily_limit > 0),
    requested_monthly_limit bigint CHECK (requested_monthly_limit IS NULL OR requested_monthly_limit > 0),
    requested_per_second_limit bigint CHECK (requested_per_second_limit IS NULL OR requested_per_second_limit > 0),
    reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 1000),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied')),
    reviewed_by uuid,
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS domain_quota_requests_pending_idx
    ON domain_quota_requests (created_at DESC)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS domain_quota_requests_domain_idx
    ON domain_quota_requests (domain_id, created_at DESC);

ALTER TABLE delivery_jobs
    ADD COLUMN IF NOT EXISTS quota_deferred boolean NOT NULL DEFAULT false;
ALTER TABLE delivery_jobs
    ADD COLUMN IF NOT EXISTS quota_reservation_id uuid;
