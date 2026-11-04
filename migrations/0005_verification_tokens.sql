-- 0005_verification_tokens.sql
-- Email verification tokens for account confirmation.

CREATE TABLE IF NOT EXISTS verification_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token           text NOT NULL UNIQUE,
    email           text NOT NULL,
    purpose         text NOT NULL DEFAULT 'account_verification'
                    CHECK (purpose IN ('account_verification', 'password_reset')),
    expires_at      timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    used_at         timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX verification_tokens_user_idx ON verification_tokens (user_id);
CREATE INDEX verification_tokens_token_idx ON verification_tokens (token);
CREATE INDEX verification_tokens_expires_idx ON verification_tokens (expires_at) WHERE used_at IS NULL;

CREATE TRIGGER verification_tokens_set_updated_at
    BEFORE UPDATE ON verification_tokens
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();