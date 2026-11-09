package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestTemplateLifecycleAndTenantIsolationIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	server := newIntegrationServer(t)
	owner := seedIntegrationUser(
		t, "template-owner@example.test", "template-owner-password", "Template Owner", "Template Workspace",
	)
	other := seedIntegrationUser(
		t, "template-other@example.test", "template-other-password", "Other Owner", "Other Workspace",
	)

	createdResponse := doRequest(t, server, http.MethodPost, "/api/v1/templates", `{
		"name":"Welcome",
		"category":"lifecycle",
		"design":{"version":1,"blocks":[{"type":"heading","content":"Hello"}]}
	}`, owner.AccessToken)
	assertStatus(t, createdResponse, http.StatusCreated)
	var created struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Category string          `json:"category"`
		Design   json.RawMessage `json:"design"`
	}
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created template: %v", err)
	}
	if created.ID == "" || created.Name != "Welcome" || created.Category != "lifecycle" {
		t.Fatalf("unexpected template response: %+v", created)
	}

	getPath := "/api/v1/templates/" + created.ID
	getResponse := doRequest(t, server, http.MethodGet, getPath, "", owner.AccessToken)
	assertStatus(t, getResponse, http.StatusOK)
	otherGet := doRequest(t, server, http.MethodGet, getPath, "", other.AccessToken)
	assertStatus(t, otherGet, http.StatusNotFound)
	otherUpdate := doRequest(t, server, http.MethodPatch, getPath, `{"name":"stolen"}`, other.AccessToken)
	assertStatus(t, otherUpdate, http.StatusNotFound)
	otherDelete := doRequest(t, server, http.MethodDelete, getPath, "", other.AccessToken)
	assertStatus(t, otherDelete, http.StatusNotFound)
	otherCampaign := doRequest(t, server, http.MethodPost, "/api/v1/campaigns", `{
		"name":"Cross-tenant template campaign",
		"template_id":"`+created.ID+`"
	}`, other.AccessToken)
	assertStatus(t, otherCampaign, http.StatusNotFound)

	updatedResponse := doRequest(t, server, http.MethodPatch, getPath, `{
		"name":"Welcome updated",
		"category":"newsletter",
		"design":{"version":1,"blocks":[{"type":"paragraph","content":"Updated design"}]}
	}`, owner.AccessToken)
	assertStatus(t, updatedResponse, http.StatusOK)
	var updated struct {
		Name     string          `json:"name"`
		Category string          `json:"category"`
		Design   json.RawMessage `json:"design"`
	}
	if err := json.Unmarshal(updatedResponse.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated template: %v", err)
	}
	if updated.Name != "Welcome updated" || updated.Category != "newsletter" || !json.Valid(updated.Design) {
		t.Fatalf("template update did not persist all fields: %+v", updated)
	}

	invalidDesign := doRequest(t, server, http.MethodPatch, getPath, `{"design":{"version":`, owner.AccessToken)
	assertStatus(t, invalidDesign, http.StatusBadRequest)

	deleted := doRequest(t, server, http.MethodDelete, getPath, "", owner.AccessToken)
	assertStatus(t, deleted, http.StatusNoContent)
	missing := doRequest(t, server, http.MethodGet, getPath, "", owner.AccessToken)
	assertStatus(t, missing, http.StatusNotFound)
}
