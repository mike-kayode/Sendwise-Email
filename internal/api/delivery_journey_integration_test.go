package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/delivery"
)

func TestStalwartTelemetryWebhookRecordsIdempotentJourney(t *testing.T) {
	if integrationPool == nil {
		t.Skip("TEST_DATABASE_URL is required for delivery journey integration")
	}
	ctx := context.Background()
	orgID := uuid.NewString()
	campaignID, contactID, recipientID, jobID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	email := "journey-" + uuid.NewString() + "@example.test"
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1::uuid, 'Journey integration', $2::text)
	`, orgID, "journey-"+orgID); err != nil {
		t.Fatalf("create journey organization: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO contacts (id, organization_id, email, status)
		VALUES ($1::uuid, $2::uuid, $3::text, 'subscribed')
	`, contactID, orgID, email); err != nil {
		t.Fatalf("create journey contact: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, subject, content, status)
		VALUES ($1::uuid, $2::uuid, 'Journey telemetry', 'Test', '{}'::jsonb, 'sending')
	`, campaignID, orgID); err != nil {
		t.Fatalf("create journey campaign: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_jobs (id, organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::text, 'submitted')
	`, jobID, orgID, campaignID, contactID, "message-"+jobID+"@example.test"); err != nil {
		t.Fatalf("create journey delivery job: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaign_recipient_tracking
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id, tracking_id, email)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::text, $7::text)
	`, orgID, campaignID, recipientID, contactID, jobID, "trk_"+jobID, email); err != nil {
		t.Fatalf("create journey tracking row: %v", err)
	}
	token, err := delivery.NewVERPToken(integrationCfg.JWTSecret, delivery.VERPClaims{
		WorkspaceID: orgID,
		CampaignID:  campaignID,
		Recipient:   strings.ToLower(email),
	})
	if err != nil {
		t.Fatalf("create journey VERP token: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_verp_tokens
			(token, organization_id, campaign_id, delivery_job_id, contact_id, email)
		VALUES ($1::text, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::text)
	`, token, orgID, campaignID, jobID, contactID, email); err != nil {
		t.Fatalf("persist journey VERP token: %v", err)
	}
	returnPath, err := delivery.FormatVERPReturnPath(integrationCfg.BounceDomain, token)
	if err != nil {
		t.Fatalf("format journey VERP return path: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"events": []any{map[string]any{
			"id":        "delivery-event-" + uuid.NewString(),
			"createdAt": time.Now().UTC(),
			"type":      "delivery.delivered",
			"data": map[string]any{
				"from": returnPath, "to": []string{email},
				"messageId": "message-" + jobID + "@example.test",
				"hostname":  "mx.example.test", "code": 250,
			},
		}},
	})
	if err != nil {
		t.Fatalf("encode Stalwart telemetry: %v", err)
	}
	server := newIntegrationServer(t)
	signature := func(body []byte) string {
		mac := hmac.New(sha256.New, []byte(integrationCfg.WebhookSecret))
		_, _ = mac.Write(body)
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/stalwart-events", strings.NewReader(string(payload)))
		request.Header.Set("X-Signature", signature(payload))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("telemetry attempt %d status = %d, body=%s; want 204",
				attempt+1, response.Code, response.Body.String())
		}
	}
	var status string
	if err := integrationPool.QueryRow(ctx, `
		SELECT journey_status
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&status); err != nil || status != "accepted_by_recipient_server" {
		t.Fatalf("journey status = %q, err=%v; want accepted_by_recipient_server", status, err)
	}
	var eventCount int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*)
		FROM delivery_journey_events
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, orgID, jobID).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("journey events = %d, err=%v; want one event after duplicate delivery", eventCount, err)
	}
}
