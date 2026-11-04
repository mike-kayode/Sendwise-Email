-- Initial schema for the email marketing platform.
-- Tenant isolation: every tenant-owned table carries organization_id. The API
-- always scopes queries by the organization in the access token. Add PostgreSQL
-- RLS policies when the application connects with a non-owner role.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    slug       text NOT NULL UNIQUE,
    plan       text NOT NULL DEFAULT 'free',
    status     text NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'suspended', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email             text NOT NULL,
    password_hash     text NOT NULL,
    name              text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'pending', 'suspended', 'closed')),
    email_verified_at timestamptz,
    last_login_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

CREATE TABLE organization_members (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            text NOT NULL DEFAULT 'viewer'
                    CHECK (role IN ('owner', 'admin', 'manager', 'author', 'analyst', 'viewer')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

CREATE INDEX organization_members_user_idx ON organization_members (user_id);

CREATE TABLE api_keys (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    name            text NOT NULL,
    key_prefix      text NOT NULL,
    key_hash        text NOT NULL,
    scopes          text[] NOT NULL DEFAULT '{}',
    last_used_at    timestamptz,
    expires_at      timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX api_keys_org_idx ON api_keys (organization_id);
CREATE UNIQUE INDEX api_keys_prefix_idx ON api_keys (key_prefix);

CREATE TABLE contacts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           text NOT NULL,
    first_name      text,
    last_name       text,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'subscribed', 'unsubscribed', 'bounced', 'complained', 'suppressed')),
    custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
    source          text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX contacts_org_email_lower_idx
    ON contacts (organization_id, lower(email));
CREATE INDEX contacts_org_status_idx ON contacts (organization_id, status);
CREATE INDEX contacts_org_created_idx ON contacts (organization_id, created_at DESC);
CREATE INDEX contacts_custom_fields_idx ON contacts USING gin (custom_fields);

CREATE TABLE custom_fields (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key             text NOT NULL,
    label           text NOT NULL,
    field_type      text NOT NULL DEFAULT 'text'
                    CHECK (field_type IN ('text', 'number', 'date', 'boolean')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, key)
);

CREATE TABLE contact_lists (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE contact_list_members (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    list_id         uuid NOT NULL REFERENCES contact_lists(id) ON DELETE CASCADE,
    source          text,
    subscribed_at   timestamptz NOT NULL DEFAULT now(),
    unsubscribed_at timestamptz,
    PRIMARY KEY (contact_id, list_id)
);

CREATE INDEX contact_list_members_list_idx ON contact_list_members (list_id);

CREATE TABLE tags (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    color           text NOT NULL DEFAULT '#64748b',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE contact_tags (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    tag_id          uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (contact_id, tag_id)
);

CREATE INDEX contact_tags_tag_idx ON contact_tags (tag_id);

CREATE TABLE segments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    rules           jsonb NOT NULL DEFAULT '{"match":"all","conditions":[]}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX segments_org_idx ON segments (organization_id);

CREATE TABLE consent_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_id      uuid,
    event_type      text NOT NULL
                    CHECK (event_type IN ('subscribe', 'unsubscribe', 'import', 'form', 'complaint', 'resubscribe')),
    source          text,
    ip_address      inet,
    user_agent      text,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX consent_events_contact_idx ON consent_events (contact_id, created_at DESC);

CREATE TABLE suppressions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           text NOT NULL,
    reason          text NOT NULL
                    CHECK (reason IN ('unsubscribe', 'hard_bounce', 'complaint', 'manual', 'abuse')),
    source          text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX suppressions_org_email_lower_idx
    ON suppressions (organization_id, lower(email));

CREATE TABLE templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    name            text NOT NULL,
    category        text NOT NULL DEFAULT 'general',
    design          jsonb NOT NULL DEFAULT '{"version":1,"blocks":[]}'::jsonb,
    html            text,
    text_content    text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX templates_org_idx ON templates (organization_id);

CREATE TABLE sending_domains (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain              text NOT NULL,
    status              text NOT NULL DEFAULT 'not_configured'
                        CHECK (status IN ('not_configured', 'pending', 'verified', 'misconfigured')),
    dkim_selector       text NOT NULL DEFAULT 'mail',
    dkim_public_key     text,
    spf_status          text NOT NULL DEFAULT 'unknown',
    dkim_status         text NOT NULL DEFAULT 'unknown',
    dmarc_status        text NOT NULL DEFAULT 'unknown',
    last_checked_at     timestamptz,
    verified_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sending_domains_org_domain_lower_idx
    ON sending_domains (organization_id, lower(domain));

CREATE TABLE campaigns (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by        uuid REFERENCES users(id) ON DELETE SET NULL,
    name              text NOT NULL,
    subject           text NOT NULL DEFAULT '',
    preview_text      text NOT NULL DEFAULT '',
    sender_name       text NOT NULL DEFAULT '',
    sender_email      text NOT NULL DEFAULT '',
    reply_to          text,
    list_id           uuid REFERENCES contact_lists(id) ON DELETE SET NULL,
    segment_id        uuid REFERENCES segments(id) ON DELETE SET NULL,
    template_id       uuid REFERENCES templates(id) ON DELETE SET NULL,
    content           jsonb NOT NULL DEFAULT '{"version":1,"blocks":[]}'::jsonb,
    status            text NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft', 'ready', 'scheduled', 'processing', 'sending', 'completed', 'paused', 'cancelled', 'failed')),
    recipient_count   integer NOT NULL DEFAULT 0,
    track_opens       boolean NOT NULL DEFAULT true,
    track_clicks      boolean NOT NULL DEFAULT true,
    scheduled_at      timestamptz,
    started_at        timestamptz,
    completed_at      timestamptz,
    paused_at         timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX campaigns_org_status_idx ON campaigns (organization_id, status);
CREATE INDEX campaigns_due_idx ON campaigns (status, scheduled_at)
    WHERE status = 'scheduled';

-- Immutable recipient snapshot. Deliberately no FK to contacts: deleting a
-- contact must not alter an in-flight or historical campaign audience.
CREATE TABLE campaign_recipients (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id     uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL,
    email           text NOT NULL,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'submitted', 'bounced', 'complained', 'unsubscribed', 'failed', 'skipped')),
    message_id      text,
    snapshot_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, contact_id)
);

CREATE INDEX campaign_recipients_campaign_idx ON campaign_recipients (campaign_id, status);
CREATE INDEX campaign_recipients_email_idx ON campaign_recipients (campaign_id, lower(email));

CREATE TABLE delivery_jobs (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id          uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id           uuid NOT NULL,
    status               text NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'processing', 'submitted', 'accepted', 'deferred', 'bounced', 'complained', 'unsubscribed', 'failed', 'cancelled')),
    message_id           text NOT NULL UNIQUE,
    attempts             integer NOT NULL DEFAULT 0,
    max_attempts         integer NOT NULL DEFAULT 5,
    next_attempt_at      timestamptz NOT NULL DEFAULT now(),
    last_error           text,
    transport_message_id text,
    accepted_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, contact_id)
);

CREATE INDEX delivery_jobs_due_idx ON delivery_jobs (status, next_attempt_at);
CREATE INDEX delivery_jobs_campaign_idx ON delivery_jobs (campaign_id, status);
CREATE INDEX delivery_jobs_org_idx ON delivery_jobs (organization_id, created_at DESC);

CREATE TABLE message_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id     uuid,
    contact_id      uuid,
    delivery_job_id uuid,
    event_type      text NOT NULL
                    CHECK (event_type IN ('submitted', 'accepted', 'delivered', 'deferred', 'bounced', 'complained', 'opened', 'clicked', 'unsubscribed', 'failed')),
    url             text,
    user_agent      text,
    ip_address      inet,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX message_events_campaign_idx ON message_events (campaign_id, event_type);
CREATE INDEX message_events_org_time_idx ON message_events (organization_id, occurred_at DESC);
CREATE INDEX message_events_job_idx ON message_events (delivery_job_id);

CREATE TABLE automations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'active', 'paused', 'archived')),
    trigger         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX automations_org_idx ON automations (organization_id, status);

CREATE TABLE automation_steps (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    automation_id   uuid NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    position        integer NOT NULL,
    step_type       text NOT NULL
                    CHECK (step_type IN ('send_email', 'wait', 'condition', 'add_tag', 'remove_tag', 'webhook')),
    config          jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (automation_id, position)
);

CREATE TABLE automation_executions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    automation_id   uuid NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL,
    status          text NOT NULL DEFAULT 'running'
                    CHECK (status IN ('running', 'waiting', 'completed', 'failed', 'cancelled')),
    current_step    integer NOT NULL DEFAULT 0,
    next_run_at     timestamptz,
    last_error      text,
    started_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX automation_executions_due_idx
    ON automation_executions (status, next_run_at)
    WHERE status IN ('running', 'waiting');
CREATE INDEX automation_executions_contact_idx
    ON automation_executions (automation_id, contact_id);

CREATE TABLE webhooks (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    url             text NOT NULL,
    secret_hash     text NOT NULL,
    events          text[] NOT NULL DEFAULT '{}',
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'paused', 'disabled')),
    last_triggered_at timestamptz,
    failure_count   integer NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX webhooks_org_idx ON webhooks (organization_id);

CREATE TABLE usage_records (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    metric          text NOT NULL,
    quantity        bigint NOT NULL DEFAULT 0,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX usage_records_org_metric_idx
    ON usage_records (organization_id, metric, occurred_at DESC);

CREATE TABLE audit_logs (
    id              bigserial PRIMARY KEY,
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    action          text NOT NULL,
    target_type     text,
    target_id       text,
    ip_address      inet,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE contact_imports (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    file_name       text NOT NULL,
    source_data     bytea NOT NULL,
    mapping         jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'processing', 'completed', 'failed', 'cancelled')),
    total_rows      integer NOT NULL DEFAULT 0,
    processed_rows  integer NOT NULL DEFAULT 0,
    imported_count  integer NOT NULL DEFAULT 0,
    skipped_count   integer NOT NULL DEFAULT 0,
    invalid_count   integer NOT NULL DEFAULT 0,
    errors          jsonb NOT NULL DEFAULT '[]'::jsonb,
    last_error      text,
    started_at      timestamptz,
    completed_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX contact_imports_org_idx ON contact_imports (organization_id, created_at DESC);
CREATE INDEX contact_imports_status_idx ON contact_imports (status);

CREATE INDEX audit_logs_org_time_idx ON audit_logs (organization_id, created_at DESC);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'organizations', 'users', 'contacts', 'contact_lists', 'segments',
        'templates', 'sending_domains', 'campaigns', 'delivery_jobs',
        'automations', 'automation_executions', 'contact_imports'
    ]
    LOOP
        EXECUTE format(
            'CREATE TRIGGER %I_set_updated_at BEFORE UPDATE ON %I
             FOR EACH ROW EXECUTE FUNCTION set_updated_at()',
            table_name, table_name
        );
    END LOOP;
END;
$$;
