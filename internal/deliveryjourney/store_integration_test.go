package deliveryjourney

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestStoreRecordDeduplicatesAndEnforcesJourneyOrder(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	orgID, campaignID, _, jobID, recipientID := seedJourneyRecipient(t, ctx, pool)
	store := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)

	event := Event{
		OrganizationID: orgID,
		JobID:          jobID,
		Type:           "submitted_to_mail_server",
		Source:         "sendwise",
		SourceEventID:  "smtp-accepted-1",
		OccurredAt:     now,
		Metadata:       json.RawMessage(`{"smtp_accepted":true}`),
	}
	inserted, err := store.Record(ctx, event)
	if err != nil || !inserted {
		t.Fatalf("record submitted event = %v, %v; want true, nil", inserted, err)
	}
	inserted, err = store.Record(ctx, event)
	if err != nil || inserted {
		t.Fatalf("record duplicate event = %v, %v; want false, nil", inserted, err)
	}

	older := event
	older.Type = "queued"
	older.SourceEventID = "queued-late"
	older.OccurredAt = now.Add(-time.Minute)
	if _, err := store.Record(ctx, older); err != nil {
		t.Fatalf("record out-of-order queued event: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT journey_status
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND campaign_id = $2::uuid
		  AND recipient_id = $3::uuid AND delivery_job_id = $4::uuid
	`, orgID, campaignID, recipientID, jobID).Scan(&status); err != nil {
		t.Fatalf("load journey status: %v", err)
	}
	if status != "submitted_to_mail_server" {
		t.Fatalf("journey status = %q, want submitted_to_mail_server", status)
	}

	acceptance := Event{
		OrganizationID: orgID,
		JobID:          jobID,
		Type:           "accepted_by_recipient_server",
		Source:         "stalwart",
		SourceEventID:  "acceptance-late",
		OccurredAt:     now.Add(time.Minute),
	}
	if _, err := store.Record(ctx, acceptance); err != nil {
		t.Fatalf("record recipient-server acceptance: %v", err)
	}
	bounce := Event{
		OrganizationID: orgID,
		JobID:          jobID,
		Type:           "bounced",
		Source:         "dsn",
		SourceEventID:  "late-dsn-1",
		OccurredAt:     now.Add(2 * time.Minute),
		Reason:         "550 recipient rejected",
	}
	if _, err := store.Record(ctx, bounce); err != nil {
		t.Fatalf("record late bounce: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT journey_status
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&status); err != nil {
		t.Fatalf("reload journey status: %v", err)
	}
	if status != "bounced" {
		t.Fatalf("journey status after late bounce = %q, want bounced", status)
	}
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_journey_events
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&count); err != nil {
		t.Fatalf("count journey events: %v", err)
	}
	if count != 4 {
		t.Fatalf("journey event count = %d, want four unique events", count)
	}
}

type fixedQueueReader struct {
	wantReturnPath string
	wantRecipient  string
	result         QueueRecipient
	calls          int
}

func (r *fixedQueueReader) LookupRecipient(_ context.Context, returnPath, recipient string) (QueueRecipient, bool, error) {
	r.calls++
	if returnPath != r.wantReturnPath || recipient != r.wantRecipient {
		return QueueRecipient{}, false, nil
	}
	return r.result, true, nil
}

func TestReconcilerUsesQueueAndRecordsFinalStatus(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	orgID, campaignID, contactID, jobID, _ := seedJourneyRecipient(t, ctx, pool)
	email := "journey-" + contactID + "@example.test"
	if _, err := pool.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET journey_status = 'submitted_to_mail_server'
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID); err != nil {
		t.Fatalf("set submitted journey state: %v", err)
	}
	token, err := delivery.NewVERPToken("journey-test-token", delivery.VERPClaims{
		WorkspaceID: orgID,
		CampaignID:  campaignID,
		Recipient:   email,
	})
	if err != nil {
		t.Fatalf("create reconciliation token: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO delivery_verp_tokens
			(token, organization_id, campaign_id, delivery_job_id, contact_id, email)
		VALUES ($1::text, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::text)
	`, token, orgID, campaignID, jobID, contactID, email); err != nil {
		t.Fatalf("persist reconciliation token: %v", err)
	}
	returnPath, err := delivery.FormatVERPReturnPath("bounces.example.test", token)
	if err != nil {
		t.Fatalf("format reconciliation return path: %v", err)
	}
	code := 250
	reader := &fixedQueueReader{
		wantReturnPath: returnPath,
		wantRecipient:  email,
		result: QueueRecipient{
			Status: "completed", RemoteHost: "mx.example.test", SMTPCode: &code,
			Reason: "250 accepted",
		},
	}
	reconciler := NewReconcilerWithReader(pool, reader, "bounces.example.test")
	results, err := reconciler.ReconcileBatch(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("reconcile submitted journey: %v", err)
	}
	if len(results) != 1 || results[0].Status != "accepted_by_recipient_server" {
		t.Fatalf("reconciliation results = %#v; want one recipient-server acceptance", results)
	}
	var status, host string
	if err := pool.QueryRow(ctx, `
		SELECT journey_status, journey_remote_host
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&status, &host); err != nil {
		t.Fatalf("load reconciled journey: %v", err)
	}
	if status != "accepted_by_recipient_server" || host != "mx.example.test" {
		t.Fatalf("reconciled status/host = %q/%q; want accepted_by_recipient_server/mx.example.test", status, host)
	}
	if reader.calls != 1 {
		t.Fatalf("queue reader called %d times, want one", reader.calls)
	}
}

func TestBackfillBatchSupportsDryRunAndResumesWithoutResending(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	orgID, campaignID, contactID, jobID, _ := seedJourneyRecipient(t, ctx, pool)
	email := "journey-" + contactID + "@example.test"
	if _, err := pool.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET journey_status = 'submitted_to_mail_server'
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID); err != nil {
		t.Fatalf("set submitted journey state: %v", err)
	}
	token, err := delivery.NewVERPToken("journey-test-token", delivery.VERPClaims{
		WorkspaceID: orgID,
		CampaignID:  campaignID,
		Recipient:   email,
	})
	if err != nil {
		t.Fatalf("create backfill token: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO delivery_verp_tokens
			(token, organization_id, campaign_id, delivery_job_id, contact_id, email)
		VALUES ($1::text, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::text)
	`, token, orgID, campaignID, jobID, contactID, email); err != nil {
		t.Fatalf("persist backfill token: %v", err)
	}
	reader := &fixedQueueReader{wantReturnPath: "never-match", wantRecipient: "never-match"}
	reconciler := NewReconcilerWithReader(pool, reader, "bounces.example.test")
	preview, err := reconciler.BackfillBatch(ctx, "", 1, true)
	if err != nil {
		t.Fatalf("preview delivery journey backfill: %v", err)
	}
	if preview.Processed != 1 || preview.HasMore || preview.Counts["accepted_final_state_unknown"] != 1 {
		t.Fatalf("dry-run backfill result = %+v", preview)
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT journey_status
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&status); err != nil {
		t.Fatalf("load status after dry run: %v", err)
	}
	if status != "submitted_to_mail_server" {
		t.Fatalf("dry-run changed journey status to %q", status)
	}
	applied, err := reconciler.BackfillBatch(ctx, "", 1, false)
	if err != nil {
		t.Fatalf("apply delivery journey backfill: %v", err)
	}
	if applied.Processed != 1 || applied.Counts["accepted_final_state_unknown"] != 1 {
		t.Fatalf("applied backfill result = %+v", applied)
	}
	if err := pool.QueryRow(ctx, `
		SELECT journey_status
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&status); err != nil {
		t.Fatalf("load status after backfill: %v", err)
	}
	if status != "accepted_final_state_unknown" {
		t.Fatalf("backfilled journey status = %q; want accepted_final_state_unknown", status)
	}
	var jobStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM delivery_jobs WHERE organization_id = $1::uuid AND id = $2::uuid
	`, orgID, jobID).Scan(&jobStatus); err != nil {
		t.Fatalf("load delivery job after backfill: %v", err)
	}
	if jobStatus != "submitted" {
		t.Fatalf("backfill changed delivery job status to %q; must never resend", jobStatus)
	}
}

func seedJourneyRecipient(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, string, string, string, string) {
	t.Helper()
	orgID, campaignID, contactID, jobID, recipientID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, name, slug) VALUES ($1::uuid, 'Journey integration', $2::text)`, []any{orgID, "journey-" + orgID}},
		{`INSERT INTO contacts (id, organization_id, email, status) VALUES ($1::uuid, $2::uuid, $3::text, 'subscribed')`, []any{contactID, orgID, "journey-" + contactID + "@example.test"}},
		{`INSERT INTO campaigns (id, organization_id, name, subject, content, status) VALUES ($1::uuid, $2::uuid, 'Journey test', 'Test', '{}'::jsonb, 'sending')`, []any{campaignID, orgID}},
		{`INSERT INTO delivery_jobs (id, organization_id, campaign_id, contact_id, message_id, status) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::text, 'submitted')`, []any{jobID, orgID, campaignID, contactID, "journey-" + jobID + "@example.test"}},
		{`INSERT INTO campaign_recipient_tracking (organization_id, campaign_id, recipient_id, contact_id, delivery_job_id, tracking_id, email) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::text, $7::text)`, []any{orgID, campaignID, recipientID, contactID, jobID, "trk_" + jobID, "journey-" + contactID + "@example.test"}},
	} {
		if _, err := pool.Exec(ctx, query.sql, query.args...); err != nil {
			t.Fatalf("seed journey integration row: %v", err)
		}
	}
	return orgID, campaignID, contactID, jobID, recipientID
}
