ALTER TABLE delivery_jobs
    ADD COLUMN IF NOT EXISTS failure_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS processing_started_at timestamptz;

CREATE INDEX IF NOT EXISTS delivery_jobs_stale_processing_idx
    ON delivery_jobs (processing_started_at)
    WHERE status = 'processing';
