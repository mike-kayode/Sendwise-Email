CREATE TABLE IF NOT EXISTS transactional_settings (
    organization_id       uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    enabled               boolean NOT NULL DEFAULT false,
    status                text NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active', 'paused', 'disabled')),
    pause_reason          text NOT NULL DEFAULT '',
    requests_per_second   integer NOT NULL DEFAULT 1 CHECK (requests_per_second > 0),
    daily_limit           integer NOT NULL DEFAULT 100 CHECK (daily_limit > 0),
    monthly_limit         integer NOT NULL DEFAULT 1000 CHECK (monthly_limit > 0),
    max_recipients        integer NOT NULL DEFAULT 5 CHECK (max_recipients > 0),
    max_message_bytes     bigint NOT NULL DEFAULT 10485760 CHECK (max_message_bytes > 0),
    max_bounce_rate       real NOT NULL DEFAULT 0.05 CHECK (max_bounce_rate >= 0 AND max_bounce_rate <= 1),
    max_complaint_rate    real NOT NULL DEFAULT 0.003 CHECK (max_complaint_rate >= 0 AND max_complaint_rate <= 1),
    minimum_sample_size   integer NOT NULL DEFAULT 100 CHECK (minimum_sample_size > 0),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

INSERT INTO transactional_settings (organization_id)
SELECT id FROM organizations
ON CONFLICT (organization_id) DO NOTHING;

CREATE OR REPLACE FUNCTION create_transactional_settings_for_organization()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO transactional_settings (organization_id)
    VALUES (NEW.id)
    ON CONFLICT (organization_id) DO NOTHING;
    RETURN NEW;
END
$$;

DROP TRIGGER IF EXISTS organizations_create_transactional_settings ON organizations;
CREATE TRIGGER organizations_create_transactional_settings
    AFTER INSERT ON organizations
    FOR EACH ROW EXECUTE FUNCTION create_transactional_settings_for_organization();

CREATE TABLE IF NOT EXISTS transactional_api_keys (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    key_prefix      text NOT NULL,
    key_hash        text NOT NULL,
    scopes          text[] NOT NULL DEFAULT ARRAY['send', 'read']::text[],
    ip_allowlist    cidr[] NOT NULL DEFAULT '{}',
    test_mode       boolean NOT NULL DEFAULT false,
    rotated_from    uuid REFERENCES transactional_api_keys(id) ON DELETE SET NULL,
    grace_until     timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS transactional_api_keys_prefix_uidx
    ON transactional_api_keys (key_prefix);
CREATE INDEX IF NOT EXISTS transactional_api_keys_org_idx
    ON transactional_api_keys (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS transactional_smtp_credentials (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    secret_hash     text NOT NULL,
    requests_per_second integer NOT NULL DEFAULT 1 CHECK (requests_per_second > 0),
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactional_smtp_credentials_org_idx
    ON transactional_smtp_credentials (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS transactional_templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    current_version integer NOT NULL DEFAULT 1 CHECK (current_version > 0),
    track_opens     boolean NOT NULL DEFAULT false,
    track_clicks    boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id)
);

CREATE INDEX IF NOT EXISTS transactional_templates_org_idx
    ON transactional_templates (organization_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS transactional_template_versions (
    template_id     uuid NOT NULL REFERENCES transactional_templates(id) ON DELETE CASCADE,
    version         integer NOT NULL CHECK (version > 0),
    subject         text NOT NULL,
    html            text NOT NULL DEFAULT '',
    text_content    text NOT NULL,
    variables       text[] NOT NULL DEFAULT '{}',
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, version)
);

CREATE TABLE IF NOT EXISTS transactional_messages (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    api_key_id          uuid REFERENCES transactional_api_keys(id) ON DELETE SET NULL,
    template_id         uuid,
    template_version    integer,
    idempotency_key     text,
    message_id          text NOT NULL UNIQUE,
    status              text NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'sending', 'delivered', 'deferred', 'bounced', 'failed', 'complained', 'suppressed', 'rejected')),
    from_email          text NOT NULL,
    reply_to            text NOT NULL DEFAULT '',
    subject             text NOT NULL,
    html                text NOT NULL DEFAULT '',
    text_content        text NOT NULL,
    headers             jsonb NOT NULL DEFAULT '{}'::jsonb,
    tags                text[] NOT NULL DEFAULT '{}',
    metadata            jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempts            integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    lease_expires_at    timestamptz,
    last_smtp_code      integer,
    last_enhanced_code  text NOT NULL DEFAULT '',
    last_smtp_response  text NOT NULL DEFAULT '',
    accepted_at         timestamptz,
    delivered_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (template_id, template_version)
        REFERENCES transactional_template_versions(template_id, version)
        ON DELETE SET NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS transactional_messages_idempotency_uidx
    ON transactional_messages (organization_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS transactional_messages_due_idx
    ON transactional_messages (next_attempt_at, created_at)
    WHERE status IN ('queued', 'deferred');
CREATE INDEX IF NOT EXISTS transactional_messages_org_created_idx
    ON transactional_messages (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS transactional_messages_org_status_idx
    ON transactional_messages (organization_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS transactional_message_recipients (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id          uuid NOT NULL REFERENCES transactional_messages(id) ON DELETE CASCADE,
    email               text NOT NULL,
    recipient_type      text NOT NULL CHECK (recipient_type IN ('to', 'cc', 'bcc')),
    status              text NOT NULL DEFAULT 'queued'
                        CHECK (status IN ('queued', 'sending', 'delivered', 'deferred', 'bounced', 'failed', 'complained', 'suppressed', 'rejected')),
    smtp_code           integer,
    enhanced_code       text NOT NULL DEFAULT '',
    smtp_response       text NOT NULL DEFAULT '',
    attempts            integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    lease_expires_at    timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS transactional_message_recipients_unique_email_idx
    ON transactional_message_recipients (message_id, recipient_type, lower(email));
CREATE INDEX IF NOT EXISTS transactional_message_recipients_due_idx
    ON transactional_message_recipients (next_attempt_at, created_at)
    WHERE status IN ('queued', 'deferred');
CREATE INDEX IF NOT EXISTS transactional_message_recipients_email_idx
    ON transactional_message_recipients (lower(email), status);

CREATE TABLE IF NOT EXISTS transactional_message_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    message_id      uuid NOT NULL REFERENCES transactional_messages(id) ON DELETE CASCADE,
    recipient_id    uuid REFERENCES transactional_message_recipients(id) ON DELETE SET NULL,
    event_type      text NOT NULL,
    source          text NOT NULL CHECK (source IN ('api', 'smtp', 'stalwart', 'retry', 'system')),
    smtp_code       integer,
    enhanced_code   text NOT NULL DEFAULT '',
    smtp_response   text NOT NULL DEFAULT '',
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactional_message_events_timeline_idx
    ON transactional_message_events (message_id, occurred_at, id);
CREATE INDEX IF NOT EXISTS transactional_message_events_org_time_idx
    ON transactional_message_events (organization_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS transactional_suppressions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    email           text NOT NULL,
    reason          text NOT NULL CHECK (reason IN ('hard_bounce', 'complaint', 'manual', 'invalid')),
    source          text NOT NULL DEFAULT 'manual',
    confirmed       boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS transactional_suppressions_workspace_email_uidx
    ON transactional_suppressions (organization_id, lower(email))
    WHERE organization_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS transactional_suppressions_global_email_uidx
    ON transactional_suppressions (lower(email))
    WHERE organization_id IS NULL;
CREATE INDEX IF NOT EXISTS transactional_suppressions_email_idx
    ON transactional_suppressions (lower(email));

CREATE TABLE IF NOT EXISTS transactional_webhooks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    url             text NOT NULL,
    encrypted_secret bytea NOT NULL,
    events          text[] NOT NULL DEFAULT '{}',
    enabled         boolean NOT NULL DEFAULT true,
    consecutive_failures integer NOT NULL DEFAULT 0,
    disabled_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactional_webhooks_org_idx
    ON transactional_webhooks (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS transactional_webhook_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id      uuid NOT NULL REFERENCES transactional_webhooks(id) ON DELETE CASCADE,
    message_event_id bigint REFERENCES transactional_message_events(id) ON DELETE SET NULL,
    event_type      text NOT NULL,
    payload         jsonb NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    response_code  integer,
    response_body  text NOT NULL DEFAULT '',
    delivered_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactional_webhook_deliveries_due_idx
    ON transactional_webhook_deliveries (next_attempt_at, created_at)
    WHERE delivered_at IS NULL;
CREATE INDEX IF NOT EXISTS transactional_webhook_deliveries_webhook_idx
    ON transactional_webhook_deliveries (webhook_id, created_at DESC);

CREATE TABLE IF NOT EXISTS transactional_usage_daily (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    usage_date      date NOT NULL,
    accepted_count  bigint NOT NULL DEFAULT 0 CHECK (accepted_count >= 0),
    delivered_count bigint NOT NULL DEFAULT 0 CHECK (delivered_count >= 0),
    bounced_count   bigint NOT NULL DEFAULT 0 CHECK (bounced_count >= 0),
    complained_count bigint NOT NULL DEFAULT 0 CHECK (complained_count >= 0),
    failed_count    bigint NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, usage_date)
);
