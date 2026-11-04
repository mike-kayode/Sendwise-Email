-- 0001_init.sql
-- Workspace service owned schema. The Workspace service is the sole writer of
-- organizations, memberships, and invitations. Identity and legacy services
-- consume workspace.* events; they never write these tables.

CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    slug       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX organizations_created_idx ON organizations (created_at DESC);

CREATE TABLE memberships (
    organization_id uuid NOT NULL,
    user_id         uuid NOT NULL,
    role            text NOT NULL DEFAULT 'viewer'
                    CHECK (role IN ('owner', 'admin', 'manager', 'author', 'analyst', 'viewer')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

CREATE INDEX memberships_user_idx ON memberships (user_id);

-- Invitations are single-use, high-entropy tokens stored only as hashes.
CREATE TABLE invitations (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL,
    email           text NOT NULL,
    role            text NOT NULL DEFAULT 'viewer'
                    CHECK (role IN ('owner', 'admin', 'manager', 'author', 'analyst', 'viewer')),
    token_hash      text NOT NULL UNIQUE,
    invited_by      uuid,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    expires_at      timestamptz NOT NULL,
    accepted_at     timestamptz,
    accepted_by     uuid,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX invitations_org_idx ON invitations (organization_id, created_at DESC);
CREATE INDEX invitations_email_idx ON invitations (lower(email)) WHERE status = 'pending';

-- Transactional outbox: business events are written in the same transaction
-- as the state change and relayed to NATS JetStream by this service.
CREATE TABLE event_outbox (
    event_id         uuid PRIMARY KEY,
    subject          text NOT NULL CHECK (length(trim(subject)) > 0),
    payload          jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at       timestamptz NOT NULL DEFAULT now(),
    published_at     timestamptz,
    attempts         integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    lease_token      uuid,
    lease_expires_at timestamptz,
    last_error       text,
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX event_outbox_pending_idx
    ON event_outbox (next_attempt_at, created_at)
    WHERE published_at IS NULL;

-- Inbox: idempotency ledger for events consumed by this service.
CREATE TABLE event_inbox (
    consumer_name text NOT NULL CHECK (length(trim(consumer_name)) > 0),
    event_id      uuid NOT NULL,
    received_at   timestamptz NOT NULL DEFAULT now(),
    processed_at  timestamptz,
    PRIMARY KEY (consumer_name, event_id)
);

CREATE INDEX event_inbox_received_idx ON event_inbox (received_at);

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER organizations_set_updated_at
    BEFORE UPDATE ON organizations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
