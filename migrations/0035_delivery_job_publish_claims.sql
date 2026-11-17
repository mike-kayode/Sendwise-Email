ALTER TABLE delivery_jobs
    ADD COLUMN IF NOT EXISTS nats_publish_claimed_at timestamptz,
    ADD COLUMN IF NOT EXISTS nats_published_at timestamptz;

CREATE INDEX IF NOT EXISTS delivery_jobs_republish_due_idx
    ON delivery_jobs (status, next_attempt_at, created_at)
    WHERE status IN ('queued', 'deferred');
