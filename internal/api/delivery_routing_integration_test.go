package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/delivery"
)

func TestWorkspaceResendSettingsAndDomainOnboarding(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no PostgreSQL database available")
	}
	const fakeAPIKey = "re_fake_not_a_real_key_1234"
	orgA := uuid.NewString()
	orgB := uuid.NewString()
	domainName := "news-" + strings.ReplaceAll(orgA[:8], "-", "") + ".example"
	var creates atomic.Int32
	fakeResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+fakeAPIKey {
			t.Errorf("authorization header = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/domains":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/domains":
			creates.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["name"] != domainName {
				t.Errorf("created domain = %q", body["name"])
			}
			_, _ = fmt.Fprintf(w, `{"id":"domain_fake_1","name":%q,"status":"pending","records":[{"record":"SPF","type":"TXT","name":"send","value":"resend-spf-value","status":"pending"},{"record":"DKIM","type":"TXT","name":"resend._domainkey","value":"resend-dkim-value","status":"pending"}]}`, domainName)
		case r.Method == http.MethodPost && r.URL.Path == "/domains/domain_fake_1/verify":
			_, _ = fmt.Fprintf(w, `{"id":"domain_fake_1","name":%q,"status":"verified","records":[{"record":"SPF","type":"TXT","name":"send","value":"resend-spf-value","status":"verified"},{"record":"DKIM","type":"TXT","name":"resend._domainkey","value":"resend-dkim-value","status":"verified"}]}`, domainName)
		default:
			http.Error(w, "unexpected fake Resend request", http.StatusNotFound)
		}
	}))
	defer fakeResend.Close()

	server := newIntegrationServer(t)
	server.resendDomains = &delivery.ResendTransport{BaseURL: fakeResend.URL, RequestsRate: 1000, Client: fakeResend.Client()}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	tokenA := integrationToken(t, orgA, auth.RoleOwner)
	tokenB := integrationToken(t, orgB, auth.RoleOwner)
	request := func(token, method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, httpServer.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	settingsBody := `{"provider":"resend","resend_api_key":"` + fakeAPIKey + `"}`
	response := request(tokenA, http.MethodPut, "/api/v1/settings/delivery", settingsBody)
	settingsResponse, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save settings status = %d, body = %s", response.StatusCode, settingsResponse)
	}
	if strings.Contains(string(settingsResponse), fakeAPIKey) || !strings.Contains(string(settingsResponse), "1234") {
		t.Fatalf("settings response exposed a key or omitted last-four mask: %s", settingsResponse)
	}
	var encryptedKey []byte
	if err := integrationPool.QueryRow(context.Background(), `
		SELECT resend_api_key_enc FROM workspace_delivery_settings WHERE workspace_id = $1
	`, orgA).Scan(&encryptedKey); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encryptedKey, []byte(fakeAPIKey)) {
		t.Fatal("workspace Resend API key was stored in plaintext")
	}
	decryptedKey, err := delivery.DecryptSecret(integrationCfg.DKIMEncryptionKey, encryptedKey)
	if err != nil || decryptedKey != fakeAPIKey {
		t.Fatalf("decrypt stored workspace key = %q, err = %v", decryptedKey, err)
	}

	response = request(tokenA, http.MethodPost, "/api/v1/settings/delivery/test", "{}")
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("test Resend connection status = %d", response.StatusCode)
	}

	response = request(tokenA, http.MethodPost, "/api/v1/domains", fmt.Sprintf(`{"domain":%q,"provider":"resend"}`, domainName))
	var created sendingDomain
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("create Resend domain status = %d, body = %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if created.Provider != delivery.ProviderResend || created.ResendDomainID != "domain_fake_1" {
		t.Fatalf("created domain = %#v", created)
	}

	response = request(tokenA, http.MethodGet, "/api/v1/domains/"+created.ID+"/dns-records", "")
	var dnsResponse struct {
		Records []dnsRecord `json:"dns_records"`
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("get Resend DNS records status = %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&dnsResponse); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if len(dnsResponse.Records) != 2 || dnsResponse.Records[0].Value != "resend-spf-value" {
		t.Fatalf("DNS records did not use Resend response: %#v", dnsResponse.Records)
	}

	response = request(tokenA, http.MethodPost, "/api/v1/domains/"+created.ID+"/verify", "")
	var verifyResponse struct {
		Domain sendingDomain `json:"domain"`
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("verify Resend domain status = %d, body = %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(&verifyResponse); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if verifyResponse.Domain.Status != "verified" {
		t.Fatalf("verified domain status = %q", verifyResponse.Domain.Status)
	}

	response = request(tokenB, http.MethodPut, "/api/v1/settings/delivery", settingsBody)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save workspace B settings status = %d", response.StatusCode)
	}
	response = request(tokenB, http.MethodPost, "/api/v1/domains", fmt.Sprintf(`{"domain":%q,"provider":"resend"}`, domainName))
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("cross-workspace domain creation status = %d, want 409", response.StatusCode)
	}
	if creates.Load() != 1 {
		t.Fatalf("Resend create called %d times, want 1", creates.Load())
	}
}
