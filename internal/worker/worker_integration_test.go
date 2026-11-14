package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestCheckTenantQuotaRejectsSuspendedWorkspace(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, status)
		VALUES ('Suspended campaign test', 'suspended-campaign-test', 'suspended')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create suspended test organization: %v", err)
	}

	worker := &Worker{pool: pool}
	err := worker.checkTenantQuota(ctx, organizationID)
	if !errors.Is(err, errWorkspaceSuspended) {
		t.Fatalf("checkTenantQuota() error = %v, want suspended workspace rejection", err)
	}
}

func TestCampaignQuotaDefersAndReservesAfterQuotaIncrease(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID, domainID, contactID, campaignID, jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Campaign quota test', 'campaign-quota-test')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create quota test organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO sending_domains (organization_id, domain, status)
		VALUES ($1, 'campaign-quota.example.test', 'verified')
		RETURNING id::text
	`, organizationID).Scan(&domainID); err != nil {
		t.Fatalf("create verified sending domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_quotas
			(domain_id, stream, daily_limit, monthly_limit, per_second_limit)
		VALUES ($1, 'marketing', 1, 10, 100)
	`, domainID); err != nil {
		t.Fatalf("set marketing domain quota: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_usage (domain_id, stream, period, period_start, sent_count)
		VALUES ($1, 'marketing', 'day', (now() AT TIME ZONE 'UTC')::date, 1)
	`, domainID); err != nil {
		t.Fatalf("fill marketing daily quota: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'campaign-recipient@example.test', 'subscribed')
		RETURNING id::text
	`, organizationID).Scan(&contactID); err != nil {
		t.Fatalf("create campaign contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (
			organization_id, name, subject, sender_name, sender_email,
			sender_postal_address, content, status
		)
		VALUES ($1, 'Quota defer test', 'Notice', 'Sendwise',
			'hello@campaign-quota.example.test', '1 Example Street',
			'{"version":1,"blocks":[]}'::jsonb, 'sending')
		RETURNING id::text
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create quota test campaign: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, gen_random_uuid()::text || '@quota.test', 'queued')
		RETURNING id::text
	`, organizationID, campaignID, contactID).Scan(&jobID); err != nil {
		t.Fatalf("create campaign delivery: %v", err)
	}

	worker := &Worker{
		cfg:  config.Config{PlatformMaxDomainRate: 100},
		pool: pool,
	}
	job := &jobRow{
		ID: jobID, OrganizationID: organizationID,
		SenderEmail: "hello@campaign-quota.example.test",
	}
	deferred, resetAt, err := worker.reserveCampaignQuota(ctx, job)
	if err != nil || !deferred || !resetAt.After(time.Now()) {
		t.Fatalf("quota reservation deferred=%t reset=%s err=%v; want a future deferral", deferred, resetAt, err)
	}
	var status string
	var quotaDeferred bool
	if err := pool.QueryRow(ctx, `
		SELECT status, quota_deferred FROM delivery_jobs WHERE id = $1
	`, jobID).Scan(&status, &quotaDeferred); err != nil {
		t.Fatalf("load deferred delivery: %v", err)
	}
	if status != "deferred" || !quotaDeferred {
		t.Fatalf("delivery state after quota exhaustion = %q deferred=%t; want deferred quota marker", status, quotaDeferred)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE domain_quotas SET daily_limit = 2
		WHERE domain_id = $1 AND stream = 'marketing'
	`, domainID); err != nil {
		t.Fatalf("raise marketing daily quota: %v", err)
	}
	deferred, _, err = worker.reserveCampaignQuota(ctx, job)
	if err != nil || deferred || job.QuotaReservationID == "" {
		t.Fatalf("reservation after quota increase deferred=%t id=%q err=%v; want an active reservation", deferred, job.QuotaReservationID, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT quota_deferred FROM delivery_jobs WHERE id = $1
	`, jobID).Scan(&quotaDeferred); err != nil {
		t.Fatalf("load resumed delivery marker: %v", err)
	}
	if quotaDeferred {
		t.Fatal("resumed delivery retained its quota-deferred marker")
	}
}
