ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS warmup_daily_cap_override integer,
    ADD COLUMN IF NOT EXISTS warmup_override_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS warmup_override_reason text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS warmup_override_set_by uuid,
    ADD COLUMN IF NOT EXISTS warmup_override_set_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'sending_domains_warmup_daily_cap_override_positive'
          AND conrelid = 'sending_domains'::regclass
    ) THEN
        ALTER TABLE sending_domains
            ADD CONSTRAINT sending_domains_warmup_daily_cap_override_positive
            CHECK (warmup_daily_cap_override IS NULL OR warmup_daily_cap_override > 0);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'sending_domains_warmup_override_fields_consistent'
          AND conrelid = 'sending_domains'::regclass
    ) THEN
        ALTER TABLE sending_domains
            ADD CONSTRAINT sending_domains_warmup_override_fields_consistent
            CHECK (
                (warmup_daily_cap_override IS NULL
                    AND warmup_override_expires_at IS NULL
                    AND warmup_override_set_by IS NULL
                    AND warmup_override_set_at IS NULL)
                OR
                (warmup_daily_cap_override IS NOT NULL
                    AND warmup_override_expires_at IS NOT NULL
                    AND warmup_override_set_by IS NOT NULL
                    AND warmup_override_set_at IS NOT NULL
                    AND warmup_override_expires_at > warmup_override_set_at
                    AND warmup_override_expires_at <= warmup_override_set_at + interval '14 days')
            );
    END IF;
END
$$;
