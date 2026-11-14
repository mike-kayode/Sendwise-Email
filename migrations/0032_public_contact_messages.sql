CREATE TABLE IF NOT EXISTS contact_messages (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    email text NOT NULL,
    topic text NOT NULL,
    message text NOT NULL,
    notification_status text NOT NULL DEFAULT 'disabled'
        CHECK (notification_status IN ('disabled', 'pending', 'sent', 'failed')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS contact_messages_created_at_idx
    ON contact_messages (created_at DESC);
