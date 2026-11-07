CREATE TABLE IF NOT EXISTS resend_webhook_events (
    svix_id text PRIMARY KEY,
    workspace_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    event_type text NOT NULL,
    payload_sha256 text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS resend_webhook_events_workspace_received_idx
    ON resend_webhook_events (workspace_id, received_at DESC);
