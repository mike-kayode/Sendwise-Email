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

// TestCampaignDomainsAreStalwartOnly proves there is no per-workspace provider
// selection left: the delivery-settings endpoint is gone and a domain can only
// be created through the Stalwart path.
func TestCampaignDomainsAreStalwartOnly(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no PostgreSQL database available")
	}
	orgA := uuid.NewString()
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

	// The workspace delivery-settings endpoint no longer exists.
	response := request(tokenA, http.MethodPut, "/api/v1/settings/delivery", `{}`)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("workspace delivery settings status = %d, body = %s, want 404", response.StatusCode, body)
	}

	// The plain Stalwart path still creates the domain.
	otherDomain := "news-" + strings.ReplaceAll(orgA[9:17], "-", "") + ".example"
	response = request(tokenA, http.MethodPost, "/api/v1/domains", fmt.Sprintf(`{"domain":%q}`, otherDomain))
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("domain creation status = %d, body = %s", response.StatusCode, body)
	}
	var created struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created domain: %v body=%s", err, body)
	}
	if created.Domain != otherDomain {
		t.Fatalf("created domain = %q, want %q", created.Domain, otherDomain)
	}
}
