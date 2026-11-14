package transactional

import (
	"context"
	"testing"

	"github.com/khayode/email-platform/internal/testutil"
)

func TestWebhookEventOutboxQueuesEachEventOnce(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID, webhookID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Webhook outbox', 'webhook-outbox')
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO transactional_webhooks (organization_id, url, encrypted_secret, events)
		VALUES ($1, 'https://hooks.example.test/events', '\x0102'::bytea, ARRAY['message.queued']::text[])
		RETURNING id::text
	`, organizationID).Scan(&webhookID); err != nil {
		t.Fatalf("create test webhook: %v", err)
	}
	receipt, err := NewStore(pool).Enqueue(ctx, EnqueueOptions{OrganizationID: organizationID}, MessageInput{
		From: "receipts@example.test", To: []string{"person@example.test"},
		Subject: "Receipt", Text: "Your receipt",
	})
	if err != nil {
		t.Fatalf("enqueue transactional message: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin webhook outbox transaction: %v", err)
		}
		if err := enqueueWebhookEvents(ctx, tx); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("enqueue webhook events: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit webhook outbox transaction: %v", err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM transactional_webhook_deliveries
		WHERE webhook_id = $1 AND event_type = 'message.queued'
		  AND payload->>'message_id' = $2
	`, webhookID, receipt.ID).Scan(&count); err != nil {
		t.Fatalf("count webhook deliveries: %v", err)
	}
	if count != 1 {
		t.Fatalf("queued webhook deliveries = %d, want exactly one", count)
	}
}
