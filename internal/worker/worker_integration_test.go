package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/testutil"
)

type noDuplicatePublisher struct {
	published int
}

func (p *noDuplicatePublisher) Publish(context.Context, string, []byte) error {
	p.published++
	return fmt.Errorf("unexpected publish")
}

func TestEnsureRecipientTrackingCreatesMissingRow(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID, contactID, campaignID, recipientID, jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Missing tracking test', 'missing-tracking-' || gen_random_uuid()::text)
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'missing-tracking@example.test', 'subscribed')
		RETURNING id::text
	`, organizationID).Scan(&contactID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, subject, sender_name, sender_email, content, status)
		VALUES ($1, 'Missing tracking campaign', 'Test', 'Sendwise', 'sender@example.test', '{}'::jsonb, 'sending')
		RETURNING id::text
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email)
		VALUES ($1, $2, $3, 'missing-tracking@example.test')
		RETURNING id::text
	`, organizationID, campaignID, contactID).Scan(&recipientID); err != nil {
		t.Fatalf("create campaign recipient: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, 'missing-tracking@example.test', 'queued')
		RETURNING id::text
	`, organizationID, campaignID, contactID).Scan(&jobID); err != nil {
		t.Fatalf("create delivery job: %v", err)
	}

	worker := &Worker{pool: pool}
	trackingID, err := worker.ensureRecipientTracking(ctx, &jobRow{
		ID: jobID, OrganizationID: organizationID, CampaignID: campaignID, ContactID: contactID,
	})
	if err != nil {
		t.Fatalf("ensureRecipientTracking(): %v", err)
	}
	if !strings.HasPrefix(trackingID, "trk_") {
		t.Fatalf("tracking ID = %q, want trk_ prefix", trackingID)
	}
	var storedRecipientID, storedTrackingID string
	if err := pool.QueryRow(ctx, `
		SELECT recipient_id::text, tracking_id
		FROM campaign_recipient_tracking
		WHERE delivery_job_id = $1
	`, jobID).Scan(&storedRecipientID, &storedTrackingID); err != nil {
		t.Fatalf("load created tracking row: %v", err)
	}
	if storedRecipientID != recipientID || storedTrackingID != trackingID {
		t.Fatalf("tracking row recipient=%q tracking=%q, want recipient=%q tracking=%q",
			storedRecipientID, storedTrackingID, recipientID, trackingID)
	}
}

func TestDeadLetterFailureIsAtomicAndNotRepublished(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID, contactID, campaignID, jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Dead letter regression', 'dead-letter-' || gen_random_uuid()::text)
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'recipient@example.test', 'subscribed')
		RETURNING id::text
	`, organizationID).Scan(&contactID); err != nil {
		t.Fatalf("create test contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, subject, sender_name, sender_email, content, status)
		VALUES ($1, 'Dead letter test', 'Test', 'Sendwise', 'sender@example.test', '{}'::jsonb, 'sending')
		RETURNING id::text
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create test campaign: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status, attempts)
		VALUES ($1, $2, $3, 'dead-letter@example.test', 'processing', 2)
		RETURNING id::text
	`, organizationID, campaignID, contactID).Scan(&jobID); err != nil {
		t.Fatalf("create exhausted delivery job: %v", err)
	}

	worker := &Worker{pool: pool, logger: slog.Default()}
	row := &jobRow{
		ID: jobID, OrganizationID: organizationID, CampaignID: campaignID,
		ContactID: contactID, Email: "recipient@example.test", Attempts: 2,
	}
	if err := worker.deadLetterJob(ctx, row, "max_attempts_exceeded", errors.New("fake SMTP failure")); err != nil {
		t.Fatalf("deadLetterJob(): %v", err)
	}
	var status string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM delivery_jobs WHERE id = $1`, jobID).Scan(&status, &attempts); err != nil {
		t.Fatalf("load failed job: %v", err)
	}
	if status != "failed" || attempts != 3 {
		t.Fatalf("delivery job status=%q attempts=%d, want failed and 3", status, attempts)
	}
	var deadLetterRows, eventRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dead_letter_queue WHERE delivery_job_id = $1`, jobID).Scan(&deadLetterRows); err != nil {
		t.Fatalf("count dead-letter rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM message_events
		WHERE delivery_job_id = $1 AND event_type = 'failed'
		  AND metadata->>'reason' = 'max_attempts_exceeded'
	`, jobID).Scan(&eventRows); err != nil {
		t.Fatalf("count dead-letter message events: %v", err)
	}
	if deadLetterRows != 1 || eventRows != 1 {
		t.Fatalf("dead-letter rows=%d event rows=%d, want one each", deadLetterRows, eventRows)
	}
	publisher := &noDuplicatePublisher{}
	service := campaigns.NewService(pool, publisher, slog.Default(), 3, false)
	if published, err := service.RepublishPending(ctx, 1000); err != nil || published != 0 {
		t.Fatalf("RepublishPending() = %d, %v; want failed job not republished", published, err)
	}
	if publisher.published != 0 {
		t.Fatalf("publisher received %d failed-job publishes, want zero", publisher.published)
	}
}

func seedTrackingJob(t *testing.T, pool *pgxpool.Pool, label string) *jobRow {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	var organizationID, contactID, campaignID, recipientID, jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ($1, $2)
		RETURNING id::text
	`, label, label+"-"+suffix).Scan(&organizationID); err != nil {
		t.Fatalf("create %s organization: %v", label, err)
	}
	email := "recipient-" + suffix + "@example.test"
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, $2, 'subscribed')
		RETURNING id::text
	`, organizationID, email).Scan(&contactID); err != nil {
		t.Fatalf("create %s contact: %v", label, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, subject, sender_name, sender_email, content, status)
		VALUES ($1, $2, 'Test', 'Sendwise', 'sender@example.test', '{}'::jsonb, 'sending')
		RETURNING id::text
	`, organizationID, label).Scan(&campaignID); err != nil {
		t.Fatalf("create %s campaign: %v", label, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING id::text
	`, organizationID, campaignID, contactID, email).Scan(&recipientID); err != nil {
		t.Fatalf("create %s campaign recipient: %v", label, err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, $4, 'processing')
		RETURNING id::text
	`, organizationID, campaignID, contactID, "message-"+suffix+"@example.test").Scan(&jobID); err != nil {
		t.Fatalf("create %s delivery job: %v", label, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaign_recipient_tracking
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id, tracking_id, email)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, organizationID, campaignID, recipientID, contactID, jobID, "trk_"+suffix, email); err != nil {
		t.Fatalf("create %s recipient tracking row: %v", label, err)
	}
	return &jobRow{
		ID: jobID, OrganizationID: organizationID, CampaignID: campaignID,
		ContactID: contactID, Email: email, Attempts: 0, MaxAttempts: 5,
	}
}

func TestAcceptedSMTPSubmissionCannotBeRetriedForTrackingFailure(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	worker := &Worker{pool: pool, logger: slog.Default()}

	row := seedTrackingJob(t, pool, "submission-bookkeeping")
	if err := worker.recordSubmission(ctx, row, &delivery.Result{MessageID: "accepted-message@example.test"}); err != nil {
		t.Fatalf("record accepted submission: %v", err)
	}
	var status, transportMessageID string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, transport_message_id
		FROM delivery_jobs WHERE id = $1
	`, row.ID).Scan(&status, &attempts, &transportMessageID); err != nil {
		t.Fatalf("load accepted delivery: %v", err)
	}
	if status != "submitted" || attempts != 1 || transportMessageID != "accepted-message@example.test" {
		t.Fatalf("accepted delivery = status %q attempts %d transport ID %q", status, attempts, transportMessageID)
	}
	var acceptedTrackingEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM email_tracking_events
		WHERE delivery_job_id = $1 AND event_type = 'accepted'
	`, row.ID).Scan(&acceptedTrackingEvents); err != nil {
		t.Fatalf("count accepted tracking events: %v", err)
	}
	if acceptedTrackingEvents != 1 {
		t.Fatalf("accepted tracking events = %d, want 1", acceptedTrackingEvents)
	}

	failedTrackingRow := seedTrackingJob(t, pool, "submission-tracking-failure")
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_tracking_update() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'synthetic tracking-store failure';
		END
		$$
	`); err != nil {
		t.Fatalf("create tracking failure trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER reject_tracking_update
		BEFORE UPDATE ON campaign_recipient_tracking
		FOR EACH ROW EXECUTE FUNCTION reject_tracking_update()
	`); err != nil {
		t.Fatalf("create tracking failure trigger: %v", err)
	}
	if err := worker.recordSubmission(ctx, failedTrackingRow, &delivery.Result{MessageID: "accepted-despite-tracking@example.test"}); err != nil {
		t.Fatalf("record SMTP acceptance with tracking failure: %v", err)
	}
	if err := worker.recordSubmission(ctx, failedTrackingRow, &delivery.Result{MessageID: "duplicate-attempt@example.test"}); err != nil {
		t.Fatalf("repeat accepted-state persistence: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, transport_message_id
		FROM delivery_jobs WHERE id = $1
	`, failedTrackingRow.ID).Scan(&status, &attempts, &transportMessageID); err != nil {
		t.Fatalf("load accepted delivery after tracking failure: %v", err)
	}
	if status != "submitted" || attempts != 1 || transportMessageID != "accepted-despite-tracking@example.test" {
		t.Fatalf("delivery after tracking failure = status %q attempts %d transport ID %q",
			status, attempts, transportMessageID)
	}
	publisher := &noDuplicatePublisher{}
	service := campaigns.NewService(pool, publisher, slog.Default(), 5, false)
	if published, err := service.RepublishPending(ctx, 10); err != nil || published != 0 {
		t.Fatalf("RepublishPending() after accepted SMTP message = %d, %v; want no republish", published, err)
	}
	if publisher.published != 0 {
		t.Fatalf("publisher received %d submitted-job publishes, want zero", publisher.published)
	}
}

func TestWorkerFailureTrackingWritesAgainstPostgres(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	worker := &Worker{pool: pool, logger: slog.Default()}

	t.Run("defer", func(t *testing.T) {
		row := seedTrackingJob(t, pool, "tracking-defer")
		if _, err := worker.handleSendFailure(ctx, row, errors.New("temporary relay timeout")); err != nil {
			t.Fatalf("handle temporary send failure: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM email_tracking_events
			WHERE delivery_job_id = $1 AND event_type = 'deferred'
		`, row.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("deferred tracking event count = %d, err=%v; want 1", count, err)
		}
	})
	t.Run("fail", func(t *testing.T) {
		row := seedTrackingJob(t, pool, "tracking-fail")
		if err := worker.failJob(ctx, row, errors.New("pre-send rendering failure")); err != nil {
			t.Fatalf("fail job: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM email_tracking_events
			WHERE delivery_job_id = $1 AND event_type = 'failed'
		`, row.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("failed tracking event count = %d, err=%v; want 1", count, err)
		}
	})
	t.Run("bounce", func(t *testing.T) {
		row := seedTrackingJob(t, pool, "tracking-bounce")
		if err := worker.bounceJob(ctx, row, errors.New("550 mailbox unavailable"), delivery.BounceHard, "550"); err != nil {
			t.Fatalf("bounce job: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM email_tracking_events
			WHERE delivery_job_id = $1 AND event_type = 'bounced'
		`, row.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("bounced tracking event count = %d, err=%v; want 1", count, err)
		}
	})
	t.Run("dead-letter", func(t *testing.T) {
		row := seedTrackingJob(t, pool, "tracking-dead-letter")
		if err := worker.deadLetterJob(ctx, row, "max_attempts_exceeded", errors.New("retry limit reached")); err != nil {
			t.Fatalf("dead-letter job: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM email_tracking_events
			WHERE delivery_job_id = $1 AND event_type = 'failed'
		`, row.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("dead-letter tracking event count = %d, err=%v; want 1", count, err)
		}
	})
}

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
