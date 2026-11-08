ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS warmup_profile text NOT NULL DEFAULT 'standard';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'sending_domains_warmup_profile_check'
          AND conrelid = 'sending_domains'::regclass
    ) THEN
        ALTER TABLE sending_domains
            ADD CONSTRAINT sending_domains_warmup_profile_check
            CHECK (warmup_profile IN ('standard', 'resend'));
    END IF;
END $$;

ALTER TABLE sending_domain_daily_stats
    ADD COLUMN IF NOT EXISTS bounce_count integer NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'sending_domain_daily_stats_bounce_count_check'
          AND conrelid = 'sending_domain_daily_stats'::regclass
    ) THEN
        ALTER TABLE sending_domain_daily_stats
            ADD CONSTRAINT sending_domain_daily_stats_bounce_count_check
            CHECK (bounce_count >= 0);
    END IF;
END $$;
