package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFormsLifecyclePublicSubmitAndTenantIsolationIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("set TEST_DATABASE_URL or INTEGRATION_DATABASE_URL to run form integration tests")
	}
	s := newIntegrationServer(t)
	s.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	userID, orgID, otherOrgID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	userEmail := userID + "@forms-integration.invalid"
	orgSlug, otherOrgSlug := "forms-"+strings.ReplaceAll(orgID, "-", ""), "forms-other-"+strings.ReplaceAll(otherOrgID, "-", "")
	if _, err := integrationPool.Exec(t.Context(), `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'unused', 'Forms Test', 'active')
	`, userID, userEmail); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	if _, err := integrationPool.Exec(t.Context(), `
		INSERT INTO organizations (id, name, slug) VALUES ($1, 'Forms Test', $2)
	`, orgID, orgSlug); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	if _, err := integrationPool.Exec(t.Context(), `
		INSERT INTO organizations (id, name, slug) VALUES ($1, 'Other Forms Test', $2)
	`, otherOrgID, otherOrgSlug); err != nil {
		t.Fatalf("create other organization: %v", err)
	}
	if _, err := integrationPool.Exec(t.Context(), `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner'), ($3, $2, 'owner')
	`, orgID, userID, otherOrgID); err != nil {
		t.Fatalf("create test memberships: %v", err)
	}
	t.Cleanup(func() {
		if _, err := integrationPool.Exec(context.Background(), `DELETE FROM organizations WHERE id = ANY($1::uuid[])`, []string{orgID, otherOrgID}); err != nil {
			t.Errorf("clean up test organizations: %v", err)
		}
		if _, err := integrationPool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("clean up test user: %v", err)
		}
	})
	token, err := integrationTokens.IssueAccess(userID, orgID, "owner")
	if err != nil {
		t.Fatalf("issue owner token: %v", err)
	}
	otherToken, err := integrationTokens.IssueAccess(userID, otherOrgID, "owner")
	if err != nil {
		t.Fatalf("issue other organization token: %v", err)
	}
	var listID, tagID string
	if err := integrationPool.QueryRow(t.Context(), `
		INSERT INTO contact_lists (organization_id, name) VALUES ($1, 'Form audience') RETURNING id::text
	`, orgID).Scan(&listID); err != nil {
		t.Fatalf("create form list: %v", err)
	}
	if err := integrationPool.QueryRow(t.Context(), `
		INSERT INTO tags (organization_id, name) VALUES ($1, 'Form source') RETURNING id::text
	`, orgID).Scan(&tagID); err != nil {
		t.Fatalf("create form tag: %v", err)
	}

	createBody := fmt.Sprintf(`{
		"name":"Collect Form %s",
		"description":"Integration form",
		"list_id":%q,
		"success_message":"You are on the list.",
		"consent_text":"I agree to receive email.",
		"spam_protection":true,
		"double_opt_in":true,
		"tag_ids":[%q],
		"fields":[
			{"field_type":"email","label":"Email","required":true},
			{"field_type":"first_name","label":"First name"},
			{"field_type":"custom","custom_key":"company","label":"Company"}
		]
	}`, strings.ReplaceAll(uuid.NewString(), "-", ""), listID, tagID)
	w := doRequest(t, s, http.MethodPost, "/api/v1/forms", createBody, token)
	assertStatus(t, w, http.StatusCreated)
	var created form
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created form: %v", err)
	}
	if created.ID == "" || created.Status != "draft" || len(created.Fields) != 3 || len(created.TagIDs) != 1 {
		t.Fatalf("created form missing saved configuration: %+v", created)
	}

	collisionBody, err := json.Marshal(formInput{
		Name: created.Name,
		Fields: []formFieldInput{
			{FieldType: "email", Label: "Email", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("encode cross-organization form: %v", err)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/forms", string(collisionBody), otherToken)
	assertStatus(t, w, http.StatusCreated)
	var otherForm form
	if err := json.Unmarshal(w.Body.Bytes(), &otherForm); err != nil {
		t.Fatalf("decode cross-organization form: %v", err)
	}
	if otherForm.Slug == created.Slug {
		t.Fatalf("public form slug %q was reused across organizations", created.Slug)
	}
	_, err = integrationPool.Exec(t.Context(), `
		UPDATE forms SET slug = $1 WHERE id = $2
	`, created.Slug, otherForm.ID)
	if !isUniqueViolation(err) {
		t.Fatalf("global slug uniqueness error = %v, want unique violation", err)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/forms/"+otherForm.ID+"/publish", "", otherToken)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodGet, "/api/v1/public/forms/"+otherForm.Slug, "", "")
	assertStatus(t, w, http.StatusOK)

	w = doRequest(t, s, http.MethodGet, "/api/v1/forms/"+created.ID, "", otherToken)
	assertStatus(t, w, http.StatusNotFound)
	w = doRequest(t, s, http.MethodGet, "/api/v1/forms/"+created.ID, "", token)
	assertStatus(t, w, http.StatusOK)
	var loaded form
	if err := json.Unmarshal(w.Body.Bytes(), &loaded); err != nil {
		t.Fatalf("decode form: %v", err)
	}
	if loaded.ListID != listID || len(loaded.Fields) != 3 {
		t.Fatalf("form configuration did not round-trip: %+v", loaded)
	}

	updateBody := strings.Replace(createBody, "Collect Form ", "Updated Form ", 1)
	w = doRequest(t, s, http.MethodPatch, "/api/v1/forms/"+created.ID, updateBody, token)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodPost, "/api/v1/forms/"+created.ID+"/publish", "", token)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodGet, "/api/v1/public/forms/"+created.Slug, "", "")
	assertStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), orgID) {
		t.Fatal("public form response exposed its organization ID")
	}

	w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
		`{"data":{"first_name":"Ada","company":"Analytical Engines"},"consent":true}`, "")
	assertStatus(t, w, http.StatusBadRequest)
	w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
		`{"data":{"email":"ada@example.test","first_name":"Ada","company":"Analytical Engines"}}`, "")
	assertStatus(t, w, http.StatusBadRequest)
	w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
		`{"website":"https://spam.example"}`, "")
	assertStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "awaiting_confirmation") {
		t.Fatal("honeypot submission was not silently discarded")
	}

	var confirmURL string
	s.formConfirmationSender = func(_ context.Context, _, _, url string) error {
		confirmURL = url
		return nil
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
		`{"data":{"email":"ada@example.test","first_name":"Ada","company":"Analytical Engines"},"consent":true}`, "")
	assertStatus(t, w, http.StatusAccepted)
	if confirmURL == "" {
		t.Fatal("double opt-in email sender was not invoked")
	}
	var contactID, contactStatus string
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT id::text, status FROM contacts WHERE organization_id = $1 AND lower(email) = $2
	`, orgID, "ada@example.test").Scan(&contactID, &contactStatus); err != nil {
		t.Fatalf("load pending contact: %v", err)
	}
	if contactStatus != "pending" {
		t.Fatalf("contact status before confirmation = %q, want pending", contactStatus)
	}
	var membershipCount, tagCount int
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM contact_list_members WHERE organization_id = $1 AND contact_id = $2
	`, orgID, contactID).Scan(&membershipCount); err != nil {
		t.Fatalf("count pending contact list membership: %v", err)
	}
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM contact_tags WHERE organization_id = $1 AND contact_id = $2
	`, orgID, contactID).Scan(&tagCount); err != nil {
		t.Fatalf("count pending contact tags: %v", err)
	}
	if membershipCount != 0 || tagCount != 0 {
		t.Fatalf("pending contact has list/tag membership before confirmation: list=%d tag=%d", membershipCount, tagCount)
	}

	parsedConfirm, err := url.Parse(confirmURL)
	if err != nil {
		t.Fatalf("parse confirmation URL: %v", err)
	}
	w = doRequest(t, s, http.MethodGet, "/api/v1/public/forms/"+created.Slug+"/confirm?token="+url.QueryEscape(parsedConfirm.Query().Get("token")), "", "")
	assertStatus(t, w, http.StatusOK)
	if assertJSONBody(t, w, "status") != `"confirmed"` {
		t.Fatalf("unexpected confirmation response: %s", w.Body.String())
	}
	w = doRequest(t, s, http.MethodGet, "/api/v1/public/forms/"+created.Slug+"/confirm?token="+url.QueryEscape(parsedConfirm.Query().Get("token")), "", "")
	assertStatus(t, w, http.StatusNotFound)
	if err := integrationPool.QueryRow(t.Context(), `SELECT status FROM contacts WHERE id = $1`, contactID).Scan(&contactStatus); err != nil {
		t.Fatalf("load confirmed contact: %v", err)
	}
	if contactStatus != "subscribed" {
		t.Fatalf("contact status after confirmation = %q, want subscribed", contactStatus)
	}
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM contact_list_members WHERE organization_id = $1 AND contact_id = $2
	`, orgID, contactID).Scan(&membershipCount); err != nil {
		t.Fatalf("count confirmed contact list membership: %v", err)
	}
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM contact_tags WHERE organization_id = $1 AND contact_id = $2
	`, orgID, contactID).Scan(&tagCount); err != nil {
		t.Fatalf("count confirmed contact tags: %v", err)
	}
	if membershipCount != 1 || tagCount != 1 {
		t.Fatalf("confirmation did not apply form associations: list=%d tag=%d", membershipCount, tagCount)
	}

	var submissionCount int
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM form_submissions WHERE form_id = $1 AND organization_id = $2
	`, created.ID, orgID).Scan(&submissionCount); err != nil {
		t.Fatalf("count form submissions: %v", err)
	}
	// Suppressed addresses are accepted with a generic success but never reactivated.
	_, err = integrationPool.Exec(t.Context(), `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, 'blocked@example.test', 'manual', 'test')
	`, orgID)
	if err != nil {
		t.Fatalf("create suppression: %v", err)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
		`{"data":{"email":"blocked@example.test"},"consent":true}`, "")
	assertStatus(t, w, http.StatusOK)
	var suppressedContactCount int
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM contacts WHERE organization_id = $1 AND lower(email) = 'blocked@example.test'
	`, orgID).Scan(&suppressedContactCount); err != nil {
		t.Fatalf("count suppressed test contacts: %v", err)
	}
	if suppressedContactCount != 0 {
		t.Fatal("form submission created a contact for a suppressed address")
	}
	if err := integrationPool.QueryRow(t.Context(), `
		SELECT count(*) FROM form_submissions WHERE form_id = $1 AND organization_id = $2
	`, created.ID, orgID).Scan(&submissionCount); err != nil {
		t.Fatalf("count form submissions after suppression: %v", err)
	}
	if submissionCount != 2 {
		t.Fatalf("form submissions = %d, want 2 (invalid and honeypot submissions excluded)", submissionCount)
	}
	w = doRequest(t, s, http.MethodGet, "/api/v1/forms/"+created.ID+"/submissions?limit=10", "", token)
	assertStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatalf("submissions pagination missing total: %s", w.Body.String())
	}

	// The eleventh request from the same IP in a minute is rejected.
	for i := 0; i < formRateLimit-5; i++ {
		w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
			`{"website":"bot"}`, "")
		assertStatus(t, w, http.StatusOK)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/public/forms/"+created.Slug+"/submit",
		`{"website":"bot"}`, "")
	assertStatus(t, w, http.StatusTooManyRequests)

	w = doRequest(t, s, http.MethodPost, "/api/v1/forms/"+created.ID+"/unpublish", "", token)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodGet, "/api/v1/public/forms/"+created.Slug, "", "")
	assertStatus(t, w, http.StatusNotFound)
	w = doRequest(t, s, http.MethodPost, "/api/v1/forms/"+created.ID+"/publish", "", token)
	assertStatus(t, w, http.StatusOK)
	w = doRequest(t, s, http.MethodDelete, "/api/v1/forms/"+created.ID, "", token)
	assertStatus(t, w, http.StatusNoContent)
	w = doRequest(t, s, http.MethodGet, "/api/v1/forms/"+created.ID, "", token)
	assertStatus(t, w, http.StatusNotFound)
}
