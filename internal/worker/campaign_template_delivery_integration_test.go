package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	regBody := fmt.Sprintf(`{"email":"%s","password":"p5-password","name":"P5 Owner","organization_name":"P5 Org"}`, ownerEmail)
	regResp := doAPIRequest(t, server.Handler(), "POST", "/api/v1/auth/register", "", regBody)
	if regResp.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d; body: %s", regResp.Code, http.StatusCreated, regResp.Body.String())
	}
	var userID, organizationID string
	if err := pool.QueryRow(context.Background(), `SELECT u.id, om.organization_id FROM users u JOIN organization_members om ON om.user_id = u.id WHERE u.email = $1 LIMIT 1`, ownerEmail).Scan(&userID, &organizationID); err != nil {
		t.Fatalf("lookup new user org membership: %v", err)
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
	campaignBody := fmt.Sprintf(`{"name":"P5 delivery proof","subject":"Hello {{first_name}}","preview_text":"P5 proof","sender_name":"Sendwise","sender_email":"hello@example.com","list_id":"%s","template_id":"%s","content":%s}`,
		list.ID, template.ID, content)
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

	waitForMailpitMessage(t, func(message mailpitMessage) bool {
		return strings.Contains(message.Subject, "Hello Ada") || strings.Contains(message.Snippet, "live proof message")
	})
}

type mailpitMessage struct {
	Subject string `json:"Subject"`
	Snippet string `json:"Snippet"`
	From    struct {
		Address string `json:"Address"`
	} `json:"From"`
	To []struct {
		Address string `json:"Address"`
	} `json:"To"`
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

func waitForMailpitMessage(t *testing.T, match func(mailpitMessage) bool) {
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
						return
					}
				}
			}
			resp.Body.Close()
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("Mailpit did not receive the expected campaign delivery message")
}
