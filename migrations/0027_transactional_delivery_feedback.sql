ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS transactional_enabled boolean NOT NULL DEFAULT false;

ALTER TABLE transactional_messages
    ADD COLUMN IF NOT EXISTS attachments jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE transactional_message_recipients
    ADD COLUMN IF NOT EXISTS accepted_at timestamptz;

CREATE TABLE IF NOT EXISTS transactional_feedback_ingest (
    event_hash       text PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    message_id       uuid NOT NULL REFERENCES transactional_messages(id) ON DELETE CASCADE,
    recipient_id     uuid NOT NULL REFERENCES transactional_message_recipients(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    enhanced_code    text NOT NULL DEFAULT '',
    reason           text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS transactional_audit_events (
    id              bigserial PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    api_key_id      uuid REFERENCES transactional_api_keys(id) ON DELETE SET NULL,
    event_type      text NOT NULL,
    metadata        jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactional_audit_events_org_time_idx
    ON transactional_audit_events (organization_id, occurred_at DESC);

CREATE TABLE IF NOT EXISTS transactional_verp_tokens (
    token           text PRIMARY KEY CHECK (token ~ '^v1[a-f0-9]{32}$'),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    message_id      uuid NOT NULL REFERENCES transactional_messages(id) ON DELETE CASCADE,
    recipient_id    uuid NOT NULL REFERENCES transactional_message_recipients(id) ON DELETE CASCADE,
    email           text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS transactional_verp_tokens_message_idx
    ON transactional_verp_tokens (message_id);
