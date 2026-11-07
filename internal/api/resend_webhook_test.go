package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVerifyResendWebhookSignature(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-secret"))
	body := []byte(`{"type":"email.delivered"}`)
	now := time.Now().UTC()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := signResendTestPayload(t, secret, "evt_signature_test", timestamp, body)

	if !verifyResendWebhookSignature(secret, "evt_signature_test", timestamp, signature, body, now) {
		t.Fatal("valid Svix signature rejected")
	}
	if verifyResendWebhookSignature(secret, "evt_signature_test", timestamp, "v1,bad", body, now) {
		t.Fatal("invalid Svix signature accepted")
	}
	staleTimestamp := strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
	staleSignature := signResendTestPayload(t, secret, "evt_signature_test", staleTimestamp, body)
	if verifyResendWebhookSignature(secret, "evt_signature_test", staleTimestamp, staleSignature, body, now) {
		t.Fatal("stale Svix signature accepted")
	}
}

func TestResendWebhookEventMappingAndIsolation(t *testing.T) {
	if integrationPool == nil {
		t.Skip("set TEST_DATABASE_URL or INTEGRATION_DATABASE_URL to run webhook integration tests")
	}

	ctx := context.Background()
	organizationID := uuid.NewString()
	otherOrganizationID := uuid.NewString()
	for i, id := range []string{organizationID, otherOrganizationID} {
		_, err := integrationPool.Exec(ctx, `
			INSERT INTO organizations (id, name, slug)
			VALUES ($1, $2, $3)
		`, id, fmt.Sprintf("Resend webhook test %d", i), "resend-webhook-"+id[:8])
		if err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		_, _ = integrationPool.Exec(ctx, `DELETE FROM organizations WHERE id = ANY($1::uuid[])`, []string{organizationID, otherOrganizationID})
	}()

	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()
	secret := integrationCfg.ResendWebhookSecret

	tests := []struct {
		name            string
		resendType      string
		eventType       string
		bounceType      string
		suppress        bool
		jobStatus       string
		recipientStatus string
	}{
		{name: "sent", resendType: "email.sent", eventType: "accepted", jobStatus: "accepted", recipientStatus: "submitted"},
		{name: "delivered", resendType: "email.delivered", eventType: "delivered", jobStatus: "accepted", recipientStatus: "submitted"},
		{name: "delayed", resendType: "email.delivery_delayed", eventType: "deferred", jobStatus: "accepted", recipientStatus: "submitted"},
		{name: "hard bounce", resendType: "email.bounced", eventType: "bounced", bounceType: "Permanent", suppress: true, jobStatus: "bounced", recipientStatus: "bounced"},
		{name: "soft bounce", resendType: "email.bounced", eventType: "bounced", bounceType: "Transient", jobStatus: "bounced", recipientStatus: "bounced"},
		{name: "complaint", resendType: "email.complained", eventType: "complained", suppress: true, jobStatus: "complained", recipientStatus: "complained"},
		{name: "opened", resendType: "email.opened", eventType: "opened", jobStatus: "accepted", recipientStatus: "submitted"},
		{name: "clicked", resendType: "email.clicked", eventType: "clicked", jobStatus: "accepted", recipientStatus: "submitted"},
		{name: "failed", resendType: "email.failed", eventType: "failed", jobStatus: "failed", recipientStatus: "failed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			campaignID, _, jobID := seedResendWebhookJob(t, organizationID, test.name)
			email := test.name + "@example.test"
			body := resendTestBody(t, test.resendType, organizationID, campaignID, "message-"+test.name, email, test.bounceType)
			eventID := "evt_" + uuid.NewString()
			resp := postSignedResendEvent(t, server.URL, secret, eventID, body, time.Now())
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("webhook status = %d, want 204", resp.StatusCode)
			}

			var gotEvent string
			var eventCount int
			if err := integrationPool.QueryRow(ctx, `
				SELECT event_type, count(*) OVER ()
				FROM message_events
				WHERE organization_id = $1 AND campaign_id = $2 AND delivery_job_id = $3
			`, organizationID, campaignID, jobID).Scan(&gotEvent, &eventCount); err != nil {
				t.Fatal(err)
			}
			if gotEvent != test.eventType || eventCount != 1 {
				t.Fatalf("event = %q (count %d), want %q (count 1)", gotEvent, eventCount, test.eventType)
			}

			var gotJobStatus, gotRecipientStatus string
			if err := integrationPool.QueryRow(ctx, `
				SELECT dj.status, cr.status
				FROM delivery_jobs dj
				JOIN campaign_recipients cr ON cr.campaign_id = dj.campaign_id AND cr.contact_id = dj.contact_id
				WHERE dj.id = $1
			`, jobID).Scan(&gotJobStatus, &gotRecipientStatus); err != nil {
				t.Fatal(err)
			}
			if gotJobStatus != test.jobStatus || gotRecipientStatus != test.recipientStatus {
				t.Fatalf("statuses = (%q, %q), want (%q, %q)", gotJobStatus, gotRecipientStatus, test.jobStatus, test.recipientStatus)
			}

			var suppressionCount int
			if err := integrationPool.QueryRow(ctx, `
				SELECT count(*) FROM suppressions WHERE organization_id = $1 AND lower(email) = $2
			`, organizationID, email).Scan(&suppressionCount); err != nil {
				t.Fatal(err)
			}
			if (suppressionCount == 1) != test.suppress {
				t.Fatalf("suppression count = %d, want suppressed=%t", suppressionCount, test.suppress)
			}

			if test.name == "sent" {
				replay := postSignedResendEvent(t, server.URL, secret, eventID, body, time.Now())
				replay.Body.Close()
				if replay.StatusCode != http.StatusNoContent {
					t.Fatalf("replayed webhook status = %d, want 204", replay.StatusCode)
				}
				if err := integrationPool.QueryRow(ctx, `
					SELECT count(*) FROM message_events
					WHERE organization_id = $1 AND campaign_id = $2 AND delivery_job_id = $3
				`, organizationID, campaignID, jobID).Scan(&eventCount); err != nil {
					t.Fatal(err)
				}
				if eventCount != 1 {
					t.Fatalf("replayed webhook created %d message events, want 1", eventCount)
				}
			}
		})
	}

	campaignID, _, foreignJobID := seedResendWebhookJob(t, otherOrganizationID, "cross-workspace")
	body := resendTestBody(t, "email.bounced", organizationID, campaignID, "message-cross-workspace", "cross-workspace@example.test", "Permanent")
	resp := postSignedResendEvent(t, server.URL, secret, "evt_"+uuid.NewString(), body, time.Now())
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cross-workspace webhook status = %d, want 204", resp.StatusCode)
	}
	var foreignJobStatus string
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM delivery_jobs WHERE id = $1`, foreignJobID).Scan(&foreignJobStatus); err != nil {
		t.Fatal(err)
	}
	if foreignJobStatus != "submitted" {
		t.Fatalf("cross-workspace webhook changed foreign job status to %q", foreignJobStatus)
	}

	body = []byte(`{"type":"email.delivered","data":{}}`)
	bad := postResendEvent(t, server.URL, "evt_bad_signature", body, time.Now(), "v1,bad")
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want 401", bad.StatusCode)
	}
	stale := postSignedResendEvent(t, server.URL, secret, "evt_stale_signature", body, time.Now().Add(-6*time.Minute))
	stale.Body.Close()
	if stale.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale signature status = %d, want 401", stale.StatusCode)
	}
}

func TestResendWebhookPausesCampaignAboveRollingBounceLimit(t *testing.T) {
	if integrationPool == nil {
		t.Skip("set TEST_DATABASE_URL or INTEGRATION_DATABASE_URL to run webhook integration tests")
	}
	ctx := context.Background()
	organizationID, campaignID := uuid.NewString(), uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug) VALUES ($1, 'Resend threshold test', $2)
	`, organizationID, "resend-threshold-"+organizationID[:8]); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = integrationPool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, organizationID)
	}()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, status, recipient_count)
		VALUES ($1, $2, 'Rolling bounce threshold', 'sending', 50)
	`, campaignID, organizationID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		contactID, jobID := uuid.NewString(), uuid.NewString()
		email := fmt.Sprintf("threshold-%02d@example.test", i)
		messageID := fmt.Sprintf("threshold-message-%02d", i)
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO contacts (id, organization_id, email, status)
			VALUES ($1, $2, $3, 'subscribed')
		`, contactID, organizationID, email); err != nil {
			t.Fatal(err)
		}
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
			VALUES ($1, $2, $3, $4, 'submitted')
		`, organizationID, campaignID, contactID, email); err != nil {
			t.Fatal(err)
		}
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO delivery_jobs (id, organization_id, campaign_id, contact_id, status, message_id)
			VALUES ($1, $2, $3, $4, 'accepted', $5)
		`, jobID, organizationID, campaignID, contactID, messageID); err != nil {
			t.Fatal(err)
		}
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO message_events (organization_id, campaign_id, contact_id, delivery_job_id, event_type)
			VALUES ($1, $2, $3, $4, 'accepted')
		`, organizationID, campaignID, contactID, jobID); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()
	secret := integrationCfg.ResendWebhookSecret
	sendBounce := func(index int) {
		t.Helper()
		email := fmt.Sprintf("threshold-%02d@example.test", index)
		body := resendTestBody(t, "email.bounced", organizationID, campaignID,
			fmt.Sprintf("threshold-message-%02d", index), email, "Permanent")
		response := postSignedResendEvent(t, server.URL, secret, "evt_"+uuid.NewString(), body, time.Now())
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("bounce webhook status = %d, want 204", response.StatusCode)
		}
	}
	sendBounce(0)
	var status string
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, campaignID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "sending" {
		t.Fatalf("campaign status at exactly 2%% bounces = %q, want sending", status)
	}
	sendBounce(1)
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, campaignID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "paused" {
		t.Fatalf("campaign status above 2%% bounces = %q, want paused", status)
	}
	var alertCount int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM campaign_alerts WHERE organization_id = $1 AND campaign_id = $2 AND alert_type = 'reputation_pause'
	`, organizationID, campaignID).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 1 {
		t.Fatalf("reputation pause alerts = %d, want 1", alertCount)
	}

	complaintCampaignID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, status, recipient_count)
		VALUES ($1, $2, 'Rolling complaint threshold', 'sending', 2001)
	`, complaintCampaignID, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationPool.Exec(ctx, `
		WITH new_contacts AS (
			INSERT INTO contacts (organization_id, email, status)
			SELECT $1, 'complaint-' || lpad(i::text, 4, '0') || '@example.test', 'subscribed'
			FROM generate_series(0, 2000) AS i
			RETURNING id, email
		), new_recipients AS (
			INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
			SELECT $1, $2, id, email, 'submitted' FROM new_contacts
			RETURNING contact_id, email
		), new_jobs AS (
			INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, status, message_id)
			SELECT $1, $2, contact_id, 'accepted',
			       replace(split_part(email, '@', 1), 'complaint-', 'complaint-message-')
			FROM new_recipients
			RETURNING id, contact_id
		)
		INSERT INTO message_events (organization_id, campaign_id, contact_id, delivery_job_id, event_type)
		SELECT $1, $2, contact_id, id, 'accepted' FROM new_jobs
	`, organizationID, complaintCampaignID); err != nil {
		t.Fatal(err)
	}
	sendComplaint := func(index int) {
		t.Helper()
		email := fmt.Sprintf("complaint-%04d@example.test", index)
		body := resendTestBody(t, "email.complained", organizationID, complaintCampaignID,
			fmt.Sprintf("complaint-message-%04d", index), email, "")
		response := postSignedResendEvent(t, server.URL, secret, "evt_"+uuid.NewString(), body, time.Now())
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("complaint webhook status = %d, want 204", response.StatusCode)
		}
	}
	sendComplaint(0)
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, complaintCampaignID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "sending" {
		t.Fatalf("campaign status below 0.05%% complaints = %q, want sending", status)
	}
	sendComplaint(1)
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, complaintCampaignID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "paused" {
		t.Fatalf("campaign status above 0.05%% complaints = %q, want paused", status)
	}
}

func seedResendWebhookJob(t *testing.T, organizationID, suffix string) (campaignID, contactID, jobID string) {
	t.Helper()
	ctx := context.Background()
	campaignID, contactID, jobID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	email := suffix + "@example.test"
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO contacts (id, organization_id, email, status)
		VALUES ($1, $2, $3, 'subscribed')
	`, contactID, organizationID, email); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, status, recipient_count)
		VALUES ($1, $2, $3, 'sending', 1)
	`, campaignID, organizationID, "Resend webhook "+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
		VALUES ($1, $2, $3, $4, 'submitted')
	`, organizationID, campaignID, contactID, email); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_jobs (id, organization_id, campaign_id, contact_id, status, message_id)
		VALUES ($1, $2, $3, $4, 'submitted', $5)
	`, jobID, organizationID, campaignID, contactID, "message-"+suffix); err != nil {
		t.Fatal(err)
	}
	return campaignID, contactID, jobID
}

func resendTestBody(t *testing.T, eventType, workspaceID, campaignID, messageID, email, bounceType string) []byte {
	t.Helper()
	data := map[string]any{
		"email_id": "re_" + uuid.NewString(),
		"to":       []string{email},
		"tags": map[string]string{
			"workspace_id": workspaceID,
			"campaign_id":  campaignID,
			"message_id":   messageID,
		},
	}
	if bounceType != "" {
		data["bounce"] = map[string]string{"type": bounceType}
	}
	body, err := json.Marshal(map[string]any{"type": eventType, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func postSignedResendEvent(t *testing.T, endpoint, secret, eventID string, body []byte, timestamp time.Time) *http.Response {
	t.Helper()
	unixTimestamp := strconv.FormatInt(timestamp.Unix(), 10)
	signature := signResendTestPayload(t, secret, eventID, unixTimestamp, body)
	return postResendEvent(t, endpoint, eventID, body, timestamp, signature)
}

func postResendEvent(t *testing.T, endpoint, eventID string, body []byte, timestamp time.Time, signature string) *http.Response {
	t.Helper()
	unixTimestamp := strconv.FormatInt(timestamp.Unix(), 10)
	req, err := http.NewRequest(http.MethodPost, endpoint+"/api/v1/webhooks/resend", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("svix-id", eventID)
	req.Header.Set("svix-timestamp", unixTimestamp)
	req.Header.Set("svix-signature", signature)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func signResendTestPayload(t *testing.T, secret, eventID, timestamp string, body []byte) string {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(secret[len("whsec_"):])
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = fmt.Fprintf(mac, "%s.%s.", eventID, timestamp)
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
