package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/testutil"
)

type warmupTestPublisher struct {
	mu        sync.Mutex
	published int
	messages  chan struct{}
}

func (p *warmupTestPublisher) Publish(context.Context, string, []byte) error {
	p.mu.Lock()
	p.published++
	p.mu.Unlock()
	if p.messages != nil {
		p.messages <- struct{}{}
	}
	return nil
}

func TestWarmupOverrideReplacesCurveAndDefersUntilNextDay(t *testing.T) {
	pool, ctx, organizationID, domainID, contactID, campaignID := newWarmupOverrideFixture(t)
	setWarmupTestOverride(t, pool, domainID, 100)
	if _, err := pool.Exec(ctx, `
		INSERT INTO sending_domain_daily_stats
			(organization_id, domain, sending_day, reserved_count)
		VALUES ($1, 'warmup-override.example.test', CURRENT_DATE, 60)
	`, organizationID); err != nil {
		t.Fatalf("seed warm-up usage below override cap: %v", err)
	}
	firstJobID := createWarmupTestJob(t, pool, ctx, organizationID, campaignID, contactID)
	var deferredContactID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'warmup-deferred-' || gen_random_uuid()::text || '@example.test', 'subscribed')
		RETURNING id::text
	`, organizationID).Scan(&deferredContactID); err != nil {
		t.Fatalf("create deferred warm-up contact: %v", err)
	}
	deferredJobID := createWarmupTestJob(t, pool, ctx, organizationID, campaignID, deferredContactID)
	worker := &Worker{
		cfg:  config.Config{WarmupStartPerDay: 50, WarmupGrowthFactor: 1.5, MaxDailyEmails: 10000},
		pool: pool,
	}

	deferred, err := worker.reserveWarmupSlot(ctx, &jobRow{
		ID: firstJobID, OrganizationID: organizationID,
		SenderEmail: "hello@warmup-override.example.test",
	})
	if err != nil || deferred {
		t.Fatalf("reservation below override cap deferred=%t err=%v; want reservation", deferred, err)
	}
	var reserved int
	if err := pool.QueryRow(ctx, `
		SELECT reserved_count FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND domain = 'warmup-override.example.test' AND sending_day = CURRENT_DATE
	`, organizationID).Scan(&reserved); err != nil {
		t.Fatalf("read warm-up usage after override reservation: %v", err)
	}
	if reserved != 61 {
		t.Fatalf("warm-up reserved count = %d, want 61; the 100-message override should replace the 50-message curve", reserved)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE sending_domain_daily_stats SET reserved_count = 100
		WHERE organization_id = $1 AND domain = 'warmup-override.example.test' AND sending_day = CURRENT_DATE
	`, organizationID); err != nil {
		t.Fatalf("fill warm-up override cap: %v", err)
	}
	deferred, err = worker.reserveWarmupSlot(ctx, &jobRow{
		ID: deferredJobID, OrganizationID: organizationID,
		SenderEmail: "hello@warmup-override.example.test",
	})
	if err != nil || !deferred {
		t.Fatalf("reservation at override cap deferred=%t err=%v; want next-day deferral", deferred, err)
	}
	var status string
	var nextAttemptAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, next_attempt_at FROM delivery_jobs WHERE id = $1
	`, deferredJobID).Scan(&status, &nextAttemptAt); err != nil {
		t.Fatalf("read deferred warm-up delivery: %v", err)
	}
	var nextWindow time.Time
	if err := pool.QueryRow(ctx, `SELECT date_trunc('day', now()) + interval '1 day'`).Scan(&nextWindow); err != nil {
		t.Fatalf("read next warm-up window: %v", err)
	}
	if status != "deferred" || !nextAttemptAt.Equal(nextWindow) {
		t.Fatalf("deferred job state = %q at %s; want next window %s", status, nextAttemptAt, nextWindow)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET nats_published_at = now()
		WHERE id = $1
	`, firstJobID); err != nil {
		t.Fatalf("mark earlier job as already published: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE delivery_jobs SET next_attempt_at = now() WHERE id = $1`, deferredJobID); err != nil {
		t.Fatalf("make deferred job due for resume: %v", err)
	}
	publisher := &warmupTestPublisher{}
	service := campaigns.NewService(pool, publisher, slog.New(slog.NewTextHandler(io.Discard, nil)), 5, false)
	published, err := service.RepublishPending(ctx, 1000)
	if err != nil || published != 1 {
		t.Fatalf("resume deferred job published=%d err=%v; want one publish", published, err)
	}
	deferred, err = worker.reserveWarmupSlot(ctx, &jobRow{
		ID: deferredJobID, OrganizationID: organizationID,
		SenderEmail: "hello@warmup-override.example.test",
	})
	if err != nil || !deferred {
		t.Fatalf("resumed job at full override cap deferred=%t err=%v; want cap enforcement", deferred, err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE sending_domain_daily_stats SET sending_day = CURRENT_DATE - 1
		WHERE organization_id = $1 AND domain = 'warmup-override.example.test' AND sending_day = CURRENT_DATE
	`, organizationID); err != nil {
		t.Fatalf("roll full warm-up window forward: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE delivery_jobs SET next_attempt_at = now(), warmup_reserved_on = NULL WHERE id = $1
	`, deferredJobID); err != nil {
		t.Fatalf("make deferred job eligible in next warm-up window: %v", err)
	}
	deferred, err = worker.reserveWarmupSlot(ctx, &jobRow{
		ID: deferredJobID, OrganizationID: organizationID,
		SenderEmail: "hello@warmup-override.example.test",
	})
	if err != nil || deferred {
		t.Fatalf("reservation in next warm-up window deferred=%t err=%v; want resumed reservation", deferred, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT reserved_count FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND domain = 'warmup-override.example.test' AND sending_day = CURRENT_DATE
	`, organizationID).Scan(&reserved); err != nil {
		t.Fatalf("read resumed next-window warm-up usage: %v", err)
	}
	if reserved != 1 {
		t.Fatalf("next-window warm-up usage = %d, want 1", reserved)
	}
}

func TestSchedulerLoopResumesDueDeferredJobs(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID, campaignID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Scheduler resume test', 'scheduler-resume-' || gen_random_uuid()::text)
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create scheduler test workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, status)
		VALUES ($1, 'Scheduler resume test', 'sending')
		RETURNING id::text
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create scheduler test campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, gen_random_uuid(), 'scheduler-resume@example.test', 'deferred')
	`, organizationID, campaignID); err != nil {
		t.Fatalf("create due deferred job: %v", err)
	}

	publisher := &warmupTestPublisher{messages: make(chan struct{}, 1)}
	service := campaigns.NewService(pool, publisher, slog.New(slog.NewTextHandler(io.Discard, nil)), 5, false)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker := &Worker{
		cfg:       config.Config{SchedulerInterval: 25 * time.Millisecond},
		pool:      pool,
		campaigns: service,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		worker.schedulerLoop(ctx)
	}()

	select {
	case <-publisher.messages:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not publish the due deferred job")
	}
	cancel()
	<-done
}

func TestCampaignBounceAutoPauseStillAppliesWithWarmupOverride(t *testing.T) {
	pool, ctx, organizationID, domainID, contactID, campaignID := newWarmupOverrideFixture(t)
	setWarmupTestOverride(t, pool, domainID, 10000)
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaign_recipients
			(organization_id, campaign_id, contact_id, email, status)
		VALUES ($1, $2, $3, 'warmup-recipient@example.test', 'bounced')
	`, organizationID, campaignID, contactID); err != nil {
		t.Fatalf("create bounced campaign recipient: %v", err)
	}

	worker := &Worker{
		cfg:    config.Config{MaxBounceRate: 0.02, MaxComplaintRate: 0.0005},
		pool:   pool,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := worker.checkCampaignReputation(ctx, campaignID, organizationID); err != nil {
		t.Fatalf("check campaign reputation: %v", err)
	}
	var status string
	var alertCount int
	if err := pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, campaignID).Scan(&status); err != nil {
		t.Fatalf("read campaign after reputation check: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM campaign_alerts
		WHERE campaign_id = $1 AND alert_type = 'reputation_pause'
	`, campaignID).Scan(&alertCount); err != nil {
		t.Fatalf("count reputation pause alerts: %v", err)
	}
	if status != "paused" || alertCount != 1 {
		t.Fatalf("campaign reputation state = %q with %d alerts; want paused with one alert", status, alertCount)
	}
}

func newWarmupOverrideFixture(t *testing.T) (pool *pgxpool.Pool, ctx context.Context, organizationID, domainID, contactID, campaignID string) {
	t.Helper()
	pool = testutil.NewPostgres(t)
	ctx = context.Background()
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Warm-up override test', 'warmup-override-' || gen_random_uuid()::text)
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create warm-up workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO sending_domains (organization_id, domain, status)
		VALUES ($1, 'warmup-override.example.test', 'verified')
		RETURNING id::text
	`, organizationID).Scan(&domainID); err != nil {
		t.Fatalf("create warm-up domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_quotas (domain_id, stream, daily_limit, per_second_limit)
		VALUES ($1, 'marketing', 1000, 100)
	`, domainID); err != nil {
		t.Fatalf("set warm-up marketing quota: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'warmup-recipient@example.test', 'subscribed')
		RETURNING id::text
	`, organizationID).Scan(&contactID); err != nil {
		t.Fatalf("create warm-up contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (
			organization_id, name, subject, sender_name, sender_email,
			sender_postal_address, content, status
		)
		VALUES ($1, 'Warm-up override test', 'Notice', 'Sendwise',
			'hello@warmup-override.example.test', '1 Example Street',
			'{"version":1,"blocks":[]}'::jsonb, 'sending')
		RETURNING id::text
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create warm-up campaign: %v", err)
	}
	return pool, ctx, organizationID, domainID, contactID, campaignID
}

func createWarmupTestJob(t *testing.T, pool *pgxpool.Pool, ctx context.Context, organizationID, campaignID, contactID string) string {
	t.Helper()
	var jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, gen_random_uuid()::text || '@warmup.test', 'queued')
		RETURNING id::text
	`, organizationID, campaignID, contactID).Scan(&jobID); err != nil {
		t.Fatalf("create warm-up delivery: %v", err)
	}
	return jobID
}

func setWarmupTestOverride(t *testing.T, pool *pgxpool.Pool, domainID string, cap int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE sending_domains
		SET warmup_daily_cap_override = $2,
		    warmup_override_expires_at = now() + interval '3 days',
		    warmup_override_reason = 'Integration test override',
		    warmup_override_set_by = gen_random_uuid(),
		    warmup_override_set_at = now()
		WHERE id = $1
	`, domainID, cap); err != nil {
		t.Fatalf("set warm-up override: %v", err)
	}
}
