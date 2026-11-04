-- 0001_init.sql
-- Identity service owned schema. The Identity service is the sole writer of
-- credential hashes, sessions, refresh tokens, verification tokens, and auth
-- audit records. Other services consume identity.* events; they never read
-- these tables.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email             text NOT NULL,
    password_hash     text NOT NULL,
    name              text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'active', 'suspended', 'closed')),
    email_verified_at timestamptz,
    last_login_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));
CREATE INDEX users_status_idx ON users (status);

-- Sessions double as refresh-token records: rotation replaces the stored hash
-- in place, which makes presented-token reuse detectable.
CREATE TABLE sessions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_info         text NOT NULL DEFAULT '',
    ip_address          inet,
    user_agent          text,
    refresh_token_hash  text NOT NULL,
    previous_refresh_token_hash text,
    organization_id     uuid,
    organization_name   text NOT NULL DEFAULT '',
    organization_slug   text NOT NULL DEFAULT '',
    role                text NOT NULL DEFAULT '',
    expires_at          timestamptz NOT NULL,
    revoked_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX sessions_refresh_hash_idx ON sessions (refresh_token_hash);
-- Superseded hashes are retained so replaying an old token is detectable.
CREATE INDEX sessions_previous_hash_idx ON sessions (previous_refresh_token_hash)
    WHERE previous_refresh_token_hash IS NOT NULL;
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;

-- Only high-entropy random tokens are stored, and only as SHA-256 hashes.
CREATE TABLE verification_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL,
    email      text NOT NULL,
    purpose    text NOT NULL DEFAULT 'account_verification'
               CHECK (purpose IN ('account_verification', 'password_reset')),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX verification_tokens_hash_idx ON verification_tokens (token_hash);
CREATE INDEX verification_tokens_user_idx ON verification_tokens (user_id);
CREATE INDEX verification_tokens_expires_idx ON verification_tokens (expires_at) WHERE used_at IS NULL;

CREATE TABLE mfa_factors (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    factor_type text NOT NULL DEFAULT 'totp' CHECK (factor_type IN ('totp', 'sms', 'email')),
    secret      text NOT NULL,
    verified    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX mfa_factors_user_idx ON mfa_factors (user_id);

CREATE TABLE auth_audit (
    id         bigserial PRIMARY KEY,
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    action     text NOT NULL,
    ip_address inet,
    user_agent text,
    metadata   jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX auth_audit_user_idx ON auth_audit (user_id, created_at DESC);
CREATE INDEX auth_audit_created_idx ON auth_audit (created_at DESC);

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

-- Only tables that own an updated_at column receive the trigger.
CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
