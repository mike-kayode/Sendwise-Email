package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestTagCRUDIsTenantScopedAndDoesNotDeleteContacts(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	userID, organizationID := createImportTestPrincipal(t, ctx, pool, "tag-owner@example.test", "tag-workspace")
	tokens := auth.NewManager("integration-test-secret-value", "integration-test", time.Hour)
	token, err := tokens.IssueAccess(userID, organizationID, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	server := New(
		config.Config{},
		pool,
		tokens,
		nil,
		auth.NewAPIKeyService(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler := server.Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/tags", strings.NewReader(`{"name":"old-name","color":"#4f46e5"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create tag status = %d, body = %s", response.Code, response.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created tag: %v", err)
	}

	var contactID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contacts (organization_id, email)
		VALUES ($1, 'tagged-contact@example.test')
		RETURNING id
	`, organizationID).Scan(&contactID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contact_tags (organization_id, contact_id, tag_id)
		VALUES ($1, $2, $3)
	`, organizationID, contactID, created.ID); err != nil {
		t.Fatalf("assign tag: %v", err)
	}

	request = httptest.NewRequest(http.MethodPatch, "/api/v1/tags/"+created.ID, strings.NewReader(`{"name":"new-name","color":"#059669"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("update tag status = %d, body = %s", response.Code, response.Body.String())
	}

	otherUser, otherOrganization := createImportTestPrincipal(t, ctx, pool, "other-tag-owner@example.test", "other-tag-workspace")
	otherToken, err := tokens.IssueAccess(otherUser, otherOrganization, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue other organization token: %v", err)
	}
	request = httptest.NewRequest(http.MethodDelete, "/api/v1/tags/"+created.ID, nil)
	request.Header.Set("Authorization", "Bearer "+otherToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-organization delete status = %d, want 404", response.Code)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/tags/"+created.ID, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete tag status = %d, body = %s", response.Code, response.Body.String())
	}

	var remainingContacts, remainingAssignments int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts WHERE id = $1 AND organization_id = $2
	`, contactID, organizationID).Scan(&remainingContacts); err != nil {
		t.Fatalf("check contact after tag deletion: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM contact_tags WHERE contact_id = $1
	`, contactID).Scan(&remainingAssignments); err != nil {
		t.Fatalf("check tag assignments after deletion: %v", err)
	}
	if remainingContacts != 1 || remainingAssignments != 0 {
		t.Fatalf("after tag deletion: contacts=%d assignments=%d", remainingContacts, remainingAssignments)
	}
}
