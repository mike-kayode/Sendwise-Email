package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAdminDomainClientUsesServiceAuthAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin/domains" {
			t.Errorf("path = %q, want internal admin domain path", r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		if r.URL.Query().Get("search") != "client mail" || r.URL.Query().Get("status") != "verified" ||
			r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("offset") != "4" {
			t.Errorf("query = %s, want encoded search and pagination", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []AdminSendingDomain{{
				ID: "domain-1", WorkspaceID: "workspace-1", Domain: "mail.example.test",
				Status: "verified", SPFStatus: "verified", DKIMStatus: "verified",
			}},
			"pagination": map[string]int{"total": 9, "limit": 2, "offset": 4},
		})
	}))
	defer server.Close()

	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")
	domains, total, err := client.AdminDomains(context.Background(), "client mail", "verified", false, 2, 4)
	if err != nil {
		t.Fatalf("AdminDomains() error = %v", err)
	}
	if total != 9 || len(domains) != 1 || domains[0].Domain != "mail.example.test" {
		t.Fatalf("AdminDomains() = %+v, total %d; want domain metadata and total 9", domains, total)
	}
}

func TestHTTPAdminSuppressionClientUsesServiceAuthAndMapsConflicts(t *testing.T) {
	const suppressionID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/internal/admin/suppressions":
			if r.URL.Query().Get("search") != "blocked" ||
				r.URL.Query().Get("workspace_id") != suppressionID ||
				r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("offset") != "4" {
				t.Errorf("suppression query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"` + suppressionID + `","email":"blocked@example.test"}],"pagination":{"total":1}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/internal/admin/suppressions":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body["workspace_id"] != suppressionID || body["email"] != "blocked@example.test" {
				if body["workspace_id"] != "conflict" || body["email"] != "blocked@example.test" {
					t.Errorf("create body = %+v", body)
				}
			}
			if body["workspace_id"] == "conflict" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"` + suppressionID + `","email":"blocked@example.test"}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/internal/admin/suppressions/"+suppressionID:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")

	items, total, err := client.AdminSuppressions(context.Background(), "blocked", suppressionID, 2, 4)
	if err != nil || total != 1 || len(items) != 1 || items[0].Email != "blocked@example.test" {
		t.Fatalf("AdminSuppressions() = %+v, total=%d, err=%v", items, total, err)
	}
	created, err := client.CreateAdminSuppression(context.Background(), suppressionID, "blocked@example.test")
	if err != nil || created.ID != suppressionID {
		t.Fatalf("CreateAdminSuppression() = %+v, err=%v", created, err)
	}
	if err := client.DeleteAdminSuppression(context.Background(), suppressionID); err != nil {
		t.Fatalf("DeleteAdminSuppression() error = %v", err)
	}
	if _, err := client.CreateAdminSuppression(context.Background(), "conflict", "blocked@example.test"); !errors.Is(err, ErrAdminSuppressionConflict) {
		t.Fatalf("CreateAdminSuppression conflict error = %v", err)
	}
}

func TestHTTPAdminDomainDisableUsesServiceAuthAndMapsNotFound(t *testing.T) {
	const domainID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/internal/admin/domains/"+domainID+"/disabled" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		var body map[string]bool
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode domain status body: %v", err)
		}
		if !body["disabled"] {
			t.Errorf("domain status body = %+v, want disabled=true", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + domainID + `","domain":"mail.example.test","admin_disabled_at":"2026-11-14T12:00:00Z"}}`))
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")
	domain, err := client.SetAdminDomainDisabled(context.Background(), domainID, true)
	if err != nil || domain.ID != domainID || domain.AdminDisabledAt == nil {
		t.Fatalf("SetAdminDomainDisabled() = %+v, err=%v", domain, err)
	}

	notFoundServer := httptest.NewServer(http.NotFoundHandler())
	defer notFoundServer.Close()
	notFoundClient := NewHTTPLegacyAPIAdminClient(notFoundServer.URL, "service-token")
	if _, err := notFoundClient.SetAdminDomainDisabled(context.Background(), domainID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetAdminDomainDisabled not-found error = %v, want ErrNotFound", err)
	}
	deleteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/internal/admin/domains/"+domainID {
			t.Errorf("delete request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("delete service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer deleteServer.Close()
	deleteClient := NewHTTPLegacyAPIAdminClient(deleteServer.URL, "service-token")
	if err := deleteClient.DeleteAdminDomain(context.Background(), domainID); err != nil {
		t.Fatalf("DeleteAdminDomain() error = %v", err)
	}
	conflictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer conflictServer.Close()
	conflictClient := NewHTTPLegacyAPIAdminClient(conflictServer.URL, "service-token")
	if err := conflictClient.DeleteAdminDomain(context.Background(), domainID); !errors.Is(err, ErrAdminDomainConflict) {
		t.Fatalf("DeleteAdminDomain conflict error = %v, want ErrAdminDomainConflict", err)
	}
}

func TestHTTPAdminWorkspaceStatusUsesServiceAuthAndMapsConflicts(t *testing.T) {
	const workspaceID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		if r.URL.Path != "/internal/admin/workspaces/"+workspaceID+"/status" {
			t.Errorf("workspace status path = %q", r.URL.Path)
		}
		if r.Method == http.MethodPatch {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode workspace status body: %v", err)
			}
			if body["status"] != "suspended" || body["expected_status"] != "active" {
				t.Errorf("workspace status body = %+v, want suspended from active", body)
			}
		}
		_, _ = w.Write([]byte(`{"status":"suspended"}`))
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")
	status, err := client.AdminWorkspaceStatus(context.Background(), workspaceID)
	if err != nil || status != "suspended" {
		t.Fatalf("AdminWorkspaceStatus() = %q, err=%v", status, err)
	}
	status, err = client.SetAdminWorkspaceStatus(context.Background(), workspaceID, "active", "suspended")
	if err != nil || status != "suspended" {
		t.Fatalf("SetAdminWorkspaceStatus() = %q, err=%v", status, err)
	}

	notFoundServer := httptest.NewServer(http.NotFoundHandler())
	defer notFoundServer.Close()
	notFoundClient := NewHTTPLegacyAPIAdminClient(notFoundServer.URL, "service-token")
	if _, err := notFoundClient.AdminWorkspaceStatus(context.Background(), workspaceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AdminWorkspaceStatus not-found error = %v, want ErrNotFound", err)
	}
	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"workspace_closed"}}`))
	}))
	defer closedServer.Close()
	closedClient := NewHTTPLegacyAPIAdminClient(closedServer.URL, "service-token")
	if _, err := closedClient.SetAdminWorkspaceStatus(context.Background(), workspaceID, "suspended", "active"); !errors.Is(err, ErrAdminWorkspaceClosed) {
		t.Fatalf("SetAdminWorkspaceStatus conflict error = %v, want ErrAdminWorkspaceClosed", err)
	}
}

func TestHTTPAdminDomainConflictResolutionUsesServiceAuth(t *testing.T) {
	const domainID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	const rightfulID = "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/internal/admin/domains/"+domainID+"/resolve-conflict" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode resolution body: %v", err)
		}
		if body["rightful_domain_id"] != rightfulID {
			t.Errorf("resolution body = %+v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"` + rightfulID + `","domain":"mail.example.test"}}`))
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")
	domain, err := client.ResolveAdminDomainConflict(context.Background(), domainID, rightfulID)
	if err != nil || domain.ID != rightfulID || domain.Domain != "mail.example.test" {
		t.Fatalf("ResolveAdminDomainConflict() = %+v, err=%v", domain, err)
	}
}

func TestHTTPAdminDomainDetailsClientReturnsResolversAndMapsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}

		switch r.URL.Path {
		case "/internal/admin/domains/00000000-0000-4000-8000-000000000001":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"id":"00000000-0000-4000-8000-000000000001","domain":"mail.example.test","dns_check_details":{"records":{"spf":{"resolvers":[{"resolver":"1.1.1.1","status":"found"}]}}}}}`))
				return
			}
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		case "/internal/admin/domains/00000000-0000-4000-8000-000000000002":
			http.NotFound(w, r)
		case "/internal/admin/domains/00000000-0000-4000-8000-000000000001/verify":
			if r.Method != http.MethodPost {
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"domain":{"id":"00000000-0000-4000-8000-000000000001","domain":"mail.example.test","status":"verified","dns_check_details":{"records":{"spf":{"resolvers":[{"resolver":"1.1.1.1","status":"found"}]}}}}}`))
		case "/internal/admin/domains/00000000-0000-4000-8000-000000000003/verify":
			http.Error(w, "ownership conflict", http.StatusConflict)
		default:
			http.Error(w, "unexpected path", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")

	domain, err := client.AdminDomain(context.Background(), "00000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatalf("AdminDomain() error = %v", err)
	}
	if domain.Domain != "mail.example.test" || !strings.Contains(string(domain.DNSCheckDetails), `"resolver":"1.1.1.1"`) {
		t.Fatalf("AdminDomain() = %+v, want DNS resolver details", domain)
	}
	if _, err := client.AdminDomain(context.Background(), "00000000-0000-4000-8000-000000000002"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing AdminDomain() error = %v, want ErrNotFound", err)
	}
	updated, err := client.RecheckAdminDomain(context.Background(), "00000000-0000-4000-8000-000000000001")
	if err != nil || updated.Status != "verified" {
		t.Fatalf("RecheckAdminDomain() = %+v, %v; want verified result", updated, err)
	}
	if _, err := client.RecheckAdminDomain(context.Background(), "00000000-0000-4000-8000-000000000003"); !errors.Is(err, ErrAdminDomainConflict) {
		t.Fatalf("conflicting RecheckAdminDomain() error = %v; want ErrAdminDomainConflict", err)
	}
}

func TestHTTPAdminCampaignClientUsesServiceAuthAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin/campaigns" {
			t.Errorf("path = %q, want internal admin campaign path", r.URL.Path)
		}

		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		if r.URL.Query().Get("search") != "weekly update" || r.URL.Query().Get("status") != "completed" ||
			r.URL.Query().Get("workspace_id") != "workspace-id" || r.URL.Query().Get("limit") != "2" ||
			r.URL.Query().Get("offset") != "4" {
			t.Errorf("query = %s, want encoded search and campaign filters", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"campaign-id","name":"Weekly update","status":"completed","recipient_count":8,"submitted_count":7}],"pagination":{"total":9,"limit":2,"offset":4}}`))
	}))
	defer server.Close()

	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")
	campaigns, total, err := client.AdminCampaigns(context.Background(), "weekly update", "completed", "workspace-id", 2, 4)
	if err != nil {
		t.Fatalf("AdminCampaigns() error = %v", err)
	}
	if total != 9 || len(campaigns) != 1 || campaigns[0].Name != "Weekly update" ||
		campaigns[0].SubmittedCount != 7 {
		t.Fatalf("AdminCampaigns() = %+v, total %d; want campaign metadata and total 9", campaigns, total)
	}
}

func TestHTTPAdminCampaignStopUsesServiceAuthAndMapsConflicts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("admin campaign stop request = %s with token %q", r.Method, r.Header.Get("X-Service-Token"))
		}

		switch r.URL.Path {
		case "/internal/admin/campaigns/00000000-0000-4000-8000-000000000001/stop":
			w.WriteHeader(http.StatusOK)
		case "/internal/admin/campaigns/00000000-0000-4000-8000-000000000002/stop":
			http.Error(w, "not stoppable", http.StatusConflict)
		case "/internal/admin/campaigns/00000000-0000-4000-8000-000000000003/stop":
			http.NotFound(w, r)
		default:
			http.Error(w, "unexpected path", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")

	if err := client.StopAdminCampaign(context.Background(), "00000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatalf("StopAdminCampaign() error = %v", err)
	}
	if err := client.StopAdminCampaign(context.Background(), "00000000-0000-4000-8000-000000000002"); !errors.Is(err, ErrAdminCampaignConflict) {
		t.Fatalf("non-stoppable campaign error = %v, want ErrAdminCampaignConflict", err)
	}
	if err := client.StopAdminCampaign(context.Background(), "00000000-0000-4000-8000-000000000003"); !errors.Is(err, ErrAdminCampaignNotFound) {
		t.Fatalf("missing campaign error = %v, want ErrAdminCampaignNotFound", err)
	}
}

func TestHTTPAdminTransactionalMessageSearchUsesServiceAuthAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin/transactional-messages" || r.Method != http.MethodGet {
			t.Errorf("request = %s %s, want transactional admin search", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		if r.URL.Query().Get("search") != "mail id" || r.URL.Query().Get("limit") != "2" ||
			r.URL.Query().Get("offset") != "4" {
			t.Errorf("query = %s, want encoded search and pagination", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"message-id","message_id":"mail id","workspace_id":"workspace-id","workspace_name":"Trading","status":"delivered"}],"pagination":{"total":3,"limit":2,"offset":4}}`))
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")

	messages, total, err := client.SearchAdminTransactionalMessages(context.Background(), "mail id", 2, 4)
	if err != nil {
		t.Fatalf("SearchAdminTransactionalMessages() error = %v", err)
	}
	if total != 3 || len(messages) != 1 || messages[0].MessageID != "mail id" ||
		messages[0].Workspace != "Trading" || messages[0].Status != "delivered" {
		t.Fatalf("transactional search = %+v, total %d; want metadata and total 3", messages, total)
	}
}

func TestHTTPAdminOperationalMetricsUsesServiceAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin/overview" || r.Method != http.MethodGet {
			t.Errorf("request = %s %s, want admin overview", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "service-token" {
			t.Errorf("service token = %q, want configured token", r.Header.Get("X-Service-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"domain_count":3,"campaign_count":4,"messages_today":2,"messages_7_days":8,"messages_30_days":10,"delivery_rate_30_days":80,"bounce_rate_30_days":10,"complaint_rate_30_days":2,"queue_depth":1,"updated_at":"2026-11-14T12:00:00Z"}}`))
	}))
	defer server.Close()
	client := NewHTTPLegacyAPIAdminClient(server.URL, "service-token")

	metrics, err := client.AdminOperationalMetrics(context.Background())
	if err != nil {
		t.Fatalf("AdminOperationalMetrics() error = %v", err)
	}
	if metrics.DomainCount != 3 || metrics.CampaignCount != 4 ||
		metrics.Messages30Days != 10 || metrics.QueueDepth != 1 ||
		metrics.DeliveryRate30d != 80 || metrics.BounceRate30d != 10 ||
		metrics.ComplaintRate30d != 2 || metrics.UpdatedAt.IsZero() {
		t.Fatalf("operational metrics = %+v, want decoded live metric values", metrics)
	}
}
