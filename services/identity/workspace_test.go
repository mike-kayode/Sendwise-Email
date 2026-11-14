package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceClientAdminOrganizationsUsesServiceAuthAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin/organizations" {
			t.Errorf("workspace path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "workspace-secret" {
			t.Errorf("service token header = %q", r.Header.Get("X-Service-Token"))
		}
		if r.URL.Query().Get("search") != "Acme + Co" ||
			r.URL.Query().Get("limit") != "25" || r.URL.Query().Get("offset") != "50" {
			t.Errorf("workspace query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []WorkspaceAdminOrganization{{
				ID: "workspace-1", Name: "Acme + Co", Slug: "acme", MemberCount: 2,
			}},
			"pagination": map[string]int{"total": 51, "limit": 25, "offset": 50},
		})
	}))
	defer server.Close()

	client := NewHTTPWorkspaceClient(server.URL, "workspace-secret")
	organizations, total, err := client.AdminOrganizations(context.Background(), "Acme + Co", 25, 50)
	if err != nil {
		t.Fatalf("AdminOrganizations() error = %v", err)
	}
	if total != 51 || len(organizations) != 1 ||
		organizations[0].Name != "Acme + Co" || organizations[0].MemberCount != 2 {
		t.Fatalf("AdminOrganizations() = %+v, total %d", organizations, total)
	}
}

func TestWorkspaceClientAdminOrganizationLoadsMembershipMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin/organizations/workspace-1" {
			t.Errorf("workspace path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Service-Token") != "workspace-secret" {
			t.Errorf("service token header = %q", r.Header.Get("X-Service-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"workspace":{"id":"workspace-1","name":"Acme","slug":"acme"},
			"members":[{"organization_id":"workspace-1","user_id":"user-1","role":"owner"}]
		}`))
	}))
	defer server.Close()

	client := NewHTTPWorkspaceClient(server.URL, "workspace-secret")
	workspace, err := client.AdminOrganization(context.Background(), "workspace-1")
	if err != nil {
		t.Fatalf("AdminOrganization() error = %v", err)
	}
	if workspace.ID != "workspace-1" || workspace.MemberCount != 1 ||
		len(workspace.Members) != 1 || workspace.Members[0].Role != "owner" {
		t.Fatalf("AdminOrganization() = %+v", workspace)
	}
}
