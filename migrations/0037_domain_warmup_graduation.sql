ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS warmup_graduated_at timestamptz,
    ADD COLUMN IF NOT EXISTS warmup_graduated_by uuid,
    ADD COLUMN IF NOT EXISTS warmup_graduated_reason text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'sending_domains_warmup_graduation_fields_consistent'
          AND conrelid = 'sending_domains'::regclass
    ) THEN
        ALTER TABLE sending_domains
            ADD CONSTRAINT sending_domains_warmup_graduation_fields_consistent
            CHECK (
                (warmup_graduated_at IS NULL
                    AND warmup_graduated_by IS NULL
                    AND warmup_graduated_reason = '')
                OR
                (warmup_graduated_at IS NOT NULL
                    AND warmup_graduated_by IS NOT NULL
                    AND length(btrim(warmup_graduated_reason)) > 0)
            );
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS sending_domain_alerts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain_id uuid NOT NULL REFERENCES sending_domains(id) ON DELETE CASCADE,
    alert_type text NOT NULL,
    message text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at timestamptz
);

CREATE INDEX IF NOT EXISTS sending_domain_alerts_domain_idx
    ON sending_domain_alerts (domain_id, created_at DESC);
