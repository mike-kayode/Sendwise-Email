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
	"testing"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/delivery"
)

type integrationProviderFunc func(context.Context, delivery.Message) (*delivery.Result, error)

func (f integrationProviderFunc) Send(ctx context.Context, message delivery.Message) (*delivery.Result, error) {
	return f(ctx, message)
}

func TestWorkspaceCampaignRoutingRejectsResendSettings(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no PostgreSQL database available")
	}
	const fakeAPIKey = "re_fake_not_a_real_key_1234"
	orgA := uuid.NewString()
	domainName := "news-" + strings.ReplaceAll(orgA[:8], "-", "") + ".example"
	server := newIntegrationServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	tokenA := integrationToken(t, orgA, auth.RoleOwner)
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
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("resend campaign setting status = %d, body = %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "campaign_delivery_stalwart_only") {
		t.Fatalf("expected Stalwart-only rejection, got %s", body)
	}

	response = request(tokenA, http.MethodPost, "/api/v1/domains", fmt.Sprintf(`{"domain":%q,"provider":"resend"}`, domainName))
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("resend domain creation status = %d, body = %s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "campaign_delivery_stalwart_only") {
		t.Fatalf("expected Stalwart-only domain rejection, got %s", body)
	}

	response = request(tokenA, http.MethodPut, "/api/v1/settings/delivery", `{"provider":"stalwart"}`)
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stalwart settings status = %d, body = %s", response.StatusCode, body)
	}

	response = request(tokenA, http.MethodPost, "/api/v1/domains", fmt.Sprintf(`{"domain":%q,"provider":"stalwart"}`, domainName))
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("stalwart domain creation status = %d, body = %s", response.StatusCode, body)
	}
	var created struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created domain: %v body=%s", err, body)
	}
	if created.Domain != domainName {
		t.Fatalf("created domain = %q, want %q", created.Domain, domainName)
	}
}
