package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/observability"
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
	token, err := integrationTokens.IssueAccess("test-user", orgID, role)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
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
	regBody := `{"email":"integration@example.com","password":"integration-password","name":"Integration User","organization_name":"` + orgName + `"}`
	w := doRequest(t, s, "POST", "/api/v1/auth/register", regBody, "")
	assertStatus(t, w, http.StatusCreated)

	var regResp struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
		Organization struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &regResp); err != nil {
		t.Fatalf("register response invalid JSON: %v", err)
	}
	if regResp.User.Email != "integration@example.com" {
		t.Fatalf("expected email integration@example.com, got %s", regResp.User.Email)
	}
	if regResp.Organization.Name != orgName {
		t.Fatalf("expected org %s, got %s", orgName, regResp.Organization.Name)
	}
	if regResp.Organization.Role != "owner" {
		t.Fatalf("expected role owner, got %s", regResp.Organization.Role)
	}

	token := regResp.AccessToken

	loginBody := `{"email":"integration@example.com","password":"integration-password"}`
	w = doRequest(t, s, "POST", "/api/v1/auth/login", loginBody, "")
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "GET", "/api/v1/auth/me", "", token)
	assertStatus(t, w, http.StatusOK)
	var meResp struct {
		User         userSummary         `json:"user"`
		Organization organizationSummary `json:"organization"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("me response invalid JSON: %v", err)
	}
	if meResp.Organization.ID != regResp.Organization.ID {
		t.Fatalf("org ID mismatch")
	}
}

func TestContactsCRUDIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	regBody := `{"email":"contacts-owner@example.com","password":"contacts-password","name":"Contacts Owner","organization_name":"Contacts Org"}`
	w := doRequest(t, s, "POST", "/api/v1/auth/register", regBody, "")
	assertStatus(t, w, http.StatusCreated)
	var regResp struct {
		AccessToken  string `json:"access_token"`
		Organization struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	json.Unmarshal(w.Body.Bytes(), &regResp)
	token := regResp.AccessToken

	createBody := `{"email":"alice@example.com","first_name":"Alice","last_name":"Smith","status":"subscribed","source":"integration_test","custom_fields":{"city":"Portland"},"consent":{"source":"integration_test","purpose":"marketing","evidence":{"form":"integration_test","confirmed":true}}}`
	w = doRequest(t, s, "POST", "/api/v1/contacts", createBody, token)
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

	regBody := `{"email":"campaigns-owner@example.com","password":"campaigns-password","name":"Campaigns Owner","organization_name":"Campaigns Org"}`
	w := doRequest(t, s, "POST", "/api/v1/auth/register", regBody, "")
	assertStatus(t, w, http.StatusCreated)
	var regResp struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &regResp)
	token := regResp.AccessToken

	createBody := `{
		"name":"Integration Campaign",
		"subject":"Hello from integration test",
		"sender_name":"Test Sender",
		"sender_email":"sender@example.com",
		"track_opens":true,
		"track_clicks":true,
		"content":{"version":1,"blocks":[{"type":"heading","content":"Hello {{first_name}}"},{"type":"paragraph","content":"Welcome!"}]}
	}`
	w = doRequest(t, s, "POST", "/api/v1/campaigns", createBody, token)
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

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/schedule", "", token)
	assertStatus(t, w, http.StatusOK, http.StatusAccepted)

	updateBody := `{"name":"Updated Integration Campaign"}`
	w = doRequest(t, s, "PATCH", "/api/v1/campaigns/"+campaignID, updateBody, token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/cancel", "", token)
	assertStatus(t, w, http.StatusOK)
}

func TestDomainsIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	regBody := `{"email":"domains-owner@example.com","password":"domains-password","name":"Domains Owner","organization_name":"Domains Org"}`
	w := doRequest(t, s, "POST", "/api/v1/auth/register", regBody, "")
	assertStatus(t, w, http.StatusCreated)
	var regResp struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &regResp)
	token := regResp.AccessToken

	createBody := `{"domain":"newsletter.example.com"}`
	w = doRequest(t, s, "POST", "/api/v1/domains", createBody, token)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.Domain != "newsletter.example.com" {
		t.Fatalf("expected newsletter.example.com, got %s", created.Domain)
	}
	domainID := created.ID

	w = doRequest(t, s, "GET", "/api/v1/domains", "", token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "GET", "/api/v1/domains/"+domainID+"/dns-records", "", token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "POST", "/api/v1/domains/"+domainID+"/verify", "", token)
	assertStatus(t, w, http.StatusOK)
}

func TestAnalyticsOverviewIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	regBody := `{"email":"analytics-owner@example.com","password":"analytics-password","name":"Analytics Owner","organization_name":"Analytics Org"}`
	w := doRequest(t, s, "POST", "/api/v1/auth/register", regBody, "")
	assertStatus(t, w, http.StatusCreated)
	var regResp struct {
		AccessToken string `json:"access_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &regResp)
	token := regResp.AccessToken

	w = doRequest(t, s, "GET", "/api/v1/analytics/overview", "", token)
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
	registration := `{"email":"api-key-owner@example.test","password":"api-key-integration-password","name":"API Key Owner","organization_name":"API Key Workspace"}`
	w := doRequest(t, s, http.MethodPost, "/api/v1/auth/register", registration, "")
	assertStatus(t, w, http.StatusCreated)
	var registered struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/api-keys", `{"name":"integration key"}`, registered.AccessToken)
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
