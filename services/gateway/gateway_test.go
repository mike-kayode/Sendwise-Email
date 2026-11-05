package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khayode/email-platform/internal/kit"
)

func TestRoute(t *testing.T) {
	cases := []struct {
		path         string
		wantTarget   string
		wantUpstream string
	}{
		{"/api/v1/auth/login", TargetIdentity, "/auth/login"},
		{"/api/v1/auth/register", TargetIdentity, "/auth/register"},
		{"/api/v1/auth/refresh", TargetIdentity, "/auth/refresh"},
		{"/api/v1/organizations", TargetWorkspace, "/organizations"},
		{"/api/v1/organizations/abc", TargetWorkspace, "/organizations/abc"},
		{"/api/v1/members", TargetWorkspace, "/members"},
		{"/api/v1/invitations", TargetWorkspace, "/invitations"},
		{"/api/v1/contacts", TargetLegacy, "/api/v1/contacts"},
		{"/api/v1/campaigns/1", TargetLegacy, "/api/v1/campaigns/1"},
		{"/api/v1/author-report", TargetLegacy, "/api/v1/author-report"},
		{"/health", TargetLegacy, "/health"},
	}
	for _, tc := range cases {
		target, upstream := route(tc.path)
		if target != tc.wantTarget || upstream != tc.wantUpstream {
			t.Errorf("route(%q) = (%q, %q), want (%q, %q)", tc.path, target, upstream, tc.wantTarget, tc.wantUpstream)
		}
	}
}

// upstreamEcho records what the fake upstream received.
type upstreamEcho struct {
	path    string
	headers http.Header
	host    string
}

func newEchoServer(captured *[]upstreamEcho) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*captured = append(*captured, upstreamEcho{path: r.URL.Path, headers: r.Header.Clone(), host: r.Host})
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
}

func newTestGateway(t *testing.T, cfg Config) (*httptest.Server, *Gateway) {
	t.Helper()
	if cfg.CORSOrigins == nil {
		cfg.CORSOrigins = []string{"http://localhost:3000"}
	}
	g, err := New(cfg, kit.NewTestLogger())
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	server := httptest.NewServer(g)
	t.Cleanup(server.Close)
	return server, g
}

func TestGatewayRoutesAndStripsSpoofedHeaders(t *testing.T) {
	var identityHits, workspaceHits, legacyHits []upstreamEcho
	identity := newEchoServer(&identityHits)
	defer identity.Close()
	workspace := newEchoServer(&workspaceHits)
	defer workspace.Close()
	legacy := newEchoServer(&legacyHits)
	defer legacy.Close()

	server, _ := newTestGateway(t, Config{
		IdentityURL:  identity.URL,
		WorkspaceURL: workspace.URL,
		LegacyAPIURL: legacy.URL,
	})

	// Auth request → identity with stripped identity and service headers.
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", strings.NewReader(`{"email":"a@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token-123")
	req.Header.Set("X-User-ID", "forged-user")
	req.Header.Set("X-Org-ID", "forged-org")
	req.Header.Set("X-Role", "owner")
	req.Header.Set("X-Service-Token", "forged-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	resp.Body.Close()

	if len(identityHits) != 1 {
		t.Fatalf("identity hits = %d", len(identityHits))
	}
	got := identityHits[0]
	if got.path != "/auth/login" {
		t.Errorf("identity path = %q, want /auth/login", got.path)
	}
	if got.headers.Get("Authorization") != "Bearer token-123" {
		t.Errorf("Authorization was not forwarded: %q", got.headers.Get("Authorization"))
	}
	for _, header := range []string{"X-User-ID", "X-Org-ID", "X-Role", "X-Service-Token"} {
		if got.headers.Get(header) != "" {
			t.Errorf("%s should be stripped, got %q", header, got.headers.Get(header))
		}
	}
	if got.headers.Get("X-Forwarded-For") == "" {
		t.Error("expected X-Forwarded-For to be set by the gateway")
	}

	// Workspace request.
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/organizations", nil)
	req.Header.Set("Authorization", "Bearer token-123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("organizations request: %v", err)
	}
	resp.Body.Close()
	if len(workspaceHits) != 1 || workspaceHits[0].path != "/organizations" {
		t.Fatalf("workspace hits = %+v", workspaceHits)
	}

	// Legacy request keeps the full public path.
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/api/v1/contacts?limit=10", nil)
	req.Header.Set("Authorization", "Bearer token-123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("contacts request: %v", err)
	}
	resp.Body.Close()
	if len(legacyHits) != 1 || legacyHits[0].path != "/api/v1/contacts" {
		t.Fatalf("legacy hits = %+v", legacyHits)
	}
}

func TestGatewayAuthRateLimit(t *testing.T) {
	var identityHits []upstreamEcho
	identity := newEchoServer(&identityHits)
	defer identity.Close()
	var workspaceHits, legacyHits []upstreamEcho
	workspace := newEchoServer(&workspaceHits)
	defer workspace.Close()
	legacy := newEchoServer(&legacyHits)
	defer legacy.Close()

	server, _ := newTestGateway(t, Config{
		IdentityURL:       identity.URL,
		WorkspaceURL:      workspace.URL,
		LegacyAPIURL:      legacy.URL,
		AuthRatePerMinute: 60,
		AuthBurst:         2,
	})

	limited := false
	for i := 0; i < 5; i++ {
		resp, err := http.Post(server.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("expected auth requests to be rate limited")
	}
}

func TestGatewayHealthAndCORS(t *testing.T) {
	var identityHits, workspaceHits, legacyHits []upstreamEcho
	identity := newEchoServer(&identityHits)
	defer identity.Close()
	workspace := newEchoServer(&workspaceHits)
	defer workspace.Close()
	legacy := newEchoServer(&legacyHits)
	defer legacy.Close()

	server, _ := newTestGateway(t, Config{
		IdentityURL:  identity.URL,
		WorkspaceURL: workspace.URL,
		LegacyAPIURL: legacy.URL,
	})

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodOptions, server.URL+"/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("CORS header = %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	// Preflight must not reach upstreams.
	if len(identityHits)+len(workspaceHits)+len(legacyHits) != 0 {
		t.Fatalf("preflight leaked to upstreams: %d", len(identityHits)+len(workspaceHits)+len(legacyHits))
	}
}

// setProdEnv pins every env var LoadConfig reads so ambient values cannot
// leak into fail-fast assertions.
func setProdEnv(t *testing.T, env string, overrides map[string]string) {
	t.Helper()
	base := map[string]string{
		"APP_ENV":                      env,
		"IDENTITY_URL":                 "http://identity:8080",
		"WORKSPACE_URL":                "http://workspace:8080",
		"LEGACY_API_URL":               "http://api:8080",
		"SERVICE_TOKEN":                "test-service-token",
		"CORS_ORIGINS":                 "http://localhost:3000",
		"GATEWAY_AUTH_RATE_PER_MINUTE": "60",
		"GATEWAY_AUTH_BURST":           "20",
	}
	for k, v := range overrides {
		base[k] = v
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
}

func TestLoadConfigFailsFastInProduction(t *testing.T) {
	cases := []struct {
		name    string
		missing string
		wantErr string
	}{
		{"identity url", "IDENTITY_URL", "IDENTITY_URL"},
		{"workspace url", "WORKSPACE_URL", "WORKSPACE_URL"},
		{"legacy api url", "LEGACY_API_URL", "LEGACY_API_URL"},
		{"service token", "SERVICE_TOKEN", "SERVICE_TOKEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setProdEnv(t, "production", map[string]string{tc.missing: ""})
			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("expected error when %s is missing in production", tc.missing)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadConfigProductionSucceedsWhenComplete(t *testing.T) {
	setProdEnv(t, "production", nil)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.LegacyAPIURL != "http://api:8080" || cfg.ServiceToken != "test-service-token" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadConfigDevelopmentAllowsMissingUpstreams(t *testing.T) {
	setProdEnv(t, "development", map[string]string{
		"IDENTITY_URL": "", "WORKSPACE_URL": "", "LEGACY_API_URL": "", "SERVICE_TOKEN": "",
	})
	if _, err := LoadConfig(); err != nil {
		t.Fatalf("development must not fail fast: %v", err)
	}
}

// newHealthServer serves /healthz; closed servers simulate a down upstream.
func newHealthServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestReadyzReportsLegacyReachability(t *testing.T) {
	identity := newHealthServer()
	defer identity.Close()
	workspace := newHealthServer()
	defer workspace.Close()
	legacy := newHealthServer()

	server, _ := newTestGateway(t, Config{
		IdentityURL:  identity.URL,
		WorkspaceURL: workspace.URL,
		LegacyAPIURL: legacy.URL,
	})

	// All up → 200 and every upstream reported.
	resp, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("readyz: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz status = %d body = %s", resp.StatusCode, body)
	}
	for _, name := range []string{"identity", "workspace", "legacy"} {
		if !strings.Contains(string(body), `"`+name+`":"up"`) {
			t.Errorf("readyz body missing %s: %s", name, body)
		}
	}

	// Legacy down → 503 naming legacy (regression: legacy used to be optional).
	legacy.Close()
	resp, err = http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("readyz after legacy down: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503 when legacy is down; body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "legacy") {
		t.Errorf("503 body must name the down upstream: %s", body)
	}
}
