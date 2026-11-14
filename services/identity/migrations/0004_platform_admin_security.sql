ALTER TABLE users
    ADD COLUMN IF NOT EXISTS platform_role text NOT NULL DEFAULT 'none';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'users_platform_role_check'
          AND conrelid = 'users'::regclass
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT users_platform_role_check
            CHECK (platform_role IN ('none', 'support_admin', 'super_admin'));
    END IF;
END;
$$;

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS admin_mfa_verified_at timestamptz;

CREATE TABLE IF NOT EXISTS admin_mfa (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_mfa_recovery_codes (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash text NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE IF NOT EXISTS admin_mfa_lockouts (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    failed_attempts integer NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS admin_audit (
    id bigserial PRIMARY KEY,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    actor_role text NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id text NOT NULL DEFAULT '',
    workspace_id uuid,
    before_state jsonb,
    after_state jsonb,
    reason text NOT NULL CHECK (length(trim(reason)) > 0),
    ip_address inet,
    user_agent text NOT NULL DEFAULT '',
    request_id text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS admin_audit_created_idx ON admin_audit (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS admin_audit_actor_idx ON admin_audit (actor_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS admin_audit_target_idx ON admin_audit (target_type, target_id, created_at DESC);

CREATE OR REPLACE FUNCTION reject_admin_audit_mutation()
RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'admin_audit is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS admin_audit_no_row_mutation ON admin_audit;
CREATE TRIGGER admin_audit_no_row_mutation
    BEFORE UPDATE OR DELETE ON admin_audit
    FOR EACH ROW EXECUTE FUNCTION reject_admin_audit_mutation();

DROP TRIGGER IF EXISTS admin_audit_no_truncate ON admin_audit;
CREATE TRIGGER admin_audit_no_truncate
    BEFORE TRUNCATE ON admin_audit
    FOR EACH STATEMENT EXECUTE FUNCTION reject_admin_audit_mutation();

REVOKE UPDATE, DELETE, TRUNCATE ON admin_audit FROM PUBLIC;
