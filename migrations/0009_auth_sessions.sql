-- Session store for access-token revocation (added to shared auth policy).
-- Every issued access token contains a session_id; this table records whether
-- the user's current session is still active. The API auth middleware consults
-- it on every request so that a logout immediately revokes all issued tokens.

CREATE TABLE IF NOT EXISTS auth_sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role          text NOT NULL DEFAULT 'viewer'
                    CHECK (role IN ('owner','admin','manager','author','analyst','viewer')),
    session_id    text NOT NULL UNIQUE,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX auth_sessions_user_idx ON auth_sessions (user_id);
CREATE INDEX auth_sessions_org_idx ON auth_sessions (organization_id);
CREATE INDEX auth_sessions_session_idx ON auth_sessions (session_id);

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER auth_sessions_set_updated_at
    BEFORE UPDATE ON auth_sessions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
