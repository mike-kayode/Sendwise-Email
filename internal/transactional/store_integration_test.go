package transactional

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestStoreRejectsEnqueueForSuspendedWorkspace(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, status)
		VALUES ('Suspended transactional test', 'suspended-transactional-test', 'suspended')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create suspended test organization: %v", err)
	}

	_, err := NewStore(pool).Enqueue(ctx, EnqueueOptions{
		OrganizationID: organizationID,
		EnforceLimits:  true,
	}, MessageInput{
		From:    "receipts@example.test",
		To:      []string{"person@example.test"},
		Subject: "Receipt",
		HTML:    "<p>Thanks for your purchase</p>",
	})
	if !errors.Is(err, ErrWorkspacePaused) {
		t.Fatalf("enqueue for suspended workspace error = %v, want ErrWorkspacePaused", err)
	}
}

func TestStoreEnqueueIsIdempotentAndSMTPAcceptanceIsNotDelivery(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Transactional test', 'transactional-test')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	var settingsDisabled bool
	if err := pool.QueryRow(ctx, `
		SELECT NOT enabled FROM transactional_settings WHERE organization_id = $1
	`, organizationID).Scan(&settingsDisabled); err != nil {
		t.Fatalf("load default transactional setting: %v", err)
	}
	if !settingsDisabled {
		t.Fatal("new organization did not default transactional delivery to disabled")
	}

	store := NewStore(pool)
	input := MessageInput{
		From:    "receipts@example.test",
		To:      []string{"person@example.test"},
		Subject: "Receipt",
		HTML:    "<p>Thanks for your purchase</p>",
	}
	options := EnqueueOptions{OrganizationID: organizationID, IdempotencyKey: "order-193"}
	first, err := store.Enqueue(ctx, options, input)
	if err != nil {
		t.Fatalf("enqueue transactional message: %v", err)
	}
	second, err := store.Enqueue(ctx, options, input)
	if err != nil {
		t.Fatalf("repeat idempotent enqueue: %v", err)
	}
	if first.ID != second.ID || first.MessageID != second.MessageID || !second.Duplicate {
		t.Fatalf("duplicate enqueue returned a different result: first=%+v second=%+v", first, second)
	}

	var messageCount, recipientCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transactional_messages WHERE organization_id = $1`, organizationID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE m.organization_id = $1
	`, organizationID).Scan(&recipientCount); err != nil {
		t.Fatalf("count recipients: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_message_events e
		WHERE e.organization_id = $1
	`, organizationID).Scan(&eventCount); err != nil {
		t.Fatalf("count message events: %v", err)
	}
	if messageCount != 1 || recipientCount != 1 || eventCount != 1 {
		t.Fatalf("idempotency created duplicate records: messages=%d recipients=%d events=%d", messageCount, recipientCount, eventCount)
	}

	job, err := store.ClaimDue(ctx, 2*time.Minute)
	if err != nil {
		t.Fatalf("claim transactional message: %v", err)
	}
	if job == nil || job.Organization != organizationID {
		t.Fatalf("claimed job = %+v, want a job for organization %s", job, organizationID)
	}
	if err := store.MarkAccepted(ctx, job, "<smtp-accepted@example.test>"); err != nil {
		t.Fatalf("record SMTP acceptance: %v", err)
	}
	var status string
	var deliveredAt sql.NullString
	if err := pool.QueryRow(ctx, `
		SELECT status, delivered_at::text FROM transactional_messages WHERE id = $1
	`, job.MessageUUID).Scan(&status, &deliveredAt); err != nil {
		t.Fatalf("load accepted message: %v", err)
	}
	if status != "sending" || deliveredAt.Valid {
		t.Fatalf("SMTP acceptance produced status=%q delivered_at=%v, want sending and no delivery timestamp", status, deliveredAt)
	}
	if err := store.TransitionRecipient(ctx, job.RecipientID, StatusDelivered, "stalwart", 0, "", "remote delivery confirmed", time.Time{}, nil); err != nil {
		t.Fatalf("record downstream delivery confirmation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status FROM transactional_messages WHERE id = $1
	`, job.MessageUUID).Scan(&status); err != nil {
		t.Fatalf("load delivered message: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("message status = %q after delivery evidence, want delivered", status)
	}
}

func TestPlatformDisabledDomainBlocksTransactionalSMTPAndWorker(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Disabled domain test', 'disabled-domain-test')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE transactional_settings
		SET enabled = true, status = 'active'
		WHERE organization_id = $1
	`, organizationID); err != nil {
		t.Fatalf("enable transactional settings: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sending_domains (
			organization_id, domain, status, spf_status, dkim_status, transactional_enabled
		) VALUES ($1, 'mailer.example.test', 'verified', 'verified', 'verified', true)
	`, organizationID); err != nil {
		t.Fatalf("insert verified sending domain: %v", err)
	}
	job := &Job{Organization: organizationID, From: "receipts@mailer.example.test"}
	worker := &DeliveryWorker{pool: pool}
	session := &smtpSubmissionSession{
		backend:    &smtpSubmissionBackend{pool: pool},
		credential: SMTPCredential{OrganizationID: organizationID},
	}
	if err := worker.validateWorkspaceAndDomain(ctx, job); err != nil {
		t.Fatalf("worker rejected enabled domain: %v", err)
	}
	if err := session.validateSender(ctx, job.From); err != nil {
		t.Fatalf("SMTP rejected enabled domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE organizations SET status = 'suspended' WHERE id = $1
	`, organizationID); err != nil {
		t.Fatalf("suspend transactional workspace: %v", err)
	}
	if err := worker.validateWorkspaceAndDomain(ctx, job); !errors.Is(err, ErrWorkspacePaused) {
		t.Fatalf("transactional worker workspace status error = %v, want ErrWorkspacePaused", err)
	}
	if err := session.validateSender(ctx, job.From); err != errSMTPSenderNotAllowed {
		t.Fatalf("SMTP workspace status error = %v, want sender not allowed", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE organizations SET status = 'active' WHERE id = $1
	`, organizationID); err != nil {
		t.Fatalf("resume transactional workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE sending_domains SET admin_disabled_at = now()
		WHERE organization_id = $1 AND lower(domain) = 'mailer.example.test'
	`, organizationID); err != nil {
		t.Fatalf("disable sending domain: %v", err)
	}
	if err := worker.validateWorkspaceAndDomain(ctx, job); err == nil {
		t.Fatal("transactional delivery worker accepted a platform-disabled domain")
	}
	if err := session.validateSender(ctx, job.From); err != errSMTPSenderNotAllowed {
		t.Fatalf("SMTP sender validation error = %v, want sender not allowed", err)
	}
}

func TestStoreTestModeLogsWithoutQueueingDelivery(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ('Test mode', 'test-mode')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	receipt, err := NewStore(pool).Enqueue(ctx, EnqueueOptions{
		OrganizationID: organizationID, IdempotencyKey: "dry-run", Mode: true,
	}, MessageInput{
		From: "alerts@example.test", To: []string{"person@example.test"},
		Subject: "Test", Text: "This must not be sent",
	})
	if err != nil {
		t.Fatalf("enqueue test-mode message: %v", err)
	}
	if receipt.Status != StatusRejected {
		t.Fatalf("test-mode receipt status = %q, want rejected/non-delivering", receipt.Status)
	}
	var recipientStatus string
	if err := pool.QueryRow(ctx, `
		SELECT r.status
		FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE m.id = $1
	`, receipt.ID).Scan(&recipientStatus); err != nil {
		t.Fatalf("load test-mode recipient: %v", err)
	}
	if recipientStatus != "rejected" {
		t.Fatalf("test-mode recipient status = %q, want rejected", recipientStatus)
	}
	var sentEventCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_message_events
		WHERE message_id = $1 AND event_type = 'message.sent'
	`, receipt.ID).Scan(&sentEventCount); err != nil {
		t.Fatalf("count test-mode send events: %v", err)
	}
	if sentEventCount != 0 {
		t.Fatalf("test-mode generated %d send events", sentEventCount)
	}
}

func TestStoreAppliesStalwartDeliveryReportsIdempotently(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Transactional feedback', 'transactional-feedback')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}

	store := NewStore(pool)
	receipt, err := store.Enqueue(ctx, EnqueueOptions{OrganizationID: organizationID}, MessageInput{
		From: "receipts@example.test", To: []string{"person@example.test"},
		Subject: "Receipt", Text: "Your receipt",
	})
	if err != nil {
		t.Fatalf("enqueue transactional message: %v", err)
	}
	job, err := store.ClaimDue(ctx, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim transactional message = %+v, err=%v", job, err)
	}
	if err := store.MarkAccepted(ctx, job, "<smtp-accepted@example.test>"); err != nil {
		t.Fatalf("record SMTP acceptance: %v", err)
	}

	claims := delivery.VERPClaims{
		WorkspaceID: organizationID,
		CampaignID:  "transactional:" + receipt.ID,
		Recipient:   "person@example.test",
	}
	if err := store.ApplyFeedback(ctx, "delay-event", claims, delivery.Feedback{
		Type: delivery.FeedbackSoftBounce, Action: "delayed",
		EnhancedCode: "4.2.0", Diagnostic: "450 mailbox busy",
	}); err != nil {
		t.Fatalf("apply temporary delivery report: %v", err)
	}
	if err := store.ApplyFeedback(ctx, "delivery-event", claims, delivery.Feedback{
		Type: delivery.FeedbackDelivered, Action: "delivered",
		EnhancedCode: "2.0.0", Diagnostic: "250 recipient server accepted message",
	}); err != nil {
		t.Fatalf("apply successful delivery report: %v", err)
	}
	if err := store.ApplyFeedback(ctx, "delivery-event", claims, delivery.Feedback{
		Type: delivery.FeedbackDelivered, Action: "delivered", EnhancedCode: "2.0.0",
	}); err != nil {
		t.Fatalf("reapply duplicate delivery report: %v", err)
	}

	var messageStatus, recipientStatus string
	if err := pool.QueryRow(ctx, `
		SELECT m.status, r.status
		FROM transactional_messages m
		JOIN transactional_message_recipients r ON r.message_id = m.id
		WHERE m.id = $1
	`, receipt.ID).Scan(&messageStatus, &recipientStatus); err != nil {
		t.Fatalf("load final transactional status: %v", err)
	}
	if messageStatus != string(StatusDelivered) || recipientStatus != string(StatusDelivered) {
		t.Fatalf("final statuses = message %q, recipient %q; want delivered", messageStatus, recipientStatus)
	}

	var feedbackCount, deliveryEventCount, deliveredUsage int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_feedback_ingest WHERE message_id = $1
	`, receipt.ID).Scan(&feedbackCount); err != nil {
		t.Fatalf("count feedback records: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_message_events
		WHERE message_id = $1 AND event_type = 'message.delivered'
	`, receipt.ID).Scan(&deliveryEventCount); err != nil {
		t.Fatalf("count delivery events: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT delivered_count FROM transactional_usage_daily
		WHERE organization_id = $1 AND usage_date = current_date
	`, organizationID).Scan(&deliveredUsage); err != nil {
		t.Fatalf("load delivery usage: %v", err)
	}
	if feedbackCount != 2 || deliveryEventCount != 1 || deliveredUsage != 1 {
		t.Fatalf("feedback=%d delivery_events=%d delivered_usage=%d; want 2, 1, 1",
			feedbackCount, deliveryEventCount, deliveredUsage)
	}
}

func TestStoreAutoPausesWorkspaceAfterExcessiveBounces(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Transactional reputation', 'transactional-reputation')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE transactional_settings
		SET enabled = true, status = 'active', minimum_sample_size = 1, max_bounce_rate = 0
		WHERE organization_id = $1
	`, organizationID); err != nil {
		t.Fatalf("configure transactional reputation threshold: %v", err)
	}

	store := NewStore(pool)
	receipt, err := store.Enqueue(ctx, EnqueueOptions{OrganizationID: organizationID}, MessageInput{
		From: "receipts@example.test", To: []string{"unknown@example.test"},
		Subject: "Receipt", Text: "Your receipt",
	})
	if err != nil {
		t.Fatalf("enqueue transactional message: %v", err)
	}
	job, err := store.ClaimDue(ctx, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim transactional message = %+v, err=%v", job, err)
	}
	if err := store.MarkAccepted(ctx, job, "<smtp-accepted@example.test>"); err != nil {
		t.Fatalf("record SMTP acceptance: %v", err)
	}
	if err := store.ApplyFeedback(ctx, "hard-bounce-event", delivery.VERPClaims{
		WorkspaceID: organizationID,
		CampaignID:  "transactional:" + receipt.ID,
		Recipient:   "unknown@example.test",
	}, delivery.Feedback{
		Type: delivery.FeedbackHardBounce, Action: "failed",
		EnhancedCode: "5.1.1", Diagnostic: "550 user unknown",
	}); err != nil {
		t.Fatalf("apply hard-bounce report: %v", err)
	}

	var status, pauseReason string
	if err := pool.QueryRow(ctx, `
		SELECT status, pause_reason FROM transactional_settings WHERE organization_id = $1
	`, organizationID).Scan(&status, &pauseReason); err != nil {
		t.Fatalf("load transactional settings after bounce: %v", err)
	}
	if status != "paused" || !strings.Contains(pauseReason, "bounce rate") {
		t.Fatalf("workspace status=%q pause_reason=%q; want automatic bounce-rate pause", status, pauseReason)
	}
	var invalidSuppressions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_suppressions
		WHERE organization_id IS NULL AND lower(email) = 'unknown@example.test' AND reason = 'invalid'
	`).Scan(&invalidSuppressions); err != nil || invalidSuppressions != 1 {
		t.Fatalf("global invalid suppression count = %d, err=%v; want 1", invalidSuppressions, err)
	}
}
