package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/services/gateway"
)

var integrationPool *pgxpool.Pool
var integrationTokens *auth.Manager
var integrationCfg config.Config

func TestMain(m *testing.M) {
	databaseURL := os.Getenv("INTEGRATION_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("TEST_DATABASE_URL")
	}
	var pool *pgxpool.Pool
	if databaseURL != "" {
		var err error
		pool, err = pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			_, _ = os.Stderr.WriteString("connect INTEGRATION_DATABASE_URL: " + err.Error() + "\n")
			os.Exit(1)
		}
		if err := pool.Ping(context.Background()); err != nil {
			_, _ = os.Stderr.WriteString("ping INTEGRATION_DATABASE_URL: " + err.Error() + "\n")
			pool.Close()
			os.Exit(1)
		}
		migrationDir := os.Getenv("MIGRATIONS_DIR")
		if migrationDir == "" {
			migrationDir = filepath.Join("..", "..", "migrations")
		}
		if err := db.Migrate(context.Background(), databaseURL, migrationDir); err != nil {
			_, _ = os.Stderr.WriteString("migrate integration database: " + err.Error() + "\n")
			pool.Close()
			os.Exit(1)
		}
		integrationPool = pool
		integrationCfg = config.Config{
			Env:                   "test",
			JWTSecret:             strings.Repeat("x", 32),
			JWTIssuer:             "email-platform-test",
			AccessTokenTTL:        15 * time.Minute,
			RequireVerifiedDomain: false,
			SMTPFrom:              "test@example.com",
			DKIMEncryptionKey:     "integration-dkim-encryption-key-0123456789",
		}
		integrationTokens = auth.NewManager(integrationCfg.JWTSecret, integrationCfg.JWTIssuer, integrationCfg.AccessTokenTTL)
	}

	exitCode := m.Run()
	if pool != nil {
		pool.Close()
	}
	os.Exit(exitCode)
}

func newIntegrationServer(t *testing.T) *Server {
	t.Helper()
	logger := observability.NewLogger("test", "api")
	return New(integrationCfg, integrationPool, integrationTokens, nil, auth.NewAPIKeyService(integrationPool, logger), nil, logger)
}

func integrationToken(t *testing.T, orgID, role string) string {
	t.Helper()
	token, err := integrationTokens.IssueAccess("11111111-1111-4111-8111-111111111111", orgID, role)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func TestLegacyActorSelfHealsThroughGateway(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	userID := uuid.NewString()
	organizationID := uuid.NewString()
	token, err := integrationTokens.IssueAccess(userID, organizationID, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue identity access token: %v", err)
	}
	apiServer := httptest.NewServer(newIntegrationServer(t).Handler())
	defer apiServer.Close()
	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer identityServer.Close()
	workspaceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer workspaceServer.Close()

	gatewayHandler, err := gateway.New(gateway.Config{
		BaseConfig: kit.BaseConfig{
			Env: "test", JWTSecret: integrationCfg.JWTSecret, JWTIssuer: integrationCfg.JWTIssuer,
		},
		IdentityURL:  identityServer.URL,
		WorkspaceURL: workspaceServer.URL,
		LegacyAPIURL: apiServer.URL,
	}, observability.NewLogger("test", "gateway"))
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()

	request := func(method, path, contentType string, body io.Reader) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, gatewayServer.URL+path, body)
		if err != nil {
			t.Fatalf("build %s request: %v", method, err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	resp := request(http.MethodGet, "/api/v1/lists", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET lists status = %d, want 200", resp.StatusCode)
	}

	contactBody := bytes.NewBufferString(`{"email":"self-heal@example.test","first_name":"Self","status":"pending"}`)
	resp = request(http.MethodPost, "/api/v1/contacts", "application/json", contactBody)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST contacts status = %d, want 201", resp.StatusCode)
	}

	var importBody bytes.Buffer
	writer := multipart.NewWriter(&importBody)
	file, err := writer.CreateFormFile("file", "self-heal.csv")
	if err != nil {
		t.Fatalf("create CSV field: %v", err)
	}
	if _, err := file.Write([]byte("Email,First Name\nself-heal-import@example.test,Import\n")); err != nil {
		t.Fatalf("write CSV: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart form: %v", err)
	}
	resp = request(http.MethodPost, "/api/v1/contacts/import", writer.FormDataContentType(), &importBody)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST contacts/import status = %d, want 202", resp.StatusCode)
	}

	var projectedUsers, projectedOrganizations, projectedMemberships int
	if err := integrationPool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE id = $1`, userID).Scan(&projectedUsers); err != nil {
		t.Fatalf("count projected user: %v", err)
	}
	if err := integrationPool.QueryRow(context.Background(), `SELECT count(*) FROM organizations WHERE id = $1`, organizationID).Scan(&projectedOrganizations); err != nil {
		t.Fatalf("count projected organization: %v", err)
	}
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM organization_members WHERE organization_id = $1 AND user_id = $2
	`, organizationID, userID).Scan(&projectedMemberships); err != nil {
		t.Fatalf("count projected membership: %v", err)
	}
	if projectedUsers != 1 || projectedOrganizations != 1 || projectedMemberships != 1 {
		t.Fatalf("actor projection counts = users:%d organizations:%d memberships:%d",
			projectedUsers, projectedOrganizations, projectedMemberships)
	}
}

func doRequest(t *testing.T, s *Server, method, path string, body string, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func assertStatus(t *testing.T, w *httptest.ResponseRecorder, expected ...int) {
	t.Helper()
	found := false
	for _, e := range expected {
		if w.Code == e {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected status %v, got %d; body: %s", expected, w.Code, w.Body.String())
	}
}

func registerIntegrationUser(t *testing.T, s *Server, email, password, name, organizationName string) authResponse {
	t.Helper()
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		t.Fatalf("invalid integration email %q", email)
	}
	email = parts[0] + "-" + uuid.NewString() + "@" + parts[1]
	organizationName += " " + uuid.NewString()

	body, err := json.Marshal(registerInput{
		Email: email, Password: password, Name: name, OrganizationName: organizationName,
	})
	if err != nil {
		t.Fatalf("encode registration request: %v", err)
	}
	w := doRequest(t, s, http.MethodPost, "/api/v1/auth/register", string(body), "")
	assertStatus(t, w, http.StatusCreated)
	var registered struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if registered.Email != email {
		t.Fatalf("registration email = %q, want %q", registered.Email, email)
	}

	var verificationToken string
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT token FROM verification_tokens
		WHERE email = $1 AND purpose = 'account_verification'
		ORDER BY created_at DESC
		LIMIT 1
	`, email).Scan(&verificationToken); err != nil {
		t.Fatalf("load registration verification token: %v", err)
	}
	w = doRequest(t, s, http.MethodGet, "/api/v1/auth/verify?token="+verificationToken, "", "")
	assertStatus(t, w, http.StatusOK)

	body, err = json.Marshal(loginInput{Email: email, Password: password})
	if err != nil {
		t.Fatalf("encode login request: %v", err)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/auth/login", string(body), "")
	assertStatus(t, w, http.StatusOK)
	var authenticated authResponse
	if err := json.Unmarshal(w.Body.Bytes(), &authenticated); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if authenticated.AccessToken == "" || authenticated.User.Email != email ||
		authenticated.Organization.Name != organizationName || authenticated.Organization.Role != "owner" {
		t.Fatalf("unexpected login response: %+v", authenticated)
	}
	return authenticated
}

func assertJSONBody(t *testing.T, w *httptest.ResponseRecorder, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v; body: %s", err, w.Body.String())
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("key %q not found in body: %s", key, w.Body.String())
	}
	return string(v)
}
func TestAuthRegisterLoginMeIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	orgName := "Integration Test Org"
	registered := registerIntegrationUser(
		t, s, "integration@example.com", "integration-password", "Integration User", orgName,
	)
	if registered.User.Name != "Integration User" {
		t.Fatalf("expected user name Integration User, got %s", registered.User.Name)
	}

	w := doRequest(t, s, "GET", "/api/v1/auth/me", "", registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var meResp struct {
		User         userSummary         `json:"user"`
		Organization organizationSummary `json:"organization"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("me response invalid JSON: %v", err)
	}
	if meResp.Organization.ID != registered.Organization.ID {
		t.Fatalf("org ID mismatch")
	}
}

func TestContactsCRUDIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	registered := registerIntegrationUser(
		t, s, "contacts-owner@example.com", "contacts-password", "Contacts Owner", "Contacts Org",
	)
	token := registered.AccessToken

	createBody := `{"email":"alice@example.com","first_name":"Alice","last_name":"Smith","status":"subscribed","source":"integration_test","custom_fields":{"city":"Portland"},"consent":{"source":"integration_test","purpose":"marketing","evidence":{"form":"integration_test","confirmed":true}}}`
	w := doRequest(t, s, "POST", "/api/v1/contacts", createBody, token)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID     string `json:"id"`
		Email  string `json:"email"`
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Email != "alice@example.com" {
		t.Fatalf("expected alice@example.com, got %s", created.Email)
	}
	contactID := created.ID

	w = doRequest(t, s, "GET", "/api/v1/contacts?limit=10&offset=0", "", token)
	assertStatus(t, w, http.StatusOK)
	var listResp struct {
		Data []contact `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(listResp.Data))
	}

	w = doRequest(t, s, "GET", "/api/v1/contacts/"+contactID, "", token)
	assertStatus(t, w, http.StatusOK)

	updateBody := `{"first_name":"Alicia","status":"subscribed","consent":{"source":"integration_test","purpose":"marketing","evidence":{"form":"integration_test","confirmed":true}}}`
	w = doRequest(t, s, "PATCH", "/api/v1/contacts/"+contactID, updateBody, token)
	assertStatus(t, w, http.StatusOK)
	var updated contact
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.FirstName != "Alicia" {
		t.Fatalf("expected Alicia, got %s", updated.FirstName)
	}

	w = doRequest(t, s, "DELETE", "/api/v1/contacts/"+contactID, "", token)
	assertStatus(t, w, http.StatusNoContent)

	w = doRequest(t, s, "GET", "/api/v1/contacts/"+contactID, "", token)
	assertStatus(t, w, http.StatusNotFound)
}

func TestCampaignsLifecycleIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	registered := registerIntegrationUser(
		t, s, "campaigns-owner@example.com", "campaigns-password", "Campaigns Owner", "Campaigns Org",
	)
	token := registered.AccessToken

	createBody := `{
		"name":"Integration Campaign",
		"subject":"Hello from integration test",
		"sender_name":"Test Sender",
		"sender_email":"sender@example.com",
		"track_opens":true,
		"track_clicks":true,
		"content":{"version":1,"blocks":[{"type":"heading","content":"Hello {{first_name}}"},{"type":"paragraph","content":"Welcome!"}]}
	}`
	w := doRequest(t, s, "POST", "/api/v1/campaigns", createBody, token)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Name   string `json:"name"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Status != "draft" {
		t.Fatalf("expected draft, got %s", created.Status)
	}
	campaignID := created.ID

	w = doRequest(t, s, "GET", "/api/v1/campaigns/"+campaignID, "", token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "GET", "/api/v1/campaigns?limit=10", "", token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/validate", "", token)
	assertStatus(t, w, http.StatusOK)
	var validation struct {
		Checks []validationCheck `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &validation); err != nil {
		t.Fatalf("decode campaign validation: %v", err)
	}
	var postalAddressRequired bool
	for _, check := range validation.Checks {
		if check.Name == "postal_address" && check.Status == "error" {
			postalAddressRequired = true
		}
	}
	if !postalAddressRequired {
		t.Fatal("campaign validation did not require a sender postal address")
	}

	scheduleBody, err := json.Marshal(scheduleInput{ScheduledAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("encode campaign schedule: %v", err)
	}
	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/schedule", string(scheduleBody), token)
	assertStatus(t, w, http.StatusBadRequest)

	updateBody := `{"name":"Updated Integration Campaign","sender_postal_address":"1 Integration Way"}`
	w = doRequest(t, s, "PATCH", "/api/v1/campaigns/"+campaignID, updateBody, token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/schedule", string(scheduleBody), token)
	assertStatus(t, w, http.StatusOK, http.StatusAccepted)

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/cancel", "", token)
	assertStatus(t, w, http.StatusOK)
}

func TestReputationThresholdPausesCampaignIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	owner := registerIntegrationUser(t, s, "reputation-owner@example.com", "reputation-password", "Reputation Owner", "Reputation Org")
	ctx := context.Background()
	var campaignID, contactID string
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, created_by, name, status, sender_email)
		VALUES ($1, $2, 'Reputation threshold proof', 'sending', 'hello@example.com')
		RETURNING id
	`, owner.Organization.ID, owner.User.ID).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'reputation-bounce@example.test', 'subscribed')
		RETURNING id
	`, owner.Organization.ID).Scan(&contactID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
		VALUES
			($1, $2, $3, 'reputation-bounce@example.test', 'submitted'),
			($1, $2, gen_random_uuid(), 'reputation-good@example.test', 'pending')
	`, owner.Organization.ID, campaignID, contactID); err != nil {
		t.Fatalf("create campaign recipient snapshot: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, gen_random_uuid()::text || '@test', 'submitted')
	`, owner.Organization.ID, campaignID, contactID); err != nil {
		t.Fatalf("create submitted delivery job: %v", err)
	}

	handler := &DeliveryHandler{
		pool: integrationPool, logger: testLogger(), maxBounceRate: 0.25, maxComplaintRate: 0.9,
	}
	if err := handler.recordReputationSignal(ctx, owner.Organization.ID, "reputation-bounce@example.test", campaignID, "bounced", "550 mailbox unavailable"); err != nil {
		t.Fatalf("record bounce feedback: %v", err)
	}
	var status string
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, campaignID).Scan(&status); err != nil {
		t.Fatalf("load campaign status: %v", err)
	}
	if status != "paused" {
		t.Fatalf("campaign status = %q, want paused", status)
	}
	var alerts int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM campaign_alerts WHERE campaign_id = $1 AND alert_type = 'reputation_pause'
	`, campaignID).Scan(&alerts); err != nil {
		t.Fatalf("count campaign alerts: %v", err)
	}
	if alerts != 1 {
		t.Fatalf("reputation alerts = %d, want 1", alerts)
	}

	var complaintCampaignID, complaintContactID string
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, created_by, name, status, sender_email)
		VALUES ($1, $2, 'Complaint threshold proof', 'sending', 'hello@example.com')
		RETURNING id
	`, owner.Organization.ID, owner.User.ID).Scan(&complaintCampaignID); err != nil {
		t.Fatalf("create complaint campaign: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'reputation-complaint@example.test', 'subscribed')
		RETURNING id
	`, owner.Organization.ID).Scan(&complaintContactID); err != nil {
		t.Fatalf("create complaint contact: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
		VALUES
			($1, $2, $3, 'reputation-complaint@example.test', 'submitted'),
			($1, $2, gen_random_uuid(), 'complaint-good@example.test', 'pending')
	`, owner.Organization.ID, complaintCampaignID, complaintContactID); err != nil {
		t.Fatalf("create complaint recipient snapshot: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, $3, gen_random_uuid()::text || '@test', 'accepted')
	`, owner.Organization.ID, complaintCampaignID, complaintContactID); err != nil {
		t.Fatalf("create complaint delivery job: %v", err)
	}
	domain := "feedback-" + uuid.NewString() + ".example.test"
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (organization_id, domain)
		VALUES ($1, $2)
	`, owner.Organization.ID, domain); err != nil {
		t.Fatalf("create complaint webhook sending domain: %v", err)
	}
	handler.maxBounceRate, handler.maxComplaintRate = 0.9, 0.25
	body, err := json.Marshal(complaintWebhook{
		Email: "reputation-complaint@example.test", Domain: domain,
		CampaignID: complaintCampaignID, FeedbackType: "spam",
	})
	if err != nil {
		t.Fatalf("encode complaint webhook: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.complaintWebhook(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("complaint webhook status = %d, want 204; body: %s", response.Code, response.Body.String())
	}
	var suppressionCount int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM suppressions
		WHERE organization_id = $1 AND lower(email) = 'reputation-complaint@example.test'
	`, owner.Organization.ID).Scan(&suppressionCount); err != nil {
		t.Fatalf("count complaint suppression: %v", err)
	}
	if suppressionCount != 1 {
		t.Fatalf("complaint suppression count = %d, want 1", suppressionCount)
	}
	var contactStatus string
	if err := integrationPool.QueryRow(ctx, `
		SELECT status FROM contacts WHERE id = $1
	`, complaintContactID).Scan(&contactStatus); err != nil {
		t.Fatalf("load complaint contact status: %v", err)
	}
	if contactStatus != "suppressed" {
		t.Fatalf("complaint contact status = %q, want suppressed", contactStatus)
	}
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, complaintCampaignID).Scan(&status); err != nil {
		t.Fatalf("load complaint campaign status: %v", err)
	}
	if status != "paused" {
		t.Fatalf("complaint campaign status = %q, want paused", status)
	}
}

func TestDomainsIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	suffix := time.Now().Format("20060102150405")
	var orgID string
	if err := integrationPool.QueryRow(context.Background(), `
		INSERT INTO organizations (name, slug)
		VALUES ($1, 'domains-test-' || gen_random_uuid()::text)
		RETURNING id
	`, "Domains Org "+suffix).Scan(&orgID); err != nil {
		t.Fatalf("create sending domains organization: %v", err)
	}
	token := integrationToken(t, orgID, "owner")

	nullKeyDomain := "null-key-smoke-" + suffix + ".example.com"
	emptyKeyDomain := "empty-key-smoke-" + suffix + ".example.com"
	verifiedDomain := "verified-smoke-" + suffix + ".example.com"
	_, err := integrationPool.Exec(context.Background(), `
		INSERT INTO sending_domains (
			organization_id, domain, status, dkim_selector, dkim_public_key,
			spf_status, dkim_status, dmarc_status, verification_status, last_checked_at
		) VALUES
			($1, $2, 'pending', 'mail', NULL, 'pending', 'missing', 'mismatched', 'pending', NULL),
			($1, $3, 'misconfigured', 'mail', '', 'missing', 'mismatched', 'verified', 'missing', now()),
			($1, $4, 'verified', 'mail', 'public-key', 'verified', 'verified', 'verified', 'verified', NULL)
	`, orgID, nullKeyDomain, emptyKeyDomain, verifiedDomain)
	if err != nil {
		t.Fatalf("insert sending domain shape rows: %v", err)
	}

	w := doRequest(t, s, "GET", "/api/v1/domains", "", token)
	assertStatus(t, w, http.StatusOK)
	var listResp struct {
		Data []struct {
			Domain             string     `json:"domain"`
			Status             string     `json:"status"`
			DKIMPublicKey      string     `json:"dkim_public_key"`
			SPFStatus          string     `json:"spf_status"`
			DKIMStatus         string     `json:"dkim_status"`
			DMARCStatus        string     `json:"dmarc_status"`
			VerificationStatus string     `json:"verification_status"`
			LastCheckedAt      *time.Time `json:"last_checked_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode domain list: %v", err)
	}
	foundNullKey := false
	foundEmptyKey := false
	foundMisconfigured := false
	foundVerified := false
	for _, d := range listResp.Data {
		if d.Domain == nullKeyDomain {
			foundNullKey = true
			if d.DKIMPublicKey != "" || d.VerificationStatus != "pending" || d.LastCheckedAt != nil ||
				d.SPFStatus != "pending" || d.DKIMStatus != "missing" || d.DMARCStatus != "mismatched" {
				t.Fatalf("unexpected NULL-key row: %+v", d)
			}
		}
		if d.Domain == emptyKeyDomain {
			foundEmptyKey = d.DKIMPublicKey == "" && d.LastCheckedAt != nil &&
				d.SPFStatus == "missing" && d.DKIMStatus == "mismatched" && d.DMARCStatus == "verified"
			foundMisconfigured = d.Status == "misconfigured"
		}
		if d.Domain == verifiedDomain {
			foundVerified = d.Status == "verified" && d.VerificationStatus == "verified" &&
				d.LastCheckedAt == nil && d.SPFStatus == "verified" &&
				d.DKIMStatus == "verified" && d.DMARCStatus == "verified"
		}
	}
	if !foundNullKey {
		t.Fatalf("expected %s in domain list", nullKeyDomain)
	}
	if !foundEmptyKey || !foundMisconfigured || !foundVerified {
		t.Fatalf("expected empty-key/misconfigured and verified rows in domain list; got %+v", listResp.Data)
	}

	w = doRequest(t, s, "GET", "/api/v1/domains/"+mustDomainID(t, orgID, nullKeyDomain)+"/dns-records", "", token)
	assertStatus(t, w, http.StatusOK)
	var nullKeyRecords struct {
		DNSRecords []dnsRecord `json:"dns_records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &nullKeyRecords); err != nil {
		t.Fatalf("decode NULL-key DNS records: %v", err)
	}
	if len(nullKeyRecords.DNSRecords) != 4 || nullKeyRecords.DNSRecords[1].Value != "" {
		t.Fatalf("expected four records and no DKIM placeholder for NULL key, got %+v", nullKeyRecords.DNSRecords)
	}

	createBody := `{"domain":"newsletter-` + suffix + `.example.com"}`
	w = doRequest(t, s, "POST", "/api/v1/domains", createBody, token)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID            string `json:"id"`
		Domain        string `json:"domain"`
		Status        string `json:"status"`
		DKIMSelector  string `json:"dkim_selector"`
		DKIMPublicKey string `json:"dkim_public_key"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Domain != "newsletter-"+suffix+".example.com" {
		t.Fatalf("expected newsletter-%s.example.com, got %s", suffix, created.Domain)
	}
	if created.Status != "pending" {
		t.Fatalf("expected pending status, got %s", created.Status)
	}
	if created.DKIMPublicKey == "" {
		t.Fatalf("expected server-generated DKIM public key, got empty value")
	}
	domainID := created.ID
	var encryptedPrivateKey string
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT dkim_private_key FROM sending_domains WHERE id = $1 AND organization_id = $2
	`, domainID, orgID).Scan(&encryptedPrivateKey); err != nil {
		t.Fatalf("read generated encrypted private key: %v", err)
	}
	if encryptedPrivateKey == "" || strings.Contains(encryptedPrivateKey, "PRIVATE KEY") {
		t.Fatal("expected generated DKIM private key to be encrypted at rest")
	}

	w = doRequest(t, s, "GET", "/api/v1/domains/"+domainID+"/dns-records", "", token)
	assertStatus(t, w, http.StatusOK)
	var recordsResp struct {
		Domain     sendingDomain `json:"domain"`
		DNSRecords []dnsRecord   `json:"dns_records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recordsResp); err != nil {
		t.Fatalf("decode dns-records response: %v; body: %s", err, w.Body.String())
	}
	if len(recordsResp.DNSRecords) != 4 {
		t.Fatalf("expected 4 dns records, got %d: %s", len(recordsResp.DNSRecords), w.Body.String())
	}
	verificationRecord := recordsResp.DNSRecords[3]
	if verificationRecord.Type != "TXT" || !strings.HasPrefix(verificationRecord.Name, "_sendwise-verification.") {
		t.Fatalf("expected verification TXT record, got %+v", verificationRecord)
	}
	if !strings.Contains(recordsResp.DNSRecords[1].Value, created.DKIMPublicKey) {
		t.Fatalf("DKIM record does not contain generated public key: %+v", recordsResp.DNSRecords[1])
	}

	s.dnsResolver = &fakeDNSResolver{records: map[string][]string{
		created.Domain: {"v=spf1 mx ~all"},
	}}
	w = doRequest(t, s, "POST", "/api/v1/domains/"+domainID+"/verify", "", token)
	assertStatus(t, w, http.StatusOK)
	var partialResp struct {
		Domain     sendingDomain `json:"domain"`
		DNSRecords []dnsRecord   `json:"dns_records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &partialResp); err != nil {
		t.Fatalf("decode partial DNS verification: %v", err)
	}
	if partialResp.Domain.Status != "misconfigured" || partialResp.Domain.SPFStatus != "verified" ||
		partialResp.Domain.DKIMStatus != "missing" || partialResp.Domain.VerificationStatus != "missing" {
		t.Fatalf("unexpected partial DNS result: %+v", partialResp.Domain)
	}

	s.dnsResolver = &fakeDNSResolver{records: map[string][]string{
		created.Domain: {"v=spf1 mx ~all"},
		created.DKIMSelector + "._domainkey." + created.Domain: {dkimRecordValue(created.DKIMPublicKey)},
		"_dmarc." + created.Domain:                             {"v=DMARC1; p=quarantine"},
		"_sendwise-verification." + created.Domain:             {"v=sendwise-verification " + created.Domain},
	}}
	w = doRequest(t, s, "POST", "/api/v1/domains/"+domainID+"/verify", "", token)
	assertStatus(t, w, http.StatusOK)
	var verifiedResp struct {
		Domain     sendingDomain `json:"domain"`
		DNSRecords []dnsRecord   `json:"dns_records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &verifiedResp); err != nil {
		t.Fatalf("decode full DNS verification: %v", err)
	}
	if verifiedResp.Domain.Status != "verified" || verifiedResp.Domain.VerificationStatus != "verified" {
		t.Fatalf("expected fully verified domain, got %+v", verifiedResp.Domain)
	}
	if len(verifiedResp.DNSRecords) != 4 {
		t.Fatalf("expected four verified DNS records, got %+v", verifiedResp.DNSRecords)
	}
	if verifiedResp.DNSRecords[3].Status != "verified" {
		t.Fatalf("verification TXT record status = %q, want verified", verifiedResp.DNSRecords[3].Status)
	}

	// Cross-tenant isolation: another organization cannot verify this domain.
	var orgID2 string
	if err := integrationPool.QueryRow(context.Background(), `
		INSERT INTO organizations (name, slug)
		VALUES ($1, 'domains-test-' || gen_random_uuid()::text)
		RETURNING id
	`, "Domains Org Two "+suffix).Scan(&orgID2); err != nil {
		t.Fatalf("create second sending domains organization: %v", err)
	}
	token2 := integrationToken(t, orgID2, "owner")
	w = doRequest(t, s, "POST", "/api/v1/domains/"+domainID+"/verify", "", token2)
	assertStatus(t, w, http.StatusNotFound)
	w = doRequest(t, s, "GET", "/api/v1/domains/"+domainID+"/dns-records", "", token2)
	assertStatus(t, w, http.StatusNotFound)
}

func mustDomainID(t *testing.T, orgID, domain string) string {
	t.Helper()
	var id string
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT id FROM sending_domains WHERE organization_id = $1 AND domain = $2
	`, orgID, domain).Scan(&id); err != nil {
		t.Fatalf("look up sending domain ID: %v", err)
	}
	return id
}

func TestAnalyticsOverviewIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	registered := registerIntegrationUser(
		t, s, "analytics-owner@example.com", "analytics-password", "Analytics Owner", "Analytics Org",
	)
	token := registered.AccessToken

	w := doRequest(t, s, "GET", "/api/v1/analytics/overview", "", token)
	assertStatus(t, w, http.StatusOK)
	var overview struct {
		Contacts   int64   `json:"contacts"`
		Subscribed int64   `json:"subscribed"`
		OpenRate   float64 `json:"open_rate"`
		ClickRate  float64 `json:"click_rate"`
		BounceRate float64 `json:"bounce_rate"`
	}
	json.Unmarshal(w.Body.Bytes(), &overview)
}

func TestAPIKeyLifecycleIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	registered := registerIntegrationUser(
		t, s, "api-key-owner@example.test", "api-key-integration-password", "API Key Owner", "API Key Workspace",
	)

	w := doRequest(t, s, http.MethodPost, "/api/v1/api-keys", `{"name":"integration key"}`, registered.AccessToken)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID     string   `json:"id"`
		Plain  string   `json:"plain_key"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode API key creation response: %v", err)
	}
	if created.ID == "" || created.Plain == "" {
		t.Fatalf("creation response missing key ID or one-time key: %#v", created)
	}

	apiKeys := auth.NewAPIKeyService(integrationPool, observability.NewLogger("test", "api-key"))
	organizationID, scopes, err := apiKeys.Authenticate(context.Background(), created.Plain)
	if err != nil {
		t.Fatalf("authenticate created API key: %v", err)
	}
	if organizationID == "" || len(scopes) == 0 {
		t.Fatalf("unexpected API key claims: organization=%q scopes=%v", organizationID, scopes)
	}

	w = doRequest(t, s, http.MethodGet, "/api/v1/api-keys", "", registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), created.Plain) || strings.Contains(w.Body.String(), "plain_key") {
		t.Fatal("API key listing exposed one-time key material")
	}

	w = doRequest(t, s, http.MethodDelete, "/api/v1/api-keys/"+created.ID, "", registered.AccessToken)
	assertStatus(t, w, http.StatusNoContent)
	if _, _, err := apiKeys.Authenticate(context.Background(), created.Plain); err == nil {
		t.Fatal("revoked API key still authenticated")
	}
}
