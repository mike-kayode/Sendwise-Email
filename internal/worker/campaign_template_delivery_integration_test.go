package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/jackc/pgx/v5/pgxpool"
	apipkg "github.com/khayode/email-platform/internal/api"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/observability"
)

func TestCampaignTemplateDeliveryIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping database: %v", err)
	}
	if err := db.Migrate(context.Background(), databaseURL, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	logger := observability.NewLogger("test", "worker")
	cfg := config.Config{
		Env:                    "test",
		APIBaseURL:             "http://localhost:8080",
		TrackingBaseURL:        "http://localhost:8080",
		JWTSecret:              strings.Repeat("x", 32),
		JWTIssuer:              "email-platform-test",
		AccessTokenTTL:         15 * time.Minute,
		SMTPAddr:               "127.0.0.1:1025",
		SMTPHost:               "mailpit",
		SMTPFrom:               "hello@example.com",
		DKIMEncryptionKey:      "campaign-dkim-encryption-key-for-tests",
		SendingRate:            20,
		MaxDeliveryAttempts:    3,
		MaxMessageAge:          48 * time.Hour,
		DomainConcurrencyLimit: 10,
		RequireVerifiedDomain:  false,
	}
	service := campaigns.NewService(pool, nil, logger, 3, false)
	tokens := auth.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)
	server := apipkg.New(cfg, pool, tokens, service, auth.NewAPIKeyService(pool, logger), nil, logger)

	ownerEmail := fmt.Sprintf("p5-owner-%d@example.com", time.Now().UnixNano())
	contactEmail := fmt.Sprintf("p5-recipient-%d@example.com", time.Now().UnixNano())
	campaignDomain := fmt.Sprintf("campaign-%d.example.test", time.Now().UnixNano())
	campaignSender := "hello@" + campaignDomain
	regBody := fmt.Sprintf(`{"email":"%s","password":"p5-password","name":"P5 Owner","organization_name":"P5 Org"}`, ownerEmail)
	regResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/auth/register", "", regBody)
	if regResp.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d; body: %s", regResp.Code, http.StatusCreated, regResp.Body.String())
	}
	var userID, organizationID string
	if err := pool.QueryRow(context.Background(), `SELECT u.id, om.organization_id FROM users u JOIN organization_members om ON om.user_id = u.id WHERE u.email = $1 LIMIT 1`, ownerEmail).Scan(&userID, &organizationID); err != nil {
		t.Fatalf("lookup new user org membership: %v", err)
	}
	publicKey, encryptedPrivateKey, err := delivery.GenerateDKIMKeyPair(cfg.DKIMEncryptionKey)
	if err != nil {
		t.Fatalf("generate campaign dkim key: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO sending_domains (organization_id, domain, status, dkim_selector, dkim_public_key, dkim_private_key)
		VALUES ($1, $2, 'verified', 'campaign-test', $3, $4)
	`, organizationID, campaignDomain, publicKey, encryptedPrivateKey); err != nil {
		t.Fatalf("create verified sending domain: %v", err)
	}
	accessToken, err := tokens.IssueAccess(userID, organizationID, "owner")
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}

	listResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/lists", accessToken, `{"name":"P5 delivery list","description":"Campaign proof list"}`)
	if listResp.Code != http.StatusCreated {
		t.Fatalf("list create status = %d, want %d; body: %s", listResp.Code, http.StatusCreated, listResp.Body.String())
	}
	var list struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil {
		t.Fatalf("list JSON invalid: %v; body: %s", err, listResp.Body.String())
	}

	contactResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/contacts", accessToken, fmt.Sprintf(`{"email":"%s","first_name":"Ada","last_name":"Lovelace","status":"subscribed","source":"campaign-proof","consent":{"source":"campaign-proof","purpose":"marketing","evidence":{"form":"campaign-proof","confirmed":true}}}`, contactEmail))
	if contactResp.Code != http.StatusCreated {
		t.Fatalf("contact create status = %d, want %d; body: %s", contactResp.Code, http.StatusCreated, contactResp.Body.String())
	}
	var contact struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(contactResp.Body.Bytes(), &contact); err != nil {
		t.Fatalf("contact JSON invalid: %v; body: %s", err, contactResp.Body.String())
	}

	if _, err := pool.Exec(context.Background(), `INSERT INTO contact_list_members (organization_id, contact_id, list_id, source) VALUES ($1, $2, $3, 'campaign-proof') ON CONFLICT DO NOTHING`, organizationID, contact.ID, list.ID); err != nil {
		t.Fatalf("add contact to list: %v", err)
	}

	templateResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/templates", accessToken, `{"name":"P5 Template","category":"newsletter","design":{"version":1,"blocks":[{"type":"heading","content":"Hi {{first_name}}","align":"left"},{"type":"paragraph","content":"This is a live proof message with unsubscribe: {{unsubscribe_url}}","align":"left"}]}}`)
	if templateResp.Code != http.StatusCreated {
		t.Fatalf("template create status = %d, want %d; body: %s", templateResp.Code, http.StatusCreated, templateResp.Body.String())
	}
	var template struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(templateResp.Body.Bytes(), &template); err != nil {
		t.Fatalf("template JSON invalid: %v; body: %s", err, templateResp.Body.String())
	}

	content := `{"version":1,"blocks":[{"type":"heading","content":"Hi {{first_name}}","align":"left"},{"type":"paragraph","content":"This is a live proof message with unsubscribe: {{unsubscribe_url}}","align":"left"}]}`
	subjectToken := fmt.Sprintf("p5-%d", time.Now().UnixNano())
	campaignBody := fmt.Sprintf(`{"name":"P5 delivery proof","subject":"%s {{first_name}}","preview_text":"P5 proof","sender_name":"Sendwise","sender_email":"%s","sender_postal_address":"1 Example Street, Example City","list_id":"%s","template_id":"%s","content":%s}`,
		subjectToken, campaignSender, list.ID, template.ID, content)
	campaignResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/campaigns", accessToken, campaignBody)
	if campaignResp.Code != http.StatusCreated {
		t.Fatalf("campaign create status = %d, want %d; body: %s", campaignResp.Code, http.StatusCreated, campaignResp.Body.String())
	}
	var campaign struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(campaignResp.Body.Bytes(), &campaign); err != nil {
		t.Fatalf("campaign JSON invalid: %v; body: %s", err, campaignResp.Body.String())
	}

	sendResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/campaigns/"+campaign.ID+"/send", accessToken, "")
	if sendResp.Code != http.StatusAccepted {
		t.Fatalf("campaign send status = %d, want %d; body: %s", sendResp.Code, http.StatusAccepted, sendResp.Body.String())
	}

	var jobID string
	if err := pool.QueryRow(context.Background(), `SELECT id FROM delivery_jobs WHERE campaign_id = $1 LIMIT 1`, campaign.ID).Scan(&jobID); err != nil {
		t.Fatalf("fetch delivery job: %v", err)
	}

	worker := New(cfg, pool, nil, delivery.NewSMTPTransport(cfg.SMTPAddr, cfg.SMTPHost, "", "", true), tokens, service, logger)
	if retryAfter, err := worker.process(context.Background(), campaigns.JobPayload{JobID: jobID, OrganizationID: organizationID, CampaignID: campaign.ID, ContactID: contact.ID}); err != nil {
		t.Fatalf("worker.process() error = %v, retryAfter=%s", err, retryAfter)
	}

	received := waitForMailpitMessage(t, func(message mailpitMessage) bool {
		return strings.Contains(message.Subject, subjectToken)
	})
	rawURL := "http://127.0.0.1:8025/api/v1/message/" + url.PathEscape(received.ID) + "/raw"
	rawResponse, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("fetch raw Mailpit message: %v", err)
	}
	defer rawResponse.Body.Close()
	if rawResponse.StatusCode != http.StatusOK {
		t.Fatalf("raw Mailpit message status = %d, want %d", rawResponse.StatusCode, http.StatusOK)
	}
	rawMessage, err := io.ReadAll(rawResponse.Body)
	if err != nil {
		t.Fatalf("read raw Mailpit message: %v", err)
	}
	rawText := string(rawMessage)
	for _, proof := range []string{
		"List-Unsubscribe:",
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click",
		"1 Example Street, Example City",
		"Unsubscribe",
	} {
		if !strings.Contains(rawText, proof) {
			t.Errorf("delivered message does not contain required compliance content %q", proof)
		}
	}
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(rawMessage), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			expectedName := "campaign-test._domainkey." + campaignDomain
			if name != expectedName {
				return nil, fmt.Errorf("unexpected dkim lookup name %q", name)
			}
			return []string{"v=DKIM1; k=rsa; p=" + publicKey}, nil
		},
	})
	if err != nil {
		t.Fatalf("verify delivered message dkim signature: %v", err)
	}
	if len(verifications) != 1 {
		t.Fatalf("delivered message has %d dkim signatures, want exactly 1", len(verifications))
	}
	if verifications[0].Err != nil {
		t.Fatalf("delivered message dkim verification failed: %v", verifications[0].Err)
	}
	t.Logf("verified DKIM signature on Mailpit message %s against %s._domainkey.%s",
		received.ID, "campaign-test", campaignDomain)
}

func TestSuppressedAddressIsNeverSentIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, databaseURL, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	var organizationID, campaignID, contactID, jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Suppression integration', 'suppression-' || gen_random_uuid()::text)
		RETURNING id
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID); err != nil {
			t.Errorf("clean up suppression fixture: %v", err)
		}
	}()
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'suppressed-recipient@example.test', 'subscribed')
		RETURNING id
	`, organizationID).Scan(&contactID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (
			organization_id, name, subject, sender_name, sender_email,
			sender_postal_address, content, status
		)
		VALUES ($1, 'Suppression proof', 'Suppressed', 'Sendwise', 'hello@example.com',
			'1 Example Street', '{"version":1,"blocks":[]}'::jsonb, 'sending')
		RETURNING id
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email)
		VALUES ($1, $2, $3, 'suppressed-recipient@example.test')
	`, organizationID, campaignID, contactID); err != nil {
		t.Fatalf("create recipient snapshot: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, gen_random_uuid()::text || '@suppression.test', 'queued')
		RETURNING id
	`, organizationID, campaignID, contactID).Scan(&jobID); err != nil {
		t.Fatalf("create delivery job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, 'suppressed-recipient@example.test', 'complaint', 'integration_test')
	`, organizationID); err != nil {
		t.Fatalf("suppress recipient: %v", err)
	}
	worker := New(config.Config{}, pool, nil, nil, nil, nil, observability.NewLogger("test", "suppression"))
	if retryAfter, err := worker.process(ctx, campaigns.JobPayload{
		JobID: jobID, OrganizationID: organizationID, CampaignID: campaignID, ContactID: contactID,
	}); err != nil || retryAfter > 0 {
		t.Fatalf("process suppressed job retryAfter=%s err=%v; want terminal cancellation", retryAfter, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM delivery_jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("load suppressed job status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("suppressed job status = %q, want cancelled", status)
	}

	var bounceContactID, bounceCampaignID, bounceJobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'hard-bounce@example.test', 'subscribed')
		RETURNING id
	`, organizationID).Scan(&bounceContactID); err != nil {
		t.Fatalf("create hard-bounce contact: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (
			organization_id, name, subject, sender_name, sender_email,
			sender_postal_address, content, status
		)
		VALUES ($1, 'Hard bounce proof', 'Hard bounce', 'Sendwise', 'hello@example.com',
			'1 Example Street', '{"version":1,"blocks":[]}'::jsonb, 'sending')
		RETURNING id
	`, organizationID).Scan(&bounceCampaignID); err != nil {
		t.Fatalf("create hard-bounce campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email)
		VALUES ($1, $2, $3, 'hard-bounce@example.test')
	`, organizationID, bounceCampaignID, bounceContactID); err != nil {
		t.Fatalf("create hard-bounce recipient: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, gen_random_uuid()::text || '@hard-bounce.test', 'processing')
		RETURNING id
	`, organizationID, bounceCampaignID, bounceContactID).Scan(&bounceJobID); err != nil {
		t.Fatalf("create hard-bounce delivery job: %v", err)
	}
	if retryAfter, err := worker.handleSendFailure(ctx, &jobRow{
		ID: bounceJobID, OrganizationID: organizationID, CampaignID: bounceCampaignID,
		ContactID: bounceContactID, Email: "hard-bounce@example.test", MaxAttempts: 3,
	}, errors.New("550 5.1.1 user unknown")); err != nil || retryAfter > 0 {
		t.Fatalf("handle hard bounce retryAfter=%s err=%v; want terminal bounce", retryAfter, err)
	}
	var suppressionCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM suppressions
		WHERE organization_id = $1 AND lower(email) = 'hard-bounce@example.test'
	`, organizationID).Scan(&suppressionCount); err != nil {
		t.Fatalf("count hard-bounce suppression: %v", err)
	}
	if suppressionCount != 1 {
		t.Fatalf("hard-bounce suppression count = %d, want 1", suppressionCount)
	}
}

type mailpitMessage struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
	Snippet string `json:"Snippet"`
	From    struct {
		Address string `json:"Address"`
	} `json:"From"`
	To []struct {
		Address string `json:"Address"`
	} `json:"To"`
}

func TestWarmupReservationIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, databaseURL, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	var organizationID, campaignID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Warm-up integration', 'warmup-' || gen_random_uuid()::text)
		RETURNING id
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, status, sender_email)
		VALUES ($1, 'Warm-up integration', 'sending', 'hello@warmup.example')
		RETURNING id
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	var jobs [2]jobRow
	for i := range jobs {
		if err := pool.QueryRow(ctx, `
			INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
			VALUES ($1, $2, gen_random_uuid(), gen_random_uuid()::text || '@warmup.test', 'queued')
			RETURNING id, contact_id
		`, organizationID, campaignID).Scan(&jobs[i].ID, &jobs[i].ContactID); err != nil {
			t.Fatalf("create delivery job %d: %v", i, err)
		}
		jobs[i].OrganizationID = organizationID
		jobs[i].CampaignID = campaignID
		jobs[i].SenderEmail = "hello@warmup.example"
	}

	worker := New(config.Config{
		SendingRate: 10, WarmupStartPerDay: 1, WarmupGrowthFactor: 1.5, MaxDailyEmails: 10,
	}, pool, nil, nil, nil, nil, observability.NewLogger("test", "worker"))
	deferred, err := worker.reserveWarmupSlot(ctx, &jobs[0])
	if err != nil || deferred {
		t.Fatalf("first warm-up reservation = deferred:%v err:%v; want reserved", deferred, err)
	}
	deferred, err = worker.reserveWarmupSlot(ctx, &jobs[1])
	if err != nil || !deferred {
		t.Fatalf("second warm-up reservation = deferred:%v err:%v; want deferred", deferred, err)
	}

	var reserved int
	if err := pool.QueryRow(ctx, `
		SELECT reserved_count FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND domain = 'warmup.example' AND sending_day = CURRENT_DATE
	`, organizationID).Scan(&reserved); err != nil {
		t.Fatalf("load reserved warm-up slots: %v", err)
	}
	if reserved != 1 {
		t.Fatalf("reserved warm-up slots = %d, want 1", reserved)
	}
}

func doAPIRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func waitForMailpitMessage(t *testing.T, match func(mailpitMessage) bool) mailpitMessage {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:8025/api/v1/messages")
		if err == nil && resp.StatusCode == http.StatusOK {
			var payload struct {
				Messages []mailpitMessage `json:"messages"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
				for _, message := range payload.Messages {
					if match(message) {
						resp.Body.Close()
						return message
					}
				}
			}
			resp.Body.Close()
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("Mailpit did not receive the expected campaign delivery message")
	return mailpitMessage{}
}
