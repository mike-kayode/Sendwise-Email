package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestContactImportAPIQueuesAndScopesProgress(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	userID, organizationID := createImportTestPrincipal(t, ctx, pool, "import-owner@example.test", "import-workspace")
	var listID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_lists (organization_id, name)
		VALUES ($1, 'Import target')
		RETURNING id
	`, organizationID).Scan(&listID); err != nil {
		t.Fatalf("create import target list: %v", err)
	}
	tokens := auth.NewManager("integration-test-secret-value", "integration-test", time.Hour)
	token, err := tokens.IssueAccess(userID, organizationID, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}

	server := New(
		config.Config{CORSOrigins: []string{"http://integration.test"}},
		pool,
		tokens,
		nil,
		auth.NewAPIKeyService(pool, slog.New(slog.NewTextHandler(io.Discard, nil))),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler := server.Handler()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "../../contacts.csv")
	if err != nil {
		t.Fatalf("create CSV field: %v", err)
	}
	if _, err := file.Write([]byte("Email address,First,Company,Status\nAda@example.test,Ada,Engines Inc,subscribed\n")); err != nil {
		t.Fatalf("write CSV content: %v", err)
	}
	mapping, err := json.Marshal(map[string]any{
		"email":      "Email address",
		"first_name": "First",
		"custom_fields": map[string]string{
			"Company": "company",
		},
	})
	if err != nil {
		t.Fatalf("encode mapping: %v", err)
	}
	if err := writer.WriteField("mapping", string(mapping)); err != nil {
		t.Fatalf("write mapping field: %v", err)
	}
	if err := writer.WriteField("list_id", listID); err != nil {
		t.Fatalf("write target list: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart body: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/contacts/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST import status = %d, body = %s", response.Code, response.Body.String())
	}

	var queued struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
		t.Fatalf("decode queued import: %v", err)
	}
	if queued.ID == "" || queued.Status != "queued" {
		t.Fatalf("unexpected queued import response: %#v", queued)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/contact-imports/"+queued.ID, nil)
	statusRequest.Header.Set("Authorization", "Bearer "+token)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("GET import status = %d, body = %s", statusResponse.Code, statusResponse.Body.String())
	}
	var status struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		FileName string `json:"file_name"`
		ListID   string `json:"list_id"`
	}
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode import status: %v", err)
	}
	if status.ID != queued.ID || status.Status != "queued" || status.FileName != "contacts.csv" || status.ListID != listID {
		t.Fatalf("unexpected import status: %#v", status)
	}

	otherUserID, otherOrganizationID := createImportTestPrincipal(t, ctx, pool, "other-import-owner@example.test", "other-import-workspace")
	otherToken, err := tokens.IssueAccess(otherUserID, otherOrganizationID, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue second access token: %v", err)
	}
	otherRequest := httptest.NewRequest(http.MethodGet, "/api/v1/contact-imports/"+queued.ID, nil)
	otherRequest.Header.Set("Authorization", "Bearer "+otherToken)
	otherResponse := httptest.NewRecorder()
	handler.ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-organization import status = %d, want 404", otherResponse.Code)
	}
}

func createImportTestPrincipal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, slug string) (string, string) {
	t.Helper()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name)
		VALUES ($1, 'integration-test-hash', 'Import Test')
		RETURNING id
	`, email).Scan(&userID); err != nil {
		t.Fatalf("create integration user: %v", err)
	}
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Import Test', $1)
		RETURNING id
	`, slug).Scan(&organizationID); err != nil {
		t.Fatalf("create integration organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, organizationID, userID); err != nil {
		t.Fatalf("create integration membership: %v", err)
	}
	return userID, organizationID
}
