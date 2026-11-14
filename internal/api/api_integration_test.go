package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/quota"
	"github.com/khayode/email-platform/internal/transactional"
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
			ServiceToken:          "integration-service-token",
			SMTPFrom:              "test@example.com",
			BounceDomain:          "bounces.example.test",
			WebhookSecret:         strings.Repeat("w", 32),
			MaxBounceRate:         0.02,
			MaxComplaintRate:      0.0005,
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
	server := New(integrationCfg, integrationPool, integrationTokens, nil, auth.NewAPIKeyService(integrationPool, logger), nil, logger)
	server.stalwartDomainHoster = stalwartDomainProvisionFunc(func(context.Context, string) error {
		return nil
	})
	return server
}

func TestLegacyRegistrationAndPasswordRecoveryRoutesAreDisabled(t *testing.T) {
	server := newIntegrationServer(t)
	for _, path := range []string{
		"/api/v1/auth/register",
		"/api/v1/auth/forgot-password",
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusGone {
			t.Errorf("POST %s status = %d, want %d; body: %s", path, response.Code, http.StatusGone, response.Body)
		}
	}
}

func integrationToken(t *testing.T, orgID, role string) string {
	t.Helper()
	token, err := integrationTokens.IssueAccess("11111111-1111-4111-8111-111111111111", orgID, role)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func seedVerifiedSender(t *testing.T, organizationID, localPart string) (string, string) {
	t.Helper()
	ctx := context.Background()
	domain := "sender-" + strings.ReplaceAll(uuid.NewString(), "-", "") + ".example.test"
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (
			organization_id, domain, status, dkim_selector, spf_status,
			dkim_status, dmarc_status, verification_status, stalwart_authenticated
		) VALUES ($1, $2, 'verified', 'mail', 'verified', 'verified', 'missing', 'verified', true)
	`, organizationID, domain); err != nil {
		t.Fatalf("seed verified sending domain: %v", err)
	}
	var identityID string
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO sender_identities (
			organization_id, domain, local_part, display_name, reply_to, is_default
		) VALUES ($1, $2, $3, 'Integration Sender', 'support@example.test', true)
		RETURNING id
	`, organizationID, domain, localPart).Scan(&identityID); err != nil {
		t.Fatalf("seed sender identity: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sender_profiles (
			organization_id, company_name, street_address, city, region, postal_code,
			country, website, support_email, social_links
		) VALUES ($1, 'Integration Company', '1 Test Street', 'Test City', 'Test Region',
		          '10000', 'Test Country', 'https://example.test', 'support@example.test', '[]'::jsonb)
	`, organizationID); err != nil {
		t.Fatalf("seed sender profile: %v", err)
	}
	return identityID, localPart + "@" + domain
}

func TestInternalAdminDomainDirectoryRequiresServiceTokenAndReturnsMetadataOnly(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	domainID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Admin Directory Workspace', $2)
	`, organizationID, "admin-domain-"+strings.ReplaceAll(organizationID, "-", "")); err != nil {
		t.Fatalf("insert admin domain workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (
			id, organization_id, domain, status, dkim_public_key, verification_token,
			spf_status, dkim_status, dmarc_status, verification_status, dns_check_details
		) VALUES ($1, $2, 'admin-directory.example.test', 'pending', 'public-key-value',
		          'private-verification-token', 'verified', 'pending', 'unknown', 'pending',
		          '{"records":{"spf":{"resolvers":[{"resolver":"1.1.1.1","status":"found"}]}}}'::jsonb)
	`, domainID, organizationID); err != nil {
		t.Fatalf("insert admin domain: %v", err)
	}
	apiServer := newIntegrationServer(t)
	apiServer.dnsResolver = &fakeDNSResolver{records: map[string][]string{}}
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/internal/admin/domains?search=admin-directory&limit=1", nil)
	if err != nil {
		t.Fatalf("build unauthenticated admin domain request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated admin domain request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin domain status = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/internal/admin/domains?search=admin-directory&limit=1", nil)
	if err != nil {
		t.Fatalf("build admin domain request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send admin domain request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read admin domain response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin domain status = %d, want 200: %s", response.StatusCode, body)
	}
	for _, privateValue := range []string{"private-verification-token", "verification_token", "dkim_public_key", "public-key-value"} {
		if strings.Contains(string(body), privateValue) {
			t.Errorf("admin domain response contains excluded key material %q: %s", privateValue, body)
		}
	}
	var result struct {
		Data       []adminSendingDomain `json:"data"`
		Pagination struct {
			Total  int `json:"total"`
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode admin domain response: %v", err)
	}
	if len(result.Data) != 1 || result.Data[0].ID != domainID ||
		result.Data[0].WorkspaceID != organizationID ||
		result.Data[0].WorkspaceName != "Admin Directory Workspace" ||
		result.Data[0].Domain != "admin-directory.example.test" ||
		result.Pagination.Total < 1 || result.Pagination.Limit != 1 {
		t.Fatalf("admin domain result = %+v; want matching, paginated workspace/domain metadata", result)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/internal/admin/domains/"+domainID, nil)
	if err != nil {
		t.Fatalf("build admin domain details request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send admin domain details request: %v", err)
	}
	defer response.Body.Close()
	detailBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read admin domain details response: %v", err)
	}
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(string(detailBody), `"resolver":"1.1.1.1"`) ||
		strings.Contains(string(detailBody), "private-verification-token") ||
		strings.Contains(string(detailBody), "public-key-value") {
		t.Fatalf("admin domain details response = %d %s; want resolver metadata without keys", response.StatusCode, detailBody)
	}

	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/domains/"+domainID+"/verify", nil)
	if err != nil {
		t.Fatalf("build unauthenticated domain re-check request: %v", err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated domain re-check request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated domain re-check status = %d, want 401", response.StatusCode)
	}
	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/domains/"+domainID+"/verify", nil)
	if err != nil {
		t.Fatalf("build domain re-check request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send domain re-check request: %v", err)
	}
	recheckBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read domain re-check response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(recheckBody), `"last_checked_at"`) {
		t.Fatalf("domain re-check status = %d; body %s", response.StatusCode, recheckBody)
	}
	var checkedAt *time.Time
	if err := integrationPool.QueryRow(ctx, `SELECT last_checked_at FROM sending_domains WHERE id = $1`, domainID).Scan(&checkedAt); err != nil {
		t.Fatalf("read persisted domain re-check time: %v", err)
	}
	if checkedAt == nil {
		t.Fatal("domain re-check did not persist last_checked_at")
	}
	if _, err := integrationPool.Exec(ctx, `UPDATE sending_domains SET owner_conflict = true WHERE id = $1`, domainID); err != nil {
		t.Fatalf("mark domain owner conflict: %v", err)
	}
	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/domains/"+domainID+"/verify", nil)
	if err != nil {
		t.Fatalf("build conflicting domain re-check request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send conflicting domain re-check request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("owner-conflict domain re-check status = %d, want 409", response.StatusCode)
	}
	var checkedAfterConflict *time.Time
	if err := integrationPool.QueryRow(ctx, `SELECT last_checked_at FROM sending_domains WHERE id = $1`, domainID).Scan(&checkedAfterConflict); err != nil {
		t.Fatalf("read domain re-check time after owner-conflict rejection: %v", err)
	}
	if checkedAfterConflict == nil || !checkedAfterConflict.Equal(*checkedAt) {
		t.Fatalf("owner-conflict re-check changed last_checked_at: before %v, after %v", checkedAt, checkedAfterConflict)
	}
}

func TestInternalAdminDomainQuotaUpdateChangesAtomicEnforcement(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID, domainID, adminID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	domain := "quota-admin-" + strings.ReplaceAll(domainID, "-", "") + ".example.test"
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Quota admin workspace', $2)
	`, organizationID, "quota-admin-"+strings.ReplaceAll(organizationID, "-", "")); err != nil {
		t.Fatalf("insert quota admin workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (id, organization_id, domain, status)
		VALUES ($1, $2, $3, 'verified')
	`, domainID, organizationID, domain); err != nil {
		t.Fatalf("insert quota admin domain: %v", err)
	}

	apiServer := newIntegrationServer(t)
	apiServer.cfg.PlatformMaxDomainRate = 1000
	handler := apiServer.Handler()
	doInternal := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if authenticated {
			kit.WithServiceToken(request, integrationCfg.ServiceToken)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	unauthorized := doInternal(http.MethodGet, "/internal/admin/domains/"+domainID+"/quotas", "", false)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated domain quota read = %d, want 401", unauthorized.Code)
	}
	payload := fmt.Sprintf(`{
		"updated_by":%q,"reason":"Approved domain quota change","streams":[
			{"stream":"marketing","daily_limit":2,"monthly_limit":10,"per_second_limit":100,"unlimited":false},
			{"stream":"transactional","daily_limit":2,"monthly_limit":10,"per_second_limit":100,"unlimited":false}
		]
	}`, adminID)
	updated := doInternal(http.MethodPut, "/internal/admin/domains/"+domainID+"/quotas", payload, true)
	if updated.Code != http.StatusOK {
		t.Fatalf("update domain quotas = %d, want 200: %s", updated.Code, updated.Body.String())
	}
	var result struct {
		Data struct {
			Streams map[string]quota.Snapshot `json:"streams"`
		} `json:"data"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode updated domain quotas: %v", err)
	}
	if result.Data.Streams[quota.StreamTransactional].DailyLimit == nil ||
		*result.Data.Streams[quota.StreamTransactional].DailyLimit != 2 ||
		result.Data.Streams[quota.StreamTransactional].DailyOverride == nil ||
		*result.Data.Streams[quota.StreamTransactional].DailyOverride != 2 {
		t.Fatalf("updated transactional quota snapshot = %+v", result.Data.Streams[quota.StreamTransactional])
	}

	for i := 0; i < 2; i++ {
		tx, err := integrationPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin accepted quota reservation: %v", err)
		}
		if _, err := quota.Reserve(ctx, tx, domainID, quota.StreamTransactional,
			quota.SourceTransactionalRecipient, uuid.NewString(), 1, 1000); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("reserve message %d under updated quota: %v", i+1, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit accepted quota reservation: %v", err)
		}
	}
	tx, err := integrationPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin quota-exceeded reservation: %v", err)
	}
	_, reserveErr := quota.Reserve(ctx, tx, domainID, quota.StreamTransactional,
		quota.SourceTransactionalRecipient, uuid.NewString(), 1, 1000)
	_ = tx.Rollback(ctx)
	var exceeded *quota.ExceededError
	if !errors.As(reserveErr, &exceeded) || exceeded.Window != quota.WindowDaily {
		t.Fatalf("reservation after reaching updated limit = %v; want daily quota exceeded", reserveErr)
	}

	nearLimit := doInternal(http.MethodGet, "/internal/admin/quotas/near-limit", "", true)
	if nearLimit.Code != http.StatusOK || !strings.Contains(nearLimit.Body.String(), domainID) {
		t.Fatalf("near-limit results = %d, want updated domain: %s", nearLimit.Code, nearLimit.Body.String())
	}
}

func TestInternalAdminDomainStatusAndRemovalRequireServiceTokenAndBlockSending(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	domainID := uuid.NewString()
	slug := "admin-domain-disable-" + strings.ReplaceAll(organizationID, "-", "")
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug) VALUES ($1, 'Domain Disable Workspace', $2)
	`, organizationID, slug); err != nil {
		t.Fatalf("insert domain-disable workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (id, organization_id, domain, status)
		VALUES ($1, $2, 'disable-me.example.test', 'verified')
	`, domainID, organizationID); err != nil {
		t.Fatalf("insert verified sending domain: %v", err)
	}
	senderID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sender_identities (id, organization_id, domain, local_part, display_name, is_default)
		VALUES ($1, $2, 'disable-me.example.test', 'support', 'Support', true)
	`, senderID, organizationID); err != nil {
		t.Fatalf("insert sending identity: %v", err)
	}
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodPatch, server.URL+"/internal/admin/domains/"+domainID+"/disabled",
		strings.NewReader(`{"disabled":true}`))
	if err != nil {
		t.Fatalf("build unauthenticated domain-disable request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated domain-disable request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated domain-disable status = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPatch, server.URL+"/internal/admin/domains/"+domainID+"/disabled",
		strings.NewReader(`{"disabled":true}`))
	if err != nil {
		t.Fatalf("build authorized domain-disable request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send authorized domain-disable request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read domain-disable response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("domain-disable status = %d, want 200: %s", response.StatusCode, body)
	}
	var result struct {
		Data adminSendingDomain `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode domain-disable response: %v", err)
	}
	if result.Data.AdminDisabledAt == nil {
		t.Fatal("domain-disable response has no disabled timestamp")
	}
	tx, err := integrationPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin domain send-guard transaction: %v", err)
	}
	if err := requireVerifiedOwnedDomain(ctx, tx, organizationID, "disable-me.example.test"); !errors.Is(err, errSenderDomainDisabled) {
		t.Fatalf("verified-domain guard = %v, want disabled-domain rejection", err)
	}
	_ = tx.Rollback(ctx)

	request, err = http.NewRequest(http.MethodPatch, server.URL+"/internal/admin/domains/"+domainID+"/disabled",
		strings.NewReader(`{"disabled":false}`))
	if err != nil {
		t.Fatalf("build domain-enable request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send domain-enable request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("domain-enable status = %d, want 200", response.StatusCode)
	}
	tx, err = integrationPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin enabled-domain verification transaction: %v", err)
	}
	if err := requireVerifiedOwnedDomain(ctx, tx, organizationID, "disable-me.example.test"); err != nil {
		t.Fatalf("verified-domain guard after enable = %v, want success", err)
	}
	_ = tx.Rollback(ctx)

	conflictDomainID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (id, organization_id, domain, status, owner_conflict)
		VALUES ($1, $2, 'conflicted.example.test', 'verified', true)
	`, conflictDomainID, organizationID); err != nil {
		t.Fatalf("insert owner-conflicted domain: %v", err)
	}
	request, err = http.NewRequest(http.MethodDelete, server.URL+"/internal/admin/domains/"+conflictDomainID, nil)
	if err != nil {
		t.Fatalf("build owner-conflicted domain removal request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send owner-conflicted domain removal request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("owner-conflicted domain removal status = %d, want 409", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodDelete, server.URL+"/internal/admin/domains/"+domainID, nil)
	if err != nil {
		t.Fatalf("build unauthenticated domain removal request: %v", err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated domain removal request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated domain removal status = %d, want 401", response.StatusCode)
	}
	request, err = http.NewRequest(http.MethodDelete, server.URL+"/internal/admin/domains/"+domainID, nil)
	if err != nil {
		t.Fatalf("build authorized domain removal request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send authorized domain removal request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("domain removal status = %d, want 204", response.StatusCode)
	}
	var remainingDomains, remainingSenders int
	if err := integrationPool.QueryRow(ctx, `SELECT count(*) FROM sending_domains WHERE id = $1`, domainID).Scan(&remainingDomains); err != nil {
		t.Fatalf("count removed sending domain: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `SELECT count(*) FROM sender_identities WHERE id = $1`, senderID).Scan(&remainingSenders); err != nil {
		t.Fatalf("count removed sender identity: %v", err)
	}
	if remainingDomains != 0 || remainingSenders != 0 {
		t.Fatalf("removed domain records remain: domains=%d sender identities=%d", remainingDomains, remainingSenders)
	}
}

func TestInternalAdminWorkspaceStatusRequiresServiceTokenAndCanSuspendWorkspace(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	slug := "admin-workspace-status-" + strings.ReplaceAll(organizationID, "-", "")
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug, status) VALUES ($1, 'Workspace Status Test', $2, 'active')
	`, organizationID, slug); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/internal/admin/workspaces/"+organizationID+"/status", nil)
	if err != nil {
		t.Fatalf("build unauthenticated status request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated status request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status request = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPatch, server.URL+"/internal/admin/workspaces/"+organizationID+"/status",
		strings.NewReader(`{"expected_status":"active","status":"suspended"}`))
	if err != nil {
		t.Fatalf("build workspace suspension request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send workspace suspension request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read workspace suspension response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"suspended"`) {
		t.Fatalf("workspace suspension response = %d: %s", response.StatusCode, body)
	}
	var status string
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1`, organizationID).Scan(&status); err != nil {
		t.Fatalf("read workspace status: %v", err)
	}
	if status != "suspended" {
		t.Fatalf("workspace database status = %q, want suspended", status)
	}
}

func TestInternalAdminDomainConflictResolutionSelectsOneOwnerAtomically(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	firstOrganizationID, secondOrganizationID := uuid.NewString(), uuid.NewString()
	firstDomainID, secondDomainID := uuid.NewString(), uuid.NewString()
	firstSlug := "admin-conflict-first-" + strings.ReplaceAll(firstOrganizationID, "-", "")
	secondSlug := "admin-conflict-second-" + strings.ReplaceAll(secondOrganizationID, "-", "")
	for _, workspace := range []struct {
		id, name, slug string
	}{
		{firstOrganizationID, "First conflict workspace", firstSlug},
		{secondOrganizationID, "Second conflict workspace", secondSlug},
	} {
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)
		`, workspace.id, workspace.name, workspace.slug); err != nil {
			t.Fatalf("insert conflict workspace: %v", err)
		}
		defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, workspace.id)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains (id, organization_id, domain, owner_conflict)
		VALUES ($1, $2, 'shared-conflict.example.test', false),
		       ($3, $4, 'shared-conflict.example.test', true)
	`, firstDomainID, firstOrganizationID, secondDomainID, secondOrganizationID); err != nil {
		t.Fatalf("insert conflicting domains: %v", err)
	}
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()
	payload := `{"rightful_domain_id":"` + secondDomainID + `"}`
	request, err := http.NewRequest(http.MethodPost,
		server.URL+"/internal/admin/domains/"+firstDomainID+"/resolve-conflict", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("build unauthenticated conflict resolution request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated conflict resolution request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ownership resolution = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPost,
		server.URL+"/internal/admin/domains/"+firstDomainID+"/resolve-conflict", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("build authorized conflict resolution request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send authorized conflict resolution request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read ownership resolution response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ownership resolution = %d, want 200: %s", response.StatusCode, body)
	}
	var result struct {
		Data adminSendingDomain `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode ownership resolution: %v", err)
	}
	if result.Data.ID != secondDomainID || result.Data.OwnerConflict {
		t.Fatalf("resolved owner = %+v, want selected owner without conflict", result.Data)
	}
	var firstConflict, secondConflict bool
	if err := integrationPool.QueryRow(ctx, `SELECT owner_conflict FROM sending_domains WHERE id = $1`, firstDomainID).Scan(&firstConflict); err != nil {
		t.Fatalf("read former owner conflict status: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `SELECT owner_conflict FROM sending_domains WHERE id = $1`, secondDomainID).Scan(&secondConflict); err != nil {
		t.Fatalf("read selected owner conflict status: %v", err)
	}
	if !firstConflict || secondConflict {
		t.Fatalf("owner conflict flags after resolution: former owner=%t selected owner=%t", firstConflict, secondConflict)
	}
}

func TestInternalAdminCampaignDirectoryRequiresServiceTokenAndReturnsMetadataOnly(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	campaignID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Campaign Admin Workspace', $2)
	`, organizationID, "admin-campaign-"+strings.ReplaceAll(organizationID, "-", "")); err != nil {
		t.Fatalf("insert admin campaign workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaigns (
			id, organization_id, name, subject, status, recipient_count
		) VALUES ($1, $2, 'Admin campaign fixture', 'private subject text', 'completed', 4)
	`, campaignID, organizationID); err != nil {
		t.Fatalf("insert admin campaign: %v", err)
	}
	for _, status := range []string{"accepted", "bounced", "complained", "failed"} {
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, status, message_id)
			VALUES ($1, $2, $3, $4, $5)
		`, organizationID, campaignID, uuid.NewString(), status, "private-message-"+status); err != nil {
			t.Fatalf("insert admin campaign delivery %s: %v", status, err)
		}
	}
	apiServer := newIntegrationServer(t)
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/internal/admin/campaigns?search=Admin+campaign+fixture&status=completed&workspace_id="+organizationID+"&limit=1", nil)
	if err != nil {
		t.Fatalf("build unauthenticated admin campaign request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated admin campaign request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin campaign status = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/internal/admin/campaigns?search=Admin+campaign+fixture&status=completed&workspace_id="+organizationID+"&limit=1", nil)
	if err != nil {
		t.Fatalf("build admin campaign request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send admin campaign request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read admin campaign response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin campaign status = %d, want 200: %s", response.StatusCode, body)
	}
	for _, expected := range []string{
		"Admin campaign fixture", "Campaign Admin Workspace", `"recipient_count":4`,
		`"submitted_count":1`, `"bounced_count":1`, `"complaint_count":1`, `"failed_count":1`,
		`"total":1`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("admin campaign response missing %q: %s", expected, body)
		}
	}
	for _, privateValue := range []string{"private subject text", "private-message-", "email"} {
		if strings.Contains(string(body), privateValue) {
			t.Errorf("admin campaign metadata exposed %q: %s", privateValue, body)
		}
	}
	request, err = http.NewRequest(http.MethodGet,
		server.URL+"/internal/admin/campaigns?search=private-message-accepted&status=completed&workspace_id="+organizationID, nil)
	if err != nil {
		t.Fatalf("build message-ID admin campaign lookup: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send message-ID admin campaign lookup: %v", err)
	}
	messageLookupBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read message-ID admin campaign lookup: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(messageLookupBody), "Admin campaign fixture") {
		t.Fatalf("message-ID admin campaign lookup = %d, want campaign metadata: %s", response.StatusCode, messageLookupBody)
	}
	if strings.Contains(string(messageLookupBody), "private-message-accepted") {
		t.Fatalf("message-ID admin campaign lookup exposed the searched identifier: %s", messageLookupBody)
	}
}

func TestInternalAdminCampaignStopRequiresServiceTokenAndCancelsPendingWork(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	campaignID := uuid.NewString()
	contactID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Campaign Stop Workspace', $2)
	`, organizationID, "admin-stop-"+strings.ReplaceAll(organizationID, "-", "")); err != nil {
		t.Fatalf("insert admin campaign stop workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaigns (id, organization_id, name, status, recipient_count)
		VALUES ($1, $2, 'Campaign to stop', 'sending', 1)
	`, campaignID, organizationID); err != nil {
		t.Fatalf("insert campaign to stop: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, status, message_id)
		VALUES ($1, $2, $3, 'queued', 'admin-stop-message')
	`, organizationID, campaignID, contactID); err != nil {
		t.Fatalf("insert queued delivery: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
		VALUES ($1, $2, $3, 'private-recipient@example.test', 'pending')
	`, organizationID, campaignID, contactID); err != nil {
		t.Fatalf("insert pending campaign recipient: %v", err)
	}
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/internal/admin/campaigns/"+campaignID+"/stop", nil)
	if err != nil {
		t.Fatalf("build unauthenticated admin campaign stop: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated admin campaign stop: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin campaign stop = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/campaigns/"+campaignID+"/stop", nil)
	if err != nil {
		t.Fatalf("build admin campaign stop: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send admin campaign stop: %v", err)
	}
	stopBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read admin campaign stop response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(stopBody), `"status":"cancelled"`) {
		t.Fatalf("admin campaign stop = %d, want cancelled: %s", response.StatusCode, stopBody)
	}
	var campaignStatus, deliveryStatus, recipientStatus string
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, campaignID).Scan(&campaignStatus); err != nil {
		t.Fatalf("read stopped campaign status: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM delivery_jobs WHERE campaign_id = $1`, campaignID).Scan(&deliveryStatus); err != nil {
		t.Fatalf("read cancelled delivery status: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM campaign_recipients WHERE campaign_id = $1`, campaignID).Scan(&recipientStatus); err != nil {
		t.Fatalf("read skipped campaign recipient status: %v", err)
	}
	if campaignStatus != "cancelled" || deliveryStatus != "cancelled" || recipientStatus != "skipped" {
		t.Fatalf("stop states = campaign %q, delivery %q, recipient %q", campaignStatus, deliveryStatus, recipientStatus)
	}

	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/campaigns/"+campaignID+"/stop", nil)
	if err != nil {
		t.Fatalf("build repeated admin campaign stop: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send repeated admin campaign stop: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("repeated admin campaign stop = %d, want 409", response.StatusCode)
	}
}

func TestInternalAdminTransactionalMessageSearchRequiresServiceTokenAndReturnsMetadataOnly(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	messageID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Transactional Admin Workspace', $2)
	`, organizationID, "admin-transactional-"+strings.ReplaceAll(organizationID, "-", "")); err != nil {
		t.Fatalf("insert transactional search workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO transactional_messages (
			id, organization_id, message_id, status, from_email, subject, html, text_content
		) VALUES ($1, $2, 'admin-search-message@example.test', 'delivered',
		          'mailer@example.test', 'private subject', '<p>private html body</p>', 'private text body')
	`, messageID, organizationID); err != nil {
		t.Fatalf("insert transactional message: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO transactional_message_recipients (message_id, email, recipient_type)
		VALUES ($1, 'private-recipient@example.test', 'to')
	`, messageID); err != nil {
		t.Fatalf("insert transactional recipient: %v", err)
	}
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet,
		server.URL+"/internal/admin/transactional-messages?search=admin-search-message%40example.test&limit=2&offset=0", nil)
	if err != nil {
		t.Fatalf("build unauthenticated transactional search: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated transactional search: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated transactional search = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet,
		server.URL+"/internal/admin/transactional-messages?search=admin-search-message%40example.test&limit=2&offset=0", nil)
	if err != nil {
		t.Fatalf("build transactional search: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send transactional search: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read transactional search response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("transactional search = %d, want 200: %s", response.StatusCode, body)
	}
	for _, expected := range []string{
		"admin-search-message@example.test", "Transactional Admin Workspace",
		`"status":"delivered"`, `"total":1`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("transactional search missing %q: %s", expected, body)
		}
	}
	for _, privateValue := range []string{
		"private subject", "private html body", "private text body",
		"private-recipient@example.test", "mailer@example.test", `"recipients"`,
	} {
		if strings.Contains(string(body), privateValue) {
			t.Errorf("transactional admin search exposed %q: %s", privateValue, body)
		}
	}
}

func TestInternalAdminOverviewRequiresServiceTokenAndReturnsLiveMetrics(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/internal/admin/overview", nil)
	if err != nil {
		t.Fatalf("build unauthenticated admin overview request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated admin overview request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated admin overview = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodGet, server.URL+"/internal/admin/overview", nil)
	if err != nil {
		t.Fatalf("build admin overview request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send admin overview request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read admin overview response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin overview = %d, want 200: %s", response.StatusCode, body)
	}
	var result struct {
		Data adminOperationalMetrics `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode admin overview metrics: %v", err)
	}
	metrics := result.Data
	if metrics.UpdatedAt.IsZero() || metrics.DomainCount < 0 || metrics.CampaignCount < 0 ||
		metrics.MessagesToday < 0 || metrics.Messages7Days < metrics.MessagesToday ||
		metrics.Messages30Days < metrics.Messages7Days || metrics.QueueDepth < 0 {
		t.Fatalf("invalid live admin metrics: %+v", metrics)
	}
	if metrics.DeliveryRate30d < 0 || metrics.DeliveryRate30d > 100 ||
		metrics.BounceRate30d < 0 || metrics.BounceRate30d > 100 ||
		metrics.ComplaintRate30d < 0 || metrics.ComplaintRate30d > 100 {
		t.Fatalf("invalid operational rate percentages: %+v", metrics)
	}
}

func TestInternalAdminSuppressionsRequireServiceTokenAndSupportAuditedOperations(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	organizationID := uuid.NewString()
	slug := "admin-suppression-" + strings.ReplaceAll(organizationID, "-", "")
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug) VALUES ($1, 'Suppression Admin Workspace', $2)
	`, organizationID, slug); err != nil {
		t.Fatalf("insert admin suppression workspace: %v", err)
	}
	defer integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID)
	email := "admin-suppression@example.test"
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO contacts (organization_id, email, status) VALUES ($1, $2, 'subscribed')
	`, organizationID, email); err != nil {
		t.Fatalf("insert suppression contact: %v", err)
	}
	server := httptest.NewServer(newIntegrationServer(t).Handler())
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/internal/admin/suppressions", nil)
	if err != nil {
		t.Fatalf("build unauthenticated suppression request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send unauthenticated suppression request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated suppression list = %d, want 401", response.StatusCode)
	}

	payload, err := json.Marshal(map[string]string{"workspace_id": organizationID, "email": email})
	if err != nil {
		t.Fatalf("encode suppression request: %v", err)
	}
	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/suppressions", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build suppression create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send suppression create request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read suppression create response: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("suppression create = %d, want 201: %s", response.StatusCode, body)
	}
	var created struct {
		Data adminSuppression `json:"data"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created suppression: %v", err)
	}
	if created.Data.ID == "" || created.Data.WorkspaceID != organizationID ||
		created.Data.Email != email || created.Data.Source != "admin" {
		t.Fatalf("unexpected created suppression: %+v", created.Data)
	}
	request, err = http.NewRequest(http.MethodPost, server.URL+"/internal/admin/suppressions", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build duplicate suppression request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send duplicate suppression request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate suppression create = %d, want 409", response.StatusCode)
	}
	var contactStatus string
	if err := integrationPool.QueryRow(ctx, `
		SELECT status FROM contacts WHERE organization_id = $1 AND lower(email) = lower($2)
	`, organizationID, email).Scan(&contactStatus); err != nil {
		t.Fatalf("read suppressed contact status: %v", err)
	}
	if contactStatus != "subscribed" {
		t.Fatalf("admin suppression changed consent status to %q, want subscribed", contactStatus)
	}

	request, err = http.NewRequest(http.MethodGet,
		server.URL+"/internal/admin/suppressions?workspace_id="+organizationID, nil)
	if err != nil {
		t.Fatalf("build suppression list request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send suppression list request: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read suppression list response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("suppression list = %d, want 200: %s", response.StatusCode, body)
	}
	var listed struct {
		Data []adminSuppression `json:"data"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode suppression list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("suppression list = %+v, want the created record", listed.Data)
	}

	request, err = http.NewRequest(http.MethodDelete,
		server.URL+"/internal/admin/suppressions/"+created.Data.ID, nil)
	if err != nil {
		t.Fatalf("build suppression delete request: %v", err)
	}
	kit.WithServiceToken(request, integrationCfg.ServiceToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send suppression delete request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("suppression delete = %d, want 204", response.StatusCode)
	}
	var count int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM suppressions WHERE organization_id = $1 AND lower(email) = lower($2)
	`, organizationID, email).Scan(&count); err != nil {
		t.Fatalf("count deleted suppression: %v", err)
	}
	if count != 0 {
		t.Fatalf("suppression count after delete = %d, want 0", count)
	}
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

func seedIntegrationUser(t *testing.T, email, password, name, organizationName string) authResponse {
	t.Helper()
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		t.Fatalf("invalid integration email %q", email)
	}
	email = parts[0] + "-" + uuid.NewString() + "@" + parts[1]
	organizationName += " " + uuid.NewString()

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash integration password: %v", err)
	}
	tx, err := integrationPool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin integration user transaction: %v", err)
	}
	defer tx.Rollback(context.Background())

	var userID, organizationID, organizationSlug string
	if err := tx.QueryRow(context.Background(), `
		INSERT INTO users (email, password_hash, name, status, email_verified_at)
		VALUES ($1, $2, $3, 'active', now())
		RETURNING id
	`, email, passwordHash, name).Scan(&userID); err != nil {
		t.Fatalf("seed integration user: %v", err)
	}
	if err := tx.QueryRow(context.Background(), `
		INSERT INTO organizations (name, slug)
		VALUES ($1, $2)
		RETURNING id, slug
	`, organizationName, slugify(organizationName)).Scan(&organizationID, &organizationSlug); err != nil {
		t.Fatalf("seed integration organization: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, organizationID, userID); err != nil {
		t.Fatalf("seed integration organization membership: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit integration user seed: %v", err)
	}

	accessToken, err := integrationTokens.IssueAccess(userID, organizationID, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue integration access token: %v", err)
	}
	return authResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(integrationCfg.AccessTokenTTL.Seconds()),
		User:        userSummary{ID: userID, Name: name, Email: email},
		Organization: organizationSummary{
			ID: organizationID, Name: organizationName, Slug: organizationSlug, Role: auth.RoleOwner,
		},
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
func TestAuthLoginMeIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	orgName := "Integration Test Org"
	seeded := seedIntegrationUser(
		t, "integration@example.com", "integration-password", "Integration User", orgName,
	)
	loginBody, err := json.Marshal(loginInput{Email: seeded.User.Email, Password: "integration-password"})
	if err != nil {
		t.Fatalf("encode login request: %v", err)
	}
	loginResponse := doRequest(t, s, http.MethodPost, "/api/v1/auth/login", string(loginBody), "")
	assertStatus(t, loginResponse, http.StatusOK)
	var authenticated authResponse
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &authenticated); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if authenticated.AccessToken == "" || authenticated.User.Email != seeded.User.Email ||
		authenticated.Organization.Name != seeded.Organization.Name || authenticated.Organization.Role != auth.RoleOwner {
		t.Fatalf("unexpected login response: %+v", authenticated)
	}

	w := doRequest(t, s, "GET", "/api/v1/auth/me", "", authenticated.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var meResp struct {
		User         userSummary         `json:"user"`
		Organization organizationSummary `json:"organization"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meResp); err != nil {
		t.Fatalf("me response invalid JSON: %v", err)
	}
	if meResp.Organization.ID != authenticated.Organization.ID {
		t.Fatalf("org ID mismatch")
	}
}

func TestContactsCRUDIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	registered := seedIntegrationUser(
		t, "contacts-owner@example.com", "contacts-password", "Contacts Owner", "Contacts Org",
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

func TestBulkContactActionsRecordConsentAndDelete(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	registered := seedIntegrationUser(
		t, "bulk-contacts-owner@example.com", "contacts-password", "Bulk Contacts Owner", "Bulk Contacts Org",
	)
	contactIDs := make([]uuid.UUID, 0, 2)
	for _, email := range []string{"bulk-one@example.test", "bulk-two@example.test"} {
		var id string
		if err := integrationPool.QueryRow(context.Background(), `
			INSERT INTO contacts (organization_id, email, first_name, status)
			VALUES ($1, $2, 'Bulk', 'pending')
			RETURNING id
		`, registered.Organization.ID, email).Scan(&id); err != nil {
			t.Fatalf("seed bulk contact: %v", err)
		}
		parsedID, err := uuid.Parse(id)
		if err != nil {
			t.Fatalf("parse bulk contact ID: %v", err)
		}
		contactIDs = append(contactIDs, parsedID)
	}
	idsJSON, err := json.Marshal(contactIDs)
	if err != nil {
		t.Fatalf("encode contact IDs: %v", err)
	}
	subscribeBody := fmt.Sprintf(
		`{"contact_ids":%s,"action":"subscribe","consent":{"source":"bulk_test","purpose":"marketing","evidence":{"confirmed":true}}}`,
		idsJSON,
	)
	w := doRequest(t, s, http.MethodPost, "/api/v1/contacts/bulk", subscribeBody, registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	for _, contactID := range contactIDs {
		var status string
		var consentEvents int
		if err := integrationPool.QueryRow(context.Background(), `
			SELECT status FROM contacts WHERE id = $1 AND organization_id = $2
		`, contactID, registered.Organization.ID).Scan(&status); err != nil {
			t.Fatalf("load bulk subscribed contact: %v", err)
		}
		if status != "subscribed" {
			t.Fatalf("bulk contact status = %q, want subscribed", status)
		}
		if err := integrationPool.QueryRow(context.Background(), `
			SELECT count(*) FROM consent_events
			WHERE contact_id = $1 AND event_type = 'subscribe' AND source = 'bulk_test'
		`, contactID).Scan(&consentEvents); err != nil {
			t.Fatalf("count bulk subscribe consent: %v", err)
		}
		if consentEvents != 1 {
			t.Fatalf("subscribe consent events = %d, want 1", consentEvents)
		}
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/contacts/bulk",
		fmt.Sprintf(`{"contact_ids":%s,"action":"unsubscribe"}`, idsJSON),
		registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var unsubscribed int
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM contacts
		WHERE id = ANY($1::uuid[]) AND organization_id = $2 AND status = 'unsubscribed'
	`, contactIDs, registered.Organization.ID).Scan(&unsubscribed); err != nil {
		t.Fatalf("count bulk unsubscribed contacts: %v", err)
	}
	if unsubscribed != len(contactIDs) {
		t.Fatalf("bulk unsubscribed contacts = %d, want %d", unsubscribed, len(contactIDs))
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/contacts/bulk",
		fmt.Sprintf(`{"contact_ids":%s,"action":"delete"}`, idsJSON),
		registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var remaining int
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM contacts WHERE id = ANY($1::uuid[]) AND organization_id = $2
	`, contactIDs, registered.Organization.ID).Scan(&remaining); err != nil {
		t.Fatalf("count deleted contacts: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining selected contacts = %d, want 0", remaining)
	}
}

func TestSenderProfileUpdatePreservesSocialLinksWhenOmitted(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	registered := seedIntegrationUser(
		t, "sender-profile-owner@example.com", "profile-password", "Profile Owner", "Profile Org",
	)

	w := doRequest(t, s, http.MethodPut, "/api/v1/sender-profile",
		`{"social_links":[{"label":"Company","url":"https://example.test"}]}`,
		registered.AccessToken,
	)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, http.MethodPut, "/api/v1/sender-profile", `{}`, registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var profile struct {
		SocialLinks []struct {
			Label string `json:"label"`
			URL   string `json:"url"`
		} `json:"social_links"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
		t.Fatalf("decode sender profile: %v", err)
	}
	if len(profile.SocialLinks) != 1 ||
		profile.SocialLinks[0].Label != "Company" ||
		profile.SocialLinks[0].URL != "https://example.test" {
		t.Fatalf("social links after omitted update = %#v, want the previously saved link", profile.SocialLinks)
	}
}

func TestCampaignsLifecycleIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)

	registered := seedIntegrationUser(
		t, "campaigns-owner@example.com", "campaigns-password", "Campaigns Owner", "Campaigns Org",
	)
	token := registered.AccessToken
	senderIdentityID, _ := seedVerifiedSender(t, registered.Organization.ID, "newsletter")

	createBody := fmt.Sprintf(`{
		"name":"Integration Campaign",
		"subject":"Hello from integration test",
		"sender_identity_id":"%s",
		"track_opens":true,
		"track_clicks":true,
		"content":{"version":1,"blocks":[{"type":"heading","content":"Hello {{first_name}}"},{"type":"paragraph","content":"Welcome!"}]}
	}`, senderIdentityID)
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
	var senderReady, profileReady bool
	for _, check := range validation.Checks {
		if check.Name == "sender" && check.Status == "pass" {
			senderReady = true
		}
		if check.Name == "sender_profile" && check.Status == "pass" {
			profileReady = true
		}
	}
	if !senderReady || !profileReady {
		t.Fatalf("campaign validation did not accept the verified sender identity and profile: %+v", validation.Checks)
	}

	htmlContentUpdate := `{"content":{"version":2,"editor":"grapesjs","html":"<table role=\"presentation\"><tr><td><h1>HTML campaign</h1></td></tr></table>","blocks":[]}}`
	w = doRequest(t, s, http.MethodPatch, "/api/v1/campaigns/"+campaignID, htmlContentUpdate, token)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodPost, "/api/v1/campaigns/"+campaignID+"/validate", "", token)
	assertStatus(t, w, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &validation); err != nil {
		t.Fatalf("decode HTML campaign validation: %v", err)
	}
	var htmlContentReady bool
	for _, check := range validation.Checks {
		if check.Name == "content" && check.Status == "pass" {
			htmlContentReady = true
		}
	}
	if !htmlContentReady {
		t.Fatalf("campaign validation did not accept saved HTML content: %+v", validation.Checks)
	}

	scheduleBody, err := json.Marshal(scheduleInput{ScheduledAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("encode campaign schedule: %v", err)
	}
	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/schedule", string(scheduleBody), token)
	assertStatus(t, w, http.StatusOK)

	updateBody := `{"name":"Updated Integration Campaign","sender_postal_address":"1 Integration Way"}`
	w = doRequest(t, s, "PATCH", "/api/v1/campaigns/"+campaignID, updateBody, token)
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/schedule", string(scheduleBody), token)
	assertStatus(t, w, http.StatusConflict)

	w = doRequest(t, s, "POST", "/api/v1/campaigns/"+campaignID+"/cancel", "", token)
	assertStatus(t, w, http.StatusOK)
}

func TestSenderIdentityCRUDAndCampaignOwnershipIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	first := seedIntegrationUser(t, "sender-one-"+uuid.NewString()+"@example.test", "sender-password", "Sender One", "Sender Workspace One")
	second := seedIntegrationUser(t, "sender-two-"+uuid.NewString()+"@example.test", "sender-password", "Sender Two", "Sender Workspace Two")
	_, defaultEmail := seedVerifiedSender(t, first.Organization.ID, "newsletter")
	domain := strings.SplitN(defaultEmail, "@", 2)[1]

	createIdentityBody := fmt.Sprintf(`{
		"domain":%q,"local_part":"sales","display_name":"Sales Team",
		"reply_to":"sales-support@example.test"
	}`, domain)
	w := doRequest(t, s, http.MethodPost, "/api/v1/sender-identities", createIdentityBody, first.AccessToken)
	assertStatus(t, w, http.StatusCreated)
	var identity senderIdentity
	if err := json.Unmarshal(w.Body.Bytes(), &identity); err != nil {
		t.Fatalf("decode new sender identity: %v", err)
	}
	if identity.Email != "sales@"+domain || identity.IsDefault || !identity.Verified {
		t.Fatalf("unexpected second sender identity: %+v", identity)
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/sender-identities/"+identity.ID+"/default", "", first.AccessToken)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodGet, "/api/v1/sender-identities", "", first.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var identities struct {
		Data []senderIdentity `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &identities); err != nil {
		t.Fatalf("decode sender identity list: %v", err)
	}
	defaults := 0
	for _, item := range identities.Data {
		if item.IsDefault {
			defaults++
			if item.ID != identity.ID {
				t.Fatalf("unexpected default sender identity: %+v", item)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("default sender count = %d, want 1", defaults)
	}

	updateIdentityBody := fmt.Sprintf(`{
		"domain":%q,"local_part":"sales","display_name":"Sales and Support",
		"reply_to":"sales-support@example.test","is_default":true
	}`, domain)
	w = doRequest(t, s, http.MethodPatch, "/api/v1/sender-identities/"+identity.ID, updateIdentityBody, first.AccessToken)
	assertStatus(t, w, http.StatusOK)

	foreignIdentityCampaign := fmt.Sprintf(`{
		"name":"Foreign identity campaign","subject":"Foreign From",
		"sender_identity_id":%q,
		"content":{"version":1,"blocks":[{"type":"paragraph","content":"Hello"}]}
	}`, identity.ID)
	w = doRequest(t, s, http.MethodPost, "/api/v1/campaigns", foreignIdentityCampaign, second.AccessToken)
	assertStatus(t, w, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "sender_identity_required") {
		t.Fatalf("foreign identity was not rejected clearly: %s", w.Body.String())
	}

	spoofedFromCampaign := `{
		"name":"Foreign domain From campaign","subject":"Foreign From",
		"sender_email":"attacker@another-workspace.test",
		"content":{"version":1,"blocks":[{"type":"paragraph","content":"Hello"}]}
	}`
	w = doRequest(t, s, http.MethodPost, "/api/v1/campaigns", spoofedFromCampaign, second.AccessToken)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode spoofed campaign: %v", err)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/campaigns/"+created.ID+"/send", "", second.AccessToken)
	assertStatus(t, w, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "sender_identity_required") {
		t.Fatalf("foreign From address was not rejected at send time: %s", w.Body.String())
	}
}

func TestConflictedSendingDomainCannotValidateOrSendCampaign(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	registered := seedIntegrationUser(
		t, "domain-conflict@example.com", "domain-conflict-password", "Conflict Owner", "Conflict Org",
	)
	s.campaigns = campaigns.NewService(integrationPool, nil, s.logger, 3, false)
	domain := "owned-" + strings.ReplaceAll(uuid.NewString(), "-", "") + ".example"
	if _, err := integrationPool.Exec(context.Background(), `
		INSERT INTO sending_domains (
			organization_id, domain, status, dkim_selector, dkim_public_key,
			spf_status, dkim_status, dmarc_status, verification_status,
			stalwart_authenticated, owner_conflict
		) VALUES ($1, $2, 'verified', 'mail', 'public-key',
		          'verified', 'verified', 'verified', 'verified', true, true)
	`, registered.Organization.ID, domain); err != nil {
		t.Fatalf("insert conflicted sending domain: %v", err)
	}

	createBody := fmt.Sprintf(`{
		"name":"Ownership conflict test",
		"subject":"Conflict",
		"sender_name":"Test Sender",
		"sender_email":"news@%s",
		"sender_postal_address":"123 Test Street",
		"content":{"version":1,"blocks":[{"type":"paragraph","content":"Hello"}]}
	}`, domain)
	w := doRequest(t, s, http.MethodPost, "/api/v1/campaigns", createBody, registered.AccessToken)
	assertStatus(t, w, http.StatusCreated)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode conflict campaign: %v", err)
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/campaigns/"+created.ID+"/validate", "", registered.AccessToken)
	assertStatus(t, w, http.StatusOK)
	var validation struct {
		Checks []validationCheck `json:"checks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &validation); err != nil {
		t.Fatalf("decode conflict campaign validation: %v", err)
	}
	foundConflict := false
	for _, check := range validation.Checks {
		if check.Name == "sending_domain" && check.Status == "error" &&
			check.Message == "Domain already owned by another workspace." {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatalf("validation did not reject conflicted domain: %s", w.Body.String())
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/campaigns/"+created.ID+"/send", "", registered.AccessToken)
	assertStatus(t, w, http.StatusConflict)
	if !strings.Contains(w.Body.String(), "Domain already owned by another workspace.") {
		t.Fatalf("send response did not explain domain conflict: %s", w.Body.String())
	}
}

func TestReputationThresholdPausesCampaignIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	owner := seedIntegrationUser(t, "reputation-owner@example.com", "reputation-password", "Reputation Owner", "Reputation Org")
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

func TestStalwartFeedbackHardSoftComplaintIsolationAndIdempotency(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	server := newIntegrationServer(t)
	server.delivery.publisher = feedbackTestPublisher{}
	owner := seedIntegrationUser(t, "feedback-owner-"+uuid.NewString()+"@example.test", "feedback-password", "Feedback Owner", "Feedback Workspace")

	makeFixture := func(email string) (string, string, string) {
		t.Helper()
		var campaignID, contactID, jobID string
		if err := integrationPool.QueryRow(ctx, `
			INSERT INTO campaigns (organization_id, created_by, name, status, sender_email)
			VALUES ($1, $2, 'Feedback integration', 'sending', 'hello@example.test')
			RETURNING id
		`, owner.Organization.ID, owner.User.ID).Scan(&campaignID); err != nil {
			t.Fatalf("create feedback campaign: %v", err)
		}
		if err := integrationPool.QueryRow(ctx, `
			INSERT INTO contacts (organization_id, email, status)
			VALUES ($1, $2, 'subscribed')
			RETURNING id
		`, owner.Organization.ID, email).Scan(&contactID); err != nil {
			t.Fatalf("create feedback contact: %v", err)
		}
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
			VALUES ($1, $2, $3, $4, 'submitted')
		`, owner.Organization.ID, campaignID, contactID, email); err != nil {
			t.Fatalf("create feedback recipient: %v", err)
		}
		if err := integrationPool.QueryRow(ctx, `
			INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
			VALUES ($1, $2, $3, gen_random_uuid()::text || '@feedback.test', 'submitted')
			RETURNING id
		`, owner.Organization.ID, campaignID, contactID).Scan(&jobID); err != nil {
			t.Fatalf("create feedback delivery job: %v", err)
		}
		token, err := delivery.NewVERPToken(integrationCfg.JWTSecret, delivery.VERPClaims{
			WorkspaceID: owner.Organization.ID,
			CampaignID:  campaignID,
			Recipient:   strings.ToLower(email),
		})
		if err != nil {
			t.Fatalf("create feedback token mapping: %v", err)
		}
		if _, err := integrationPool.Exec(ctx, `
			INSERT INTO delivery_verp_tokens
				(token, organization_id, campaign_id, delivery_job_id, contact_id, email)
			VALUES ($1, $2, $3, $4, $5, lower($6))
		`, token, owner.Organization.ID, campaignID, jobID, contactID, email); err != nil {
			t.Fatalf("persist feedback token mapping: %v", err)
		}
		return campaignID, contactID, jobID
	}
	postFeedback := func(raw string) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/stalwart-feedback", strings.NewReader(raw))
		request.Header.Set("X-Webhook-Signature", computeWebhookSignature(integrationCfg.WebhookSecret, time.Now().Unix(), raw))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response.Code
	}
	makeMessage := func(email, campaignID, status, action, diagnostic string) string {
		t.Helper()
		returnPath, err := delivery.NewVERPReturnPath(integrationCfg.JWTSecret, integrationCfg.BounceDomain, delivery.VERPClaims{
			WorkspaceID: owner.Organization.ID,
			CampaignID:  campaignID,
			Recipient:   strings.ToLower(email),
		})
		if err != nil {
			t.Fatalf("create test VERP address: %v", err)
		}
		if status == "" {
			return fmt.Sprintf("From: fbl@example.test\r\nTo: %s\r\nContent-Type: multipart/report; report-type=feedback-report; boundary=x\r\n\r\n--x\r\nContent-Type: message/feedback-report\r\n\r\nFeedback-Type: abuse\r\nOriginal-Rcpt-To: %s\r\n--x--\r\n", returnPath, email)
		}
		return fmt.Sprintf("From: mailer-daemon@example.test\r\nTo: %s\r\nContent-Type: multipart/report; report-type=delivery-status; boundary=x\r\n\r\n--x\r\nContent-Type: message/delivery-status\r\n\r\nFinal-Recipient: rfc822; %s\r\nAction: %s\r\nStatus: %s\r\nDiagnostic-Code: smtp; %s\r\n--x--\r\n", returnPath, email, action, status, diagnostic)
	}

	hardEmail := "Hard-" + uuid.NewString() + "@example.test"
	hardCampaign, hardContact, hardJob := makeFixture(hardEmail)
	hardMessage := makeMessage(hardEmail, hardCampaign, "5.1.1", "failed", "550 user unknown")
	for attempt := 0; attempt < 2; attempt++ {
		if code := postFeedback(hardMessage); code != http.StatusNoContent {
			t.Fatalf("hard bounce request status = %d, want 204", code)
		}
	}
	var count int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_feedback_ingest WHERE delivery_job_id = $1
	`, hardJob).Scan(&count); err != nil || count != 1 {
		t.Fatalf("hard feedback rows = %d, err=%v; want one idempotent event", count, err)
	}
	var reason, status string
	if err := integrationPool.QueryRow(ctx, `
		SELECT reason FROM suppressions WHERE organization_id = $1 AND lower(email) = lower($2)
	`, owner.Organization.ID, hardEmail).Scan(&reason); err != nil || reason != "hard_bounce" {
		t.Fatalf("hard suppression reason = %q, err=%v", reason, err)
	}
	if err := integrationPool.QueryRow(ctx, `SELECT status FROM contacts WHERE id = $1`, hardContact).Scan(&status); err != nil || status != "suppressed" {
		t.Fatalf("hard-bounced contact status = %q, err=%v", status, err)
	}

	softEmail := "soft-" + uuid.NewString() + "@example.test"
	softCampaign, _, softJob := makeFixture(softEmail)
	for attempt := 1; attempt <= 3; attempt++ {
		message := makeMessage(softEmail, softCampaign, "4.2.0", "delayed", fmt.Sprintf("450 mailbox busy attempt %d", attempt))
		if code := postFeedback(message); code != http.StatusNoContent {
			t.Fatalf("soft bounce %d request status = %d, want 204", attempt, code)
		}
	}
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_feedback_ingest WHERE delivery_job_id = $1 AND event_type = 'soft_bounce'
	`, softJob).Scan(&count); err != nil || count != 3 {
		t.Fatalf("soft feedback rows = %d, err=%v; want 3", count, err)
	}
	if err := integrationPool.QueryRow(ctx, `
		SELECT reason FROM suppressions WHERE organization_id = $1 AND lower(email) = lower($2)
	`, owner.Organization.ID, softEmail).Scan(&reason); err != nil || reason != "hard_bounce" {
		t.Fatalf("repeated soft-bounce suppression reason = %q, err=%v", reason, err)
	}

	complaintEmail := "complaint-" + uuid.NewString() + "@example.test"
	complaintCampaign, _, complaintJob := makeFixture(complaintEmail)
	if code := postFeedback(makeMessage(complaintEmail, complaintCampaign, "", "", "")); code != http.StatusNoContent {
		t.Fatalf("ARF complaint request status = %d, want 204", code)
	}
	if err := integrationPool.QueryRow(ctx, `
		SELECT status FROM delivery_jobs WHERE id = $1
	`, complaintJob).Scan(&status); err != nil || status != "complained" {
		t.Fatalf("complaint job status = %q, err=%v", status, err)
	}
	if err := integrationPool.QueryRow(ctx, `
		SELECT reason FROM suppressions WHERE organization_id = $1 AND lower(email) = lower($2)
	`, owner.Organization.ID, complaintEmail).Scan(&reason); err != nil || reason != "complaint" {
		t.Fatalf("complaint suppression reason = %q, err=%v", reason, err)
	}

	other := seedIntegrationUser(t, "feedback-other-"+uuid.NewString()+"@example.test", "feedback-password", "Other Owner", "Other Workspace")
	forgedTokenAddress, err := delivery.NewVERPReturnPath(integrationCfg.JWTSecret, integrationCfg.BounceDomain, delivery.VERPClaims{
		WorkspaceID: other.Organization.ID,
		CampaignID:  hardCampaign,
		Recipient:   hardEmail,
	})
	if err != nil {
		t.Fatalf("create cross-workspace VERP token: %v", err)
	}
	forgedMessage := strings.Replace(hardMessage, mustExtractToAddress(hardMessage), forgedTokenAddress, 1)
	if code := postFeedback(forgedMessage); code != http.StatusBadRequest {
		t.Fatalf("cross-workspace feedback status = %d, want 400 for unknown token", code)
	}
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_feedback_ingest WHERE organization_id = $1 AND campaign_id = $2
	`, other.Organization.ID, hardCampaign).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-workspace feedback rows = %d, err=%v", count, err)
	}
}

func TestStalwartTransactionalDeliveryReportUpdatesRecipient(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx := context.Background()
	server := newIntegrationServer(t)
	owner := seedIntegrationUser(t, "transactional-feedback-"+uuid.NewString()+"@example.test",
		"feedback-password", "Transactional Feedback", "Transactional Feedback Workspace")
	store := transactional.NewStore(integrationPool)
	recipientEmail := "recipient-" + uuid.NewString() + "@example.test"
	receipt, err := store.Enqueue(ctx, transactional.EnqueueOptions{OrganizationID: owner.Organization.ID},
		transactional.MessageInput{
			From: "receipts@example.test", To: []string{recipientEmail},
			Subject: "Receipt", Text: "Your receipt",
		})
	if err != nil {
		t.Fatalf("enqueue transactional message: %v", err)
	}
	var recipientID string
	if err := integrationPool.QueryRow(ctx, `
		SELECT id::text FROM transactional_message_recipients WHERE message_id = $1
	`, receipt.ID).Scan(&recipientID); err != nil {
		t.Fatalf("load transactional recipient: %v", err)
	}
	job := &transactional.Job{
		Organization: owner.Organization.ID, MessageUUID: receipt.ID,
		RecipientID: recipientID, To: recipientEmail,
	}
	if err := store.TransitionRecipient(ctx, job.RecipientID, transactional.StatusSending,
		"system", 0, "", "test submission in progress", time.Time{}, nil); err != nil {
		t.Fatalf("mark transactional message sending: %v", err)
	}
	if err := store.MarkAccepted(ctx, job, "<smtp-accepted@example.test>"); err != nil {
		t.Fatalf("record SMTP acceptance: %v", err)
	}
	token, returnPath, err := transactional.NewVERPForRecipient(
		integrationCfg.JWTSecret, integrationCfg.BounceDomain,
		owner.Organization.ID, receipt.ID, job.To,
	)
	if err != nil {
		t.Fatalf("create transactional VERP address: %v", err)
	}
	if err := store.SaveVERPToken(ctx, job, token, job.To); err != nil {
		t.Fatalf("store transactional VERP token: %v", err)
	}
	raw := fmt.Sprintf(
		"From: mailer-daemon@example.test\r\nTo: %s\r\n"+
			"Content-Type: multipart/report; report-type=delivery-status; boundary=x\r\n\r\n"+
			"--x\r\nContent-Type: message/delivery-status\r\n\r\n"+
			"Final-Recipient: rfc822; %s\r\nAction: delivered\r\n"+
			"Status: 2.0.0\r\nDiagnostic-Code: smtp; 250 accepted\r\n--x--\r\n",
		returnPath, job.To,
	)
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/stalwart-feedback", strings.NewReader(raw))
		request.Header.Set("X-Webhook-Signature",
			computeWebhookSignature(integrationCfg.WebhookSecret, time.Now().Unix(), raw))
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delivery report attempt %d status = %d, body=%s; want 204",
				attempt+1, response.Code, response.Body.String())
		}
	}

	var messageStatus, recipientStatus string
	if err := integrationPool.QueryRow(ctx, `
		SELECT m.status, r.status
		FROM transactional_messages m
		JOIN transactional_message_recipients r ON r.message_id = m.id
		WHERE m.id = $1
	`, receipt.ID).Scan(&messageStatus, &recipientStatus); err != nil {
		t.Fatalf("load transactional status: %v", err)
	}
	if messageStatus != "delivered" || recipientStatus != "delivered" {
		t.Fatalf("statuses = message %q, recipient %q; want delivered", messageStatus, recipientStatus)
	}
	var feedbackCount int
	if err := integrationPool.QueryRow(ctx, `
		SELECT count(*) FROM transactional_feedback_ingest WHERE message_id = $1
	`, receipt.ID).Scan(&feedbackCount); err != nil || feedbackCount != 1 {
		t.Fatalf("transactional feedback rows = %d, err=%v; want one idempotent event", feedbackCount, err)
	}
}

func TestTransactionalAPIKeySendReadRotateAndRevoke(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	owner := seedIntegrationUser(t, "transactional-api-"+uuid.NewString()+"@example.test",
		"transactional-password", "Transactional API", "Transactional API Workspace")
	server := newIntegrationServer(t)
	server.cfg.TransactionalEnabled = true
	if _, err := integrationPool.Exec(context.Background(), `
		UPDATE transactional_settings
		SET enabled = true, status = 'active'
		WHERE organization_id = $1
	`, owner.Organization.ID); err != nil {
		t.Fatalf("enable transactional workspace: %v", err)
	}
	var quotaDomainID string
	quotaDomain := "transactional-" + uuid.NewString() + ".example.test"
	if err := integrationPool.QueryRow(context.Background(), `
		INSERT INTO sending_domains
			(organization_id, domain, status, spf_status, dkim_status, transactional_enabled)
		VALUES ($1, $2, 'verified', 'verified', 'verified', true)
		RETURNING id::text
	`, owner.Organization.ID, quotaDomain).Scan(&quotaDomainID); err != nil {
		t.Fatalf("create verified transactional quota domain: %v", err)
	}
	if _, err := integrationPool.Exec(context.Background(), `
		INSERT INTO domain_quotas
			(domain_id, stream, daily_limit, monthly_limit, per_second_limit)
		VALUES ($1, 'transactional', 1, 10, 100)
	`, quotaDomainID); err != nil {
		t.Fatalf("set transactional domain quota: %v", err)
	}

	createKeyBody := `{"name":"integration key","scopes":["send","read"],"test_mode":true}`
	createKeyResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/api-keys", createKeyBody, owner.AccessToken)
	assertStatus(t, createKeyResponse, http.StatusCreated)
	var createdKey transactionalAPIKeyResponse
	if err := json.Unmarshal(createKeyResponse.Body.Bytes(), &createdKey); err != nil {
		t.Fatalf("decode transactional API key: %v", err)
	}
	if createdKey.PlainKey == "" || !strings.HasPrefix(createdKey.PlainKey, "sw_tx_test_") {
		t.Fatalf("created key did not return a one-time test secret: %+v", createdKey)
	}

	messageBody, err := json.Marshal(transactional.MessageInput{
		From: "receipts@" + quotaDomain, To: []string{"recipient@example.test"},
		Subject: "Test receipt", Text: "This must not be delivered",
	})
	if err != nil {
		t.Fatalf("encode transactional message: %v", err)
	}
	sendResponse := doRequest(t, server, http.MethodPost, "/api/v1/transactional/messages",
		string(messageBody), createdKey.PlainKey)
	assertStatus(t, sendResponse, http.StatusAccepted)
	var receipt transactional.MessageReceipt
	if err := json.Unmarshal(sendResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode transactional message receipt: %v", err)
	}
	if receipt.Status != transactional.StatusRejected {
		t.Fatalf("test-mode receipt status = %q, want rejected without delivery", receipt.Status)
	}

	readResponse := doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/messages/"+receipt.ID, "", createdKey.PlainKey)
	assertStatus(t, readResponse, http.StatusOK)
	var readMessage transactionalMessageResponse
	if err := json.Unmarshal(readResponse.Body.Bytes(), &readMessage); err != nil {
		t.Fatalf("decode transactional message: %v", err)
	}
	if readMessage.ID != receipt.ID || readMessage.Status != string(transactional.StatusRejected) {
		t.Fatalf("read transactional message = %+v", readMessage)
	}
	dashboardMessagesResponse := doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/dashboard/messages?limit=20", "", owner.AccessToken)
	assertStatus(t, dashboardMessagesResponse, http.StatusOK)
	var dashboardMessages struct {
		Data []transactionalMessageResponse `json:"data"`
	}
	if err := json.Unmarshal(dashboardMessagesResponse.Body.Bytes(), &dashboardMessages); err != nil {
		t.Fatalf("decode transactional dashboard messages: %v", err)
	}
	if len(dashboardMessages.Data) != 1 || dashboardMessages.Data[0].ID != receipt.ID {
		t.Fatalf("transactional dashboard messages = %+v; want message %s", dashboardMessages.Data, receipt.ID)
	}
	usageResponse := doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/usage", "", owner.AccessToken)
	assertStatus(t, usageResponse, http.StatusOK)
	var usage map[string]int64
	if err := json.Unmarshal(usageResponse.Body.Bytes(), &usage); err != nil {
		t.Fatalf("decode transactional usage: %v", err)
	}
	if _, ok := usage["daily_limit"]; ok {
		t.Fatalf("transactional usage still exposes a workspace quota: %+v", usage)
	}

	liveKeyResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/api-keys", `{"name":"send-only key","scopes":["send"]}`, owner.AccessToken)
	assertStatus(t, liveKeyResponse, http.StatusCreated)
	var liveKey transactionalAPIKeyResponse
	if err := json.Unmarshal(liveKeyResponse.Body.Bytes(), &liveKey); err != nil {
		t.Fatalf("decode live transactional API key: %v", err)
	}
	assertStatus(t, doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/messages", string(messageBody), liveKey.PlainKey), http.StatusAccepted)
	quotaResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/messages", string(messageBody), liveKey.PlainKey)
	assertStatus(t, quotaResponse, http.StatusTooManyRequests)
	var quotaBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Window string `json:"window"`
		Reset  string `json:"reset_at"`
	}
	if err := json.Unmarshal(quotaResponse.Body.Bytes(), &quotaBody); err != nil {
		t.Fatalf("decode quota-exceeded response: %v", err)
	}
	if quotaBody.Error.Code != "quota_exceeded" || quotaBody.Window == "" ||
		quotaBody.Reset == "" || quotaResponse.Result().Header.Get("Retry-After") == "" {
		t.Fatalf("quota response missing code/window/reset/Retry-After: body=%s headers=%v",
			quotaResponse.Body.String(), quotaResponse.Result().Header)
	}
	assertStatus(t, doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/messages/"+receipt.ID, "", liveKey.PlainKey), http.StatusForbidden)

	rotateResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/api-keys/"+createdKey.ID+"/rotate", "", owner.AccessToken)
	assertStatus(t, rotateResponse, http.StatusCreated)
	var rotatedKey transactionalAPIKeyResponse
	if err := json.Unmarshal(rotateResponse.Body.Bytes(), &rotatedKey); err != nil {
		t.Fatalf("decode rotated transactional API key: %v", err)
	}
	if rotatedKey.PlainKey == "" || rotatedKey.ID == createdKey.ID {
		t.Fatalf("rotation did not issue a new key: %+v", rotatedKey)
	}
	assertStatus(t, doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/messages/"+receipt.ID, "", createdKey.PlainKey), http.StatusOK)
	assertStatus(t, doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/messages/"+receipt.ID, "", rotatedKey.PlainKey), http.StatusOK)

	revokeResponse := doRequest(t, server, http.MethodDelete,
		"/api/v1/transactional/api-keys/"+createdKey.ID, "", owner.AccessToken)
	assertStatus(t, revokeResponse, http.StatusNoContent)
	assertStatus(t, doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/messages/"+receipt.ID, "", createdKey.PlainKey), http.StatusUnauthorized)
}

func TestTransactionalSMTPCredentialLifecycle(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	owner := seedIntegrationUser(t, "transactional-smtp-"+uuid.NewString()+"@example.test",
		"smtp-credential-password", "Transactional SMTP", "Transactional SMTP Workspace")
	server := newIntegrationServer(t)

	createdResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/smtp-credentials", `{"name":"app server"}`, owner.AccessToken)
	assertStatus(t, createdResponse, http.StatusCreated)
	var credential transactionalSMTPCredentialResponse
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &credential); err != nil {
		t.Fatalf("decode SMTP credential: %v", err)
	}
	if credential.Username == "" || credential.Password == "" {
		t.Fatalf("SMTP credential response omitted one-time login: %+v", credential)
	}
	if _, err := transactional.AuthenticateSMTP(context.Background(), integrationPool, credential.Username, credential.Password); err != nil {
		t.Fatalf("authenticate newly created SMTP credential: %v", err)
	}

	listResponse := doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/smtp-credentials", "", owner.AccessToken)
	assertStatus(t, listResponse, http.StatusOK)
	if strings.Contains(listResponse.Body.String(), credential.Password) {
		t.Fatal("SMTP credential listing returned its one-time password")
	}

	otherOwner := seedIntegrationUser(t, "transactional-smtp-other-"+uuid.NewString()+"@example.test",
		"smtp-other-password", "Other SMTP Owner", "Other SMTP Workspace")
	crossWorkspace := doRequest(t, server, http.MethodDelete,
		"/api/v1/transactional/smtp-credentials/"+credential.ID, "", otherOwner.AccessToken)
	assertStatus(t, crossWorkspace, http.StatusNotFound)

	revokeResponse := doRequest(t, server, http.MethodDelete,
		"/api/v1/transactional/smtp-credentials/"+credential.ID, "", owner.AccessToken)
	assertStatus(t, revokeResponse, http.StatusNoContent)
	if _, err := transactional.AuthenticateSMTP(context.Background(), integrationPool, credential.Username, credential.Password); !errors.Is(err, transactional.ErrInvalidSMTPCredentials) {
		t.Fatalf("revoked credential authentication error = %v; want invalid credentials", err)
	}
}

func TestTransactionalSMTPSubmissionRequiresTLSAuthAndEnqueues(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := seedIntegrationUser(t, "transactional-smtp-submit-"+uuid.NewString()+"@example.test",
		"smtp-submit-password", "Transactional SMTP Submit", "Transactional SMTP Submit Workspace")
	domain := "smtp-" + uuid.NewString() + ".example.test"
	if _, err := integrationPool.Exec(context.Background(), `
		INSERT INTO sending_domains (organization_id, domain, status, spf_status, dkim_status, transactional_enabled)
		VALUES ($1, $2, 'verified', 'verified', 'verified', true)
	`, owner.Organization.ID, domain); err != nil {
		t.Fatalf("seed active transactional sending domain: %v", err)
	}
	if _, err := integrationPool.Exec(context.Background(), `
		UPDATE transactional_settings SET enabled = true, status = 'active'
		WHERE organization_id = $1
	`, owner.Organization.ID); err != nil {
		t.Fatalf("enable transactional sending: %v", err)
	}
	server := newIntegrationServer(t)
	server.cfg.TransactionalEnabled = true
	server.cfg.TransactionalRequestsPerSecond = 5
	createdResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/smtp-credentials", `{"name":"network test"}`, owner.AccessToken)
	assertStatus(t, createdResponse, http.StatusCreated)
	var credential transactionalSMTPCredentialResponse
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &credential); err != nil {
		t.Fatalf("decode SMTP credential: %v", err)
	}

	startTLSListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for test SMTP server: %v", err)
	}
	implicitTLSListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		startTLSListener.Close()
		t.Fatalf("listen for implicit TLS test SMTP server: %v", err)
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := tlsServer.TLS.Certificates[0]
	tlsServer.Close()
	smtpConfig := integrationCfg
	smtpConfig.TransactionalEnabled = true
	smtpConfig.TransactionalSMTPEnabled = true
	smtpConfig.TransactionalSMTPHostname = "localhost"
	smtpConfig.TransactionalMaxRecipients = 10
	smtpConfig.TransactionalMaxMessageBytes = 1 << 20
	submissionServer := transactional.NewSMTPSubmissionServer(smtpConfig, integrationPool)
	go func() {
		if err := submissionServer.ServeListeners(ctx, startTLSListener, implicitTLSListener, &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		}); err != nil && ctx.Err() == nil {
			t.Errorf("run test SMTP server: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = startTLSListener.Close()
		_ = implicitTLSListener.Close()
	})

	client, err := smtp.Dial(startTLSListener.Addr().String())
	if err != nil {
		t.Fatalf("connect to SMTP submission listener: %v", err)
	}
	defer client.Close()
	if err := client.StartTLS(&tls.Config{ServerName: "localhost", InsecureSkipVerify: true}); err != nil {
		t.Fatalf("negotiate STARTTLS: %v", err)
	}
	if err := client.Auth(smtp.PlainAuth("", credential.Username, credential.Password, "127.0.0.1")); err != nil {
		t.Fatalf("authenticate SMTP credential: %v", err)
	}
	if err := client.Mail("sender@" + domain); err != nil {
		t.Fatalf("specify authorized sender: %v", err)
	}
	recipient := "recipient@example.test"
	if err := client.Rcpt(recipient); err != nil {
		t.Fatalf("specify recipient: %v", err)
	}
	data, err := client.Data()
	if err != nil {
		t.Fatalf("start SMTP message data: %v", err)
	}
	_, err = io.WriteString(data, "From: sender@"+domain+"\r\nTo: "+recipient+
		"\r\nSubject: SMTP submission integration proof\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nQueued through authenticated SMTP.\r\n")
	if err != nil {
		t.Fatalf("write SMTP message: %v", err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("submit SMTP message: %v", err)
	}
	if err := client.Quit(); err != nil {
		t.Fatalf("close SMTP session: %v", err)
	}

	var queued int
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM transactional_messages
		WHERE organization_id = $1 AND subject = 'SMTP submission integration proof'
	`, owner.Organization.ID).Scan(&queued); err != nil {
		t.Fatalf("query SMTP-submitted message: %v", err)
	}
	if queued != 1 {
		t.Fatalf("SMTP-submitted messages = %d; want one queued message", queued)
	}
}

func TestTransactionalTemplateVersionsRenderInMessageSend(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	owner := seedIntegrationUser(t, "transactional-template-"+uuid.NewString()+"@example.test",
		"transactional-password", "Transactional Template", "Transactional Template Workspace")
	server := newIntegrationServer(t)
	server.cfg.TransactionalEnabled = true
	if _, err := integrationPool.Exec(context.Background(), `
		UPDATE transactional_settings SET enabled = true, status = 'active'
		WHERE organization_id = $1
	`, owner.Organization.ID); err != nil {
		t.Fatalf("enable transactional workspace: %v", err)
	}

	createResponse := doRequest(t, server, http.MethodPost, "/api/v1/transactional/templates",
		`{"name":"Receipt","subject":"Receipt for {{name}}","html":"<p>Hello {{name}}</p>","text":"Hello {{name}}"}`,
		owner.AccessToken)
	assertStatus(t, createResponse, http.StatusCreated)
	var template transactionalTemplateResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &template); err != nil {
		t.Fatalf("decode transactional template: %v", err)
	}
	if template.CurrentVersion != 1 || len(template.Variables) != 1 || template.Variables[0] != "name" {
		t.Fatalf("created transactional template = %+v", template)
	}

	updateResponse := doRequest(t, server, http.MethodPatch,
		"/api/v1/transactional/templates/"+template.ID,
		`{"subject":"Updated receipt for {{name}}"}`, owner.AccessToken)
	assertStatus(t, updateResponse, http.StatusOK)
	if err := json.Unmarshal(updateResponse.Body.Bytes(), &template); err != nil {
		t.Fatalf("decode updated transactional template: %v", err)
	}
	if template.CurrentVersion != 2 || template.Subject != "Updated receipt for {{name}}" {
		t.Fatalf("updated transactional template = %+v; want version 2", template)
	}

	createKeyResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/api-keys", `{"name":"template test","test_mode":true}`, owner.AccessToken)
	assertStatus(t, createKeyResponse, http.StatusCreated)
	var key transactionalAPIKeyResponse
	if err := json.Unmarshal(createKeyResponse.Body.Bytes(), &key); err != nil {
		t.Fatalf("decode transactional API key: %v", err)
	}
	messageBody, err := json.Marshal(transactional.MessageInput{
		From: "receipts@example.test", To: []string{"recipient@example.test"},
		TemplateID: template.ID, Variables: map[string]string{"name": "<Mike>"},
	})
	if err != nil {
		t.Fatalf("encode template message: %v", err)
	}
	sendResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/messages", string(messageBody), key.PlainKey)
	assertStatus(t, sendResponse, http.StatusAccepted)
	var receipt transactional.MessageReceipt
	if err := json.Unmarshal(sendResponse.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode template message receipt: %v", err)
	}
	readResponse := doRequest(t, server, http.MethodGet,
		"/api/v1/transactional/messages/"+receipt.ID, "", key.PlainKey)
	assertStatus(t, readResponse, http.StatusOK)
	var message transactionalMessageResponse
	if err := json.Unmarshal(readResponse.Body.Bytes(), &message); err != nil {
		t.Fatalf("decode rendered template message: %v", err)
	}
	if message.Subject != "Updated receipt for <Mike>" || message.HTML != "<p>Hello &lt;Mike&gt;</p>" {
		t.Fatalf("rendered message subject/html = %q / %q", message.Subject, message.HTML)
	}
	var storedTemplateID string
	var storedVersion int
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT template_id::text, template_version
		FROM transactional_messages WHERE id = $1
	`, receipt.ID).Scan(&storedTemplateID, &storedVersion); err != nil {
		t.Fatalf("load message template version: %v", err)
	}
	if storedTemplateID != template.ID || storedVersion != 2 {
		t.Fatalf("stored template reference = %s version %d; want %s version 2",
			storedTemplateID, storedVersion, template.ID)
	}

	messageBody, err = json.Marshal(transactional.MessageInput{
		From: "receipts@example.test", To: []string{"another@example.test"}, TemplateID: template.ID,
	})
	if err != nil {
		t.Fatalf("encode missing-variable template message: %v", err)
	}
	assertStatus(t, doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/messages", string(messageBody), key.PlainKey), http.StatusBadRequest)
}

func TestTransactionalSendingDomainRequiresVerifiedSPFAndDKIM(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	owner := seedIntegrationUser(t, "transactional-domain-"+uuid.NewString()+"@example.test",
		"transactional-password", "Transactional Domain", "Transactional Domain Workspace")
	server := newIntegrationServer(t)
	domainID := uuid.NewString()
	domainName := "mail-" + uuid.NewString() + ".example.test"
	if _, err := integrationPool.Exec(context.Background(), `
		INSERT INTO sending_domains (id, organization_id, domain, status, spf_status, dkim_status)
		VALUES ($1, $2, $3, 'verified', 'verified', 'verified')
	`, domainID, owner.Organization.ID, domainName); err != nil {
		t.Fatalf("insert verified sending domain: %v", err)
	}

	enableResponse := doRequest(t, server, http.MethodPut,
		"/api/v1/domains/"+domainID+"/transactional", `{"enabled":true}`, owner.AccessToken)
	assertStatus(t, enableResponse, http.StatusOK)
	var domain sendingDomain
	if err := json.Unmarshal(enableResponse.Body.Bytes(), &domain); err != nil {
		t.Fatalf("decode transactional sending domain: %v", err)
	}
	if !domain.TransactionalEnabled {
		t.Fatalf("transactional_enabled = false after successful enable: %+v", domain)
	}

	if _, err := integrationPool.Exec(context.Background(), `
		UPDATE sending_domains SET status = 'pending' WHERE id = $1
	`, domainID); err != nil {
		t.Fatalf("make sending domain pending: %v", err)
	}
	assertStatus(t, doRequest(t, server, http.MethodPut,
		"/api/v1/domains/"+domainID+"/transactional", `{"enabled":false}`, owner.AccessToken), http.StatusOK)
	assertStatus(t, doRequest(t, server, http.MethodPut,
		"/api/v1/domains/"+domainID+"/transactional", `{"enabled":true}`, owner.AccessToken), http.StatusConflict)
}

func TestTransactionalWebhookManagementEncryptsSecretsAndRejectsPrivateURLs(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	owner := seedIntegrationUser(t, "transactional-webhook-"+uuid.NewString()+"@example.test",
		"transactional-password", "Transactional Webhook", "Transactional Webhook Workspace")
	server := newIntegrationServer(t)

	createResponse := doRequest(t, server, http.MethodPost, "/api/v1/transactional/webhooks",
		`{"url":"https://hooks.example.test/events","events":["message.delivered","message.bounced"]}`,
		owner.AccessToken)
	assertStatus(t, createResponse, http.StatusCreated)
	var webhook transactionalWebhookResponse
	if err := json.Unmarshal(createResponse.Body.Bytes(), &webhook); err != nil {
		t.Fatalf("decode transactional webhook: %v", err)
	}
	if webhook.ID == "" || webhook.SigningSecret == "" || !webhook.Enabled {
		t.Fatalf("created webhook omitted its one-time signing secret or enabled state: %+v", webhook)
	}

	var encrypted []byte
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT encrypted_secret FROM transactional_webhooks WHERE id = $1
	`, webhook.ID).Scan(&encrypted); err != nil {
		t.Fatalf("load encrypted webhook secret: %v", err)
	}
	if strings.Contains(string(encrypted), webhook.SigningSecret) {
		t.Fatal("webhook signing secret was stored in plaintext")
	}
	decrypted, err := transactional.DecryptWebhookSecret(integrationCfg.DKIMEncryptionKey, encrypted)
	if err != nil || decrypted != webhook.SigningSecret {
		t.Fatalf("decrypt stored webhook secret = %q, err=%v", decrypted, err)
	}

	listResponse := doRequest(t, server, http.MethodGet, "/api/v1/transactional/webhooks", "", owner.AccessToken)
	assertStatus(t, listResponse, http.StatusOK)
	if strings.Contains(listResponse.Body.String(), webhook.SigningSecret) {
		t.Fatal("webhook list exposed the one-time signing secret")
	}
	assertStatus(t, doRequest(t, server, http.MethodPost, "/api/v1/transactional/webhooks",
		`{"url":"https://127.0.0.1/admin","events":["message.delivered"]}`, owner.AccessToken),
		http.StatusBadRequest)

	rotateResponse := doRequest(t, server, http.MethodPost,
		"/api/v1/transactional/webhooks/"+webhook.ID+"/rotate-secret", "", owner.AccessToken)
	assertStatus(t, rotateResponse, http.StatusOK)
	var rotated struct {
		SigningSecret string `json:"signing_secret"`
	}
	if err := json.Unmarshal(rotateResponse.Body.Bytes(), &rotated); err != nil {
		t.Fatalf("decode rotated webhook secret: %v", err)
	}
	if rotated.SigningSecret == "" || rotated.SigningSecret == webhook.SigningSecret {
		t.Fatal("webhook secret rotation did not issue a new secret")
	}
	assertStatus(t, doRequest(t, server, http.MethodDelete,
		"/api/v1/transactional/webhooks/"+webhook.ID, "", owner.AccessToken), http.StatusNoContent)
}

type feedbackTestPublisher struct{}

func (feedbackTestPublisher) Publish(context.Context, string, []byte) error { return nil }

func mustExtractToAddress(message string) string {
	for _, line := range strings.Split(message, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "to: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "To: "))
		}
	}
	return ""
}

func TestDomainsIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	var hostedDomains []string
	s.stalwartDomainHoster = stalwartDomainProvisionFunc(func(_ context.Context, domain string) error {
		hostedDomains = append(hostedDomains, domain)
		return nil
	})

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
	conflictedDomain := "conflicted-smoke-" + suffix + ".example.com"
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
	if _, err := integrationPool.Exec(context.Background(), `
		INSERT INTO sending_domains (
			organization_id, domain, status, dkim_selector, dkim_public_key,
			spf_status, dkim_status, dmarc_status, verification_status,
			stalwart_authenticated, owner_conflict
		) VALUES ($1, $2, 'verified', 'mail', 'public-key',
		          'verified', 'verified', 'verified', 'verified', true, true)
	`, orgID, conflictedDomain); err != nil {
		t.Fatalf("insert conflicted sending domain: %v", err)
	}

	w := doRequest(t, s, "GET", "/api/v1/domains", "", token)
	assertStatus(t, w, http.StatusOK)
	var listResp struct {
		Data []struct {
			Domain             string     `json:"domain"`
			Status             string     `json:"status"`
			OwnerConflict      bool       `json:"owner_conflict"`
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
	foundConflict := false
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
		if d.Domain == conflictedDomain {
			foundConflict = d.Status == "verified" && d.OwnerConflict
		}
	}
	if !foundNullKey {
		t.Fatalf("expected %s in domain list", nullKeyDomain)
	}
	if !foundEmptyKey || !foundMisconfigured || !foundVerified || !foundConflict {
		t.Fatalf("expected empty-key/misconfigured, verified, and ownership-conflict rows in domain list; got %+v", listResp.Data)
	}

	w = doRequest(t, s, "GET", "/api/v1/domains/"+mustDomainID(t, orgID, nullKeyDomain)+"/dns-records", "", token)
	assertStatus(t, w, http.StatusOK)
	var nullKeyRecords struct {
		DNSRecords []dnsRecord `json:"dns_records"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &nullKeyRecords); err != nil {
		t.Fatalf("decode NULL-key DNS records: %v", err)
	}
	if len(nullKeyRecords.DNSRecords) != 5 || nullKeyRecords.DNSRecords[1].Value != "" {
		t.Fatalf("expected five records and no DKIM placeholder for NULL key, got %+v", nullKeyRecords.DNSRecords)
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
	var encryptedPrivateKey, verificationToken string
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT dkim_private_key, verification_token
		FROM sending_domains WHERE id = $1 AND organization_id = $2
	`, domainID, orgID).Scan(&encryptedPrivateKey, &verificationToken); err != nil {
		t.Fatalf("read generated encrypted private key: %v", err)
	}
	if encryptedPrivateKey == "" || strings.Contains(encryptedPrivateKey, "PRIVATE KEY") {
		t.Fatal("expected generated DKIM private key to be encrypted at rest")
	}
	if len(verificationToken) < 32 || verificationToken == created.Domain {
		t.Fatalf("expected a unique, opaque verification token; got %q", verificationToken)
	}
	if strings.Contains(w.Body.String(), verificationToken) {
		t.Fatal("create-domain response must not expose the verification token outside DNS records")
	}

	secondDomainBody := `{"domain":"newsletter-second-` + suffix + `.example.com"}`
	w = doRequest(t, s, "POST", "/api/v1/domains", secondDomainBody, token)
	assertStatus(t, w, http.StatusCreated)
	var secondDomain struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &secondDomain); err != nil {
		t.Fatalf("decode second created domain: %v", err)
	}
	var secondVerificationToken string
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT verification_token FROM sending_domains WHERE id = $1 AND organization_id = $2
	`, secondDomain.ID, orgID).Scan(&secondVerificationToken); err != nil {
		t.Fatalf("read second generated domain verification token: %v", err)
	}
	if secondVerificationToken == verificationToken {
		t.Fatal("sending domains must have distinct verification tokens")
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
	if len(recordsResp.DNSRecords) != 5 {
		t.Fatalf("expected 5 dns records, got %d: %s", len(recordsResp.DNSRecords), w.Body.String())
	}
	verificationRecord := recordsResp.DNSRecords[3]
	if verificationRecord.Type != "TXT" || !strings.HasPrefix(verificationRecord.Name, "_sendwise-verification.") {
		t.Fatalf("expected verification TXT record, got %+v", verificationRecord)
	}
	if verificationRecord.Value != "v=sendwise-verification "+verificationToken {
		t.Fatalf("verification TXT record does not contain its unique token: %+v", verificationRecord)
	}
	if normalizeDkimTXTValue(recordsResp.DNSRecords[1].Value) != normalizeDkimKey(created.DKIMPublicKey) {
		t.Fatalf("DKIM record does not match generated public key: %+v", recordsResp.DNSRecords[1])
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
		"_sendwise-verification." + created.Domain:             {"v=sendwise-verification " + verificationToken},
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
	if len(verifiedResp.DNSRecords) != 5 {
		t.Fatalf("expected five verified/advisory DNS records, got %+v", verifiedResp.DNSRecords)
	}
	if verifiedResp.DNSRecords[3].Status != "found" {
		t.Fatalf("verification TXT record status = %q, want found", verifiedResp.DNSRecords[3].Status)
	}
	if len(hostedDomains) != 1 || hostedDomains[0] != created.Domain {
		t.Fatalf("Stalwart hosted domains = %v, want [%s]", hostedDomains, created.Domain)
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

func TestDeleteSendingDomainRemovesRelatedRecords(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	owner := seedIntegrationUser(
		t, "delete-domain-owner@example.com", "delete-domain-password", "Domain Owner", "Domain Delete Org",
	)
	domain := "delete-" + strings.ReplaceAll(uuid.NewString(), "-", "") + ".example.test"
	var domainID, identityID string
	if err := integrationPool.QueryRow(context.Background(), `
		INSERT INTO sending_domains (organization_id, domain, status)
		VALUES ($1, $2, 'pending')
		RETURNING id
	`, owner.Organization.ID, domain).Scan(&domainID); err != nil {
		t.Fatalf("seed sending domain: %v", err)
	}
	if err := integrationPool.QueryRow(context.Background(), `
		INSERT INTO sender_identities (organization_id, domain, local_part, display_name)
		VALUES ($1, $2, 'news', 'News')
		RETURNING id
	`, owner.Organization.ID, domain).Scan(&identityID); err != nil {
		t.Fatalf("seed sender identity: %v", err)
	}

	other := seedIntegrationUser(
		t, "other-delete-domain-owner@example.com", "delete-domain-password", "Other Owner", "Other Delete Org",
	)
	w := doRequest(t, s, http.MethodDelete, "/api/v1/domains/"+domainID, "", other.AccessToken)
	assertStatus(t, w, http.StatusNotFound)
	var count int
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM sending_domains WHERE id = $1 AND organization_id = $2
	`, domainID, owner.Organization.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("domain after cross-workspace delete = %d, err=%v; want 1", count, err)
	}

	w = doRequest(t, s, http.MethodDelete, "/api/v1/domains/"+domainID, "", owner.AccessToken)
	assertStatus(t, w, http.StatusNoContent)
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM sending_domains WHERE id = $1 AND organization_id = $2
	`, domainID, owner.Organization.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("domain records after delete = %d, err=%v; want 0", count, err)
	}
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT count(*) FROM sender_identities WHERE id = $1 AND organization_id = $2
	`, identityID, owner.Organization.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("sender identities after delete = %d, err=%v; want 0", count, err)
	}
}

func TestStalwartDomainVerificationHostsTenantDomain(t *testing.T) {
	if os.Getenv("RUN_STALWART_INTEGRATION") != "1" {
		t.Skip("set RUN_STALWART_INTEGRATION=1 to run against the local Stalwart container")
	}
	if integrationPool == nil {
		t.Skip("no database available")
	}
	managementURL := os.Getenv("STALWART_URL")
	adminUsername := os.Getenv("STALWART_ADMIN_USERNAME")
	adminPassword := os.Getenv("STALWART_ADMIN_PASSWORD")
	if managementURL == "" || adminUsername == "" || adminPassword == "" {
		t.Fatal("STALWART_URL, STALWART_ADMIN_USERNAME, and STALWART_ADMIN_PASSWORD are required")
	}

	server := newIntegrationServer(t)
	server.cfg.StalwartManagementURL = managementURL
	server.cfg.StalwartAdminUsername = adminUsername
	server.cfg.StalwartAdminPassword = adminPassword
	server.stalwartDomainHoster = stalwartDomainProvisionFunc(func(ctx context.Context, domain string) error {
		return ensureStalwartDomain(ctx, server.cfg, domain)
	})
	domain := "tenant-" + strings.ReplaceAll(uuid.NewString(), "-", "") + ".example.test"
	var organizationID string
	if err := integrationPool.QueryRow(context.Background(), `
		INSERT INTO organizations (name, slug)
		VALUES ($1, 'stalwart-domain-' || gen_random_uuid()::text)
		RETURNING id
	`, "Stalwart tenant domain").Scan(&organizationID); err != nil {
		t.Fatalf("create tenant organization: %v", err)
	}
	token := integrationToken(t, organizationID, "owner")

	response := doRequest(t, server, http.MethodPost, "/api/v1/domains", `{"domain":"`+domain+`"}`, token)
	assertStatus(t, response, http.StatusCreated)
	var created sendingDomain
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created tenant domain: %v", err)
	}
	var verificationToken string
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT verification_token FROM sending_domains WHERE id = $1 AND organization_id = $2
	`, created.ID, organizationID).Scan(&verificationToken); err != nil {
		t.Fatalf("load tenant verification token: %v", err)
	}
	server.dnsResolver = &fakeDNSResolver{records: map[string][]string{
		domain: {"v=spf1 mx ~all"},
		created.DKIMSelector + "._domainkey." + domain: {dkimRecordValue(created.DKIMPublicKey)},
		"_dmarc." + domain:                 {"v=DMARC1; p=quarantine"},
		"_sendwise-verification." + domain: {"v=sendwise-verification " + verificationToken},
	}}
	response = doRequest(t, server, http.MethodPost, "/api/v1/domains/"+created.ID+"/verify", "", token)
	assertStatus(t, response, http.StatusOK)
	var verified struct {
		Domain sendingDomain `json:"domain"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &verified); err != nil {
		t.Fatalf("decode verified tenant domain: %v", err)
	}
	if verified.Domain.Status != "verified" || !verified.Domain.StalwartAuthenticated {
		t.Fatalf("domain verification did not record successful Stalwart hosting: %+v", verified.Domain)
	}

	admin := stalwartAdminClient{
		baseURL: managementURL, username: adminUsername, password: adminPassword, client: http.DefaultClient,
	}
	session, err := admin.request(context.Background(), http.MethodGet, "/jmap/session", nil)
	if err != nil {
		t.Fatalf("load Stalwart management session: %v", err)
	}
	var sessionData struct {
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.Unmarshal(session, &sessionData); err != nil {
		t.Fatalf("decode Stalwart management session: %v", err)
	}
	domainsJSON, err := admin.call(context.Background(), sessionData.PrimaryAccounts["urn:stalwart:jmap"], "x:Domain/get", map[string]any{"ids": nil})
	if err != nil {
		t.Fatalf("read Stalwart domains: %v", err)
	}
	var domains struct {
		List []struct {
			Name           string         `json:"name"`
			DKIMManagement map[string]any `json:"dkimManagement"`
		} `json:"list"`
	}
	if err := json.Unmarshal(domainsJSON, &domains); err != nil {
		t.Fatalf("decode Stalwart domains: %v", err)
	}
	for _, hosted := range domains.List {
		if strings.EqualFold(hosted.Name, domain) {
			if hosted.DKIMManagement["@type"] != "Manual" {
				t.Fatalf("Stalwart DKIM management = %v, want Manual for app-only signing", hosted.DKIMManagement)
			}
			if err := admin.EnsureDomain(context.Background(), domain); err != nil {
				t.Fatalf("re-enforce app-managed DKIM for existing Stalwart domain: %v", err)
			}
			return
		}
	}
	t.Fatalf("verified tenant domain %q is not hosted in Stalwart", domain)
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

	registered := seedIntegrationUser(
		t, "analytics-owner@example.com", "analytics-password", "Analytics Owner", "Analytics Org",
	)
	token := registered.AccessToken
	organizationID := registered.Organization.ID
	ctx := context.Background()
	messagePrefix := uuid.NewString()

	var firstContactID, secondContactID string
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'analytics-one@example.test', 'subscribed')
		RETURNING id
	`, organizationID).Scan(&firstContactID); err != nil {
		t.Fatalf("seed first analytics contact: %v", err)
	}
	if err := integrationPool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		VALUES ($1, 'analytics-two@example.test', 'subscribed')
		RETURNING id
	`, organizationID).Scan(&secondContactID); err != nil {
		t.Fatalf("seed second analytics contact: %v", err)
	}

	campaignResponse := doRequest(t, s, http.MethodPost, "/api/v1/campaigns", `{
		"name":"Analytics seeded campaign",
		"subject":"Seeded report"
	}`, token)
	assertStatus(t, campaignResponse, http.StatusCreated)
	var campaign struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(campaignResponse.Body.Bytes(), &campaign); err != nil || campaign.ID == "" {
		t.Fatalf("decode seeded analytics campaign: id=%q err=%v", campaign.ID, err)
	}
	if _, err := integrationPool.Exec(ctx, `
		UPDATE campaigns SET status = 'completed', recipient_count = 3
		WHERE id = $1 AND organization_id = $2
	`, campaign.ID, organizationID); err != nil {
		t.Fatalf("mark analytics campaign complete: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email, status)
		VALUES
			($1, $2, $3, 'analytics-one@example.test', 'submitted'),
			($1, $2, $4, 'analytics-two@example.test', 'submitted'),
			($1, $2, gen_random_uuid(), 'analytics-bounced@example.test', 'bounced')
	`, organizationID, campaign.ID, firstContactID, secondContactID); err != nil {
		t.Fatalf("seed analytics campaign recipients: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES
			($1, $2, $3, $5 || '-one@test', 'submitted'),
			($1, $2, $4, $5 || '-two@test', 'submitted')
	`, organizationID, campaign.ID, firstContactID, secondContactID, messagePrefix); err != nil {
		t.Fatalf("seed analytics delivery jobs: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO message_events (organization_id, campaign_id, contact_id, event_type, url)
		VALUES
			($1, $2, $3, 'opened', NULL),
			($1, $2, $3, 'clicked', 'https://example.test/offer'),
			($1, $2, $4, 'clicked', 'https://example.test/offer'),
			($1, $2, gen_random_uuid(), 'bounced', NULL)
	`, organizationID, campaign.ID, firstContactID, secondContactID); err != nil {
		t.Fatalf("seed analytics message events: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO suppressions (organization_id, email, reason)
		VALUES
			($1, 'analytics-complaint@example.test', 'complaint'),
			($1, 'analytics-unsubscribe@example.test', 'unsubscribe')
	`, organizationID); err != nil {
		t.Fatalf("seed analytics suppressions: %v", err)
	}

	w := doRequest(t, s, "GET", "/api/v1/analytics/overview", "", token)
	assertStatus(t, w, http.StatusOK)
	var overview struct {
		Contacts      int64   `json:"contacts"`
		Subscribed    int64   `json:"subscribed"`
		CampaignsSent int64   `json:"campaigns_sent"`
		Submitted     int64   `json:"emails_submitted"`
		Opens         int64   `json:"opens"`
		Clicks        int64   `json:"clicks"`
		Bounces       int64   `json:"bounces"`
		Complaints    int64   `json:"complaints"`
		Unsubscribes  int64   `json:"unsubscribes"`
		OpenRate      float64 `json:"open_rate"`
		ClickRate     float64 `json:"click_rate"`
		BounceRate    float64 `json:"bounce_rate"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode analytics overview: %v", err)
	}
	if overview.Contacts != 2 || overview.Subscribed != 2 || overview.CampaignsSent != 1 ||
		overview.Submitted != 2 || overview.Opens != 1 || overview.Clicks != 2 ||
		overview.Bounces != 1 || overview.Complaints != 1 || overview.Unsubscribes != 1 {
		t.Fatalf("overview counters do not match seeded data: %+v", overview)
	}
	if overview.OpenRate != 50 || overview.ClickRate != 100 || overview.BounceRate != 50 {
		t.Fatalf("overview rates do not match submitted deliveries: open=%v click=%v bounce=%v", overview.OpenRate, overview.ClickRate, overview.BounceRate)
	}

	reportResponse := doRequest(t, s, http.MethodGet, "/api/v1/analytics/campaigns/"+campaign.ID, "", token)
	assertStatus(t, reportResponse, http.StatusOK)
	var report struct {
		Recipients struct {
			Total    int64            `json:"total"`
			ByStatus map[string]int64 `json:"by_status"`
		} `json:"recipients"`
		Events struct {
			ByType map[string]int64 `json:"by_type"`
		} `json:"events"`
		Rates struct {
			OpenRate   float64 `json:"open_rate"`
			ClickRate  float64 `json:"click_rate"`
			BounceRate float64 `json:"bounce_rate"`
		} `json:"rates"`
		TopLinks []struct {
			URL    string `json:"url"`
			Clicks int64  `json:"clicks"`
		} `json:"top_links"`
	}
	if err := json.Unmarshal(reportResponse.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode campaign report: %v", err)
	}
	if report.Recipients.Total != 3 || report.Recipients.ByStatus["submitted"] != 2 || report.Recipients.ByStatus["bounced"] != 1 {
		t.Fatalf("campaign recipient totals do not match seeded data: %+v", report.Recipients)
	}
	if report.Events.ByType["opened"] != 1 || report.Events.ByType["clicked"] != 2 || report.Events.ByType["bounced"] != 1 {
		t.Fatalf("campaign event counts do not match seeded data: %+v", report.Events)
	}
	if report.Rates.OpenRate != 33.33 || report.Rates.ClickRate != 66.67 || report.Rates.BounceRate != 33.33 {
		t.Fatalf("campaign report rates do not match recipient totals: %+v", report.Rates)
	}
	if len(report.TopLinks) != 1 || report.TopLinks[0].URL != "https://example.test/offer" || report.TopLinks[0].Clicks != 2 {
		t.Fatalf("campaign top links do not match seeded clicks: %+v", report.TopLinks)
	}
}

func TestAPIKeyLifecycleIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	s := newIntegrationServer(t)
	registered := seedIntegrationUser(
		t, "api-key-owner@example.test", "api-key-integration-password", "API Key Owner", "API Key Workspace",
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
