package worker

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

	"github.com/khayode/email-platform/internal/api"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestContactImportWorkerPersistsPendingContactsAndResults(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()

	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Import Integration', 'import-integration')
		RETURNING id
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create integration organization: %v", err)
	}
	var listID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_lists (organization_id, name)
		VALUES ($1, 'Import target')
		RETURNING id
	`, organizationID).Scan(&listID); err != nil {
		t.Fatalf("create import target list: %v", err)
	}

	mapping := contacts.CSVColumnMapping{
		Email:        "email",
		FirstName:    "first_name",
		CustomFields: map[string]string{"company": "company"},
	}
	mappingJSON, err := json.Marshal(mapping)
	if err != nil {
		t.Fatalf("encode mapping: %v", err)
	}
	source := []byte("email,first_name,company,status\nada@example.test,Ada,Engines Inc,subscribed\nada@example.test,Ada,Duplicate,subscribed\nnot-an-email,Invalid,Unknown,subscribed\n")
	var importID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_imports (organization_id, file_name, source_data, mapping, list_id)
		VALUES ($1, 'contacts.csv', $2, $3::jsonb, $4)
		RETURNING id
	`, organizationID, source, mappingJSON, listID).Scan(&importID); err != nil {
		t.Fatalf("create import job: %v", err)
	}

	worker := &Worker{
		pool:   pool,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := worker.processPendingContactImports(ctx); err != nil {
		t.Fatalf("process pending imports: %v", err)
	}

	var status string
	var total, processed, imported, skipped, invalid int
	var remainingSource []byte
	var rawErrors []byte
	if err := pool.QueryRow(ctx, `
		SELECT status, total_rows, processed_rows, imported_count, skipped_count,
		       invalid_count, source_data, errors
		FROM contact_imports WHERE id = $1 AND organization_id = $2
	`, importID, organizationID).Scan(
		&status, &total, &processed, &imported, &skipped, &invalid, &remainingSource, &rawErrors,
	); err != nil {
		t.Fatalf("load import result: %v", err)
	}
	if status != "completed" || total != 3 || processed != 3 || imported != 1 || skipped != 1 || invalid != 1 {
		t.Fatalf("unexpected import result: status=%s total=%d processed=%d imported=%d skipped=%d invalid=%d", status, total, processed, imported, skipped, invalid)
	}
	if len(remainingSource) != 0 {
		t.Fatalf("completed job retained %d upload bytes", len(remainingSource))
	}
	var rowErrors []contactImportRowError
	if err := json.Unmarshal(rawErrors, &rowErrors); err != nil {
		t.Fatalf("decode row errors: %v", err)
	}
	if len(rowErrors) != 1 || rowErrors[0].Row != 4 {
		t.Fatalf("unexpected row errors: %#v", rowErrors)
	}

	var contactStatus string
	var customFields []byte
	if err := pool.QueryRow(ctx, `
		SELECT status, custom_fields FROM contacts
		WHERE organization_id = $1 AND email = 'ada@example.test'
	`, organizationID).Scan(&contactStatus, &customFields); err != nil {
		t.Fatalf("load imported contact: %v", err)
	}
	if contactStatus != "pending" {
		t.Fatalf("imported contact status = %q, want pending", contactStatus)
	}
	var fields map[string]string
	if err := json.Unmarshal(customFields, &fields); err != nil {
		t.Fatalf("decode imported custom fields: %v", err)
	}
	if fields["company"] != "Engines Inc" {
		t.Fatalf("company = %q, want Engines Inc", fields["company"])
	}
	var memberships int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM contact_list_members m
		JOIN contacts c ON c.id = m.contact_id
		WHERE m.organization_id = $1 AND m.list_id = $2
		  AND c.email = 'ada@example.test'
	`, organizationID, listID).Scan(&memberships); err != nil {
		t.Fatalf("count imported list membership: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("list memberships = %d, want 1", memberships)
	}
}

// TestContactImportRecoversJobStuckInProcessing proves that a job whose worker
// crashed mid-run (status still 'processing', lease expired) is re-claimed and
// finished instead of being stuck forever.
func TestContactImportRecoversJobStuckInProcessing(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()

	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Stuck Import', 'stuck-import')
		RETURNING id
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create integration organization: %v", err)
	}
	mapping := `{"email":"email","first_name":"first_name"}`
	source := []byte("email,first_name\nstick@example.test,Stuck\n")
	var importID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_imports (organization_id, file_name, source_data, mapping,
		                             status, lease_token, lease_expires_at, attempts, started_at)
		VALUES ($1, 'stuck.csv', $2, $3::jsonb, 'processing', '11111111-1111-4111-8111-111111111111',
		        now() - interval '1 minute', 1, now() - interval '10 minutes')
		RETURNING id
	`, organizationID, source, mapping).Scan(&importID); err != nil {
		t.Fatalf("create stuck import job: %v", err)
	}

	worker := &Worker{
		pool:   pool,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := worker.processPendingContactImports(ctx); err != nil {
		t.Fatalf("process pending imports: %v", err)
	}

	var status string
	var imported, invalid int
	var lastError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, imported_count, invalid_count, last_error
		FROM contact_imports WHERE id = $1
	`, importID).Scan(&status, &imported, &invalid, &lastError); err != nil {
		t.Fatalf("load import result: %v", err)
	}
	if status != "completed" || imported != 1 || invalid != 0 {
		t.Fatalf("stuck import result: status=%s imported=%d invalid=%d, want completed/1/0", status, imported, invalid)
	}
	if lastError != nil && *lastError != "" {
		t.Fatalf("completed import retained last_error: %q", *lastError)
	}
}

// TestContactImportEndToEndFromUploadToContacts drives the exact production
// path: POST /api/v1/contacts/import (multipart CSV) -> contact_imports row ->
// worker run -> GET /api/v1/contact-imports/{id}. The CSV has a header, two
// valid rows, one invalid email and one duplicate: imported=2, invalid=1,
// skipped=1, and the row error number counts the header row.
func TestContactImportEndToEndFromUploadToContacts(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var userID, organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name)
		VALUES ('import-e2e@example.test', 'import-e2e-hash', 'Import E2E')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("create integration user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Import E2E', 'import-e2e')
		RETURNING id
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create integration organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, organizationID, userID); err != nil {
		t.Fatalf("create integration membership: %v", err)
	}
	var listID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO contact_lists (organization_id, name)
		VALUES ($1, 'E2E import target')
		RETURNING id
	`, organizationID).Scan(&listID); err != nil {
		t.Fatalf("create import target list: %v", err)
	}
	var otherOrganizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Other Import E2E', 'other-import-e2e')
		RETURNING id
	`).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("create second organization: %v", err)
	}

	tokens := auth.NewManager("import-e2e-secret-value", "import-e2e", time.Hour)
	token, err := tokens.IssueAccess(userID, organizationID, auth.RoleOwner)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	server := api.New(
		config.Config{CORSOrigins: []string{"http://import.e2e.test"}},
		pool,
		tokens,
		nil,
		auth.NewAPIKeyService(pool, logger),
		nil,
		logger,
	)
	handler := server.Handler()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "contacts.csv")
	if err != nil {
		t.Fatalf("create CSV field: %v", err)
	}
	csv := "email,first_name\n" +
		"one@example.test,One\n" +
		"two@example.test,Two\n" +
		"not-an-email,Broken\n" +
		"one@example.test,Duplicate\n"
	if _, err := file.Write([]byte(csv)); err != nil {
		t.Fatalf("write CSV content: %v", err)
	}
	if err := writer.WriteField("list_id", listID); err != nil {
		t.Fatalf("write list_id field: %v", err)
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

	worker := &Worker{pool: pool, logger: logger}
	if err := worker.processPendingContactImports(ctx); err != nil {
		t.Fatalf("run worker once: %v", err)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/contact-imports/"+queued.ID, nil)
	statusRequest.Header.Set("Authorization", "Bearer "+token)
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("GET import status = %d, body = %s", statusResponse.Code, statusResponse.Body.String())
	}
	var result struct {
		Status        string `json:"status"`
		TotalRows     int    `json:"total_rows"`
		ProcessedRows int    `json:"processed_rows"`
		Imported      int    `json:"imported"`
		Skipped       int    `json:"skipped"`
		Invalid       int    `json:"invalid"`
		Errors        []struct {
			Row     int    `json:"row"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode import result: %v", err)
	}
	if result.Status != "completed" || result.TotalRows != 4 || result.ProcessedRows != 4 ||
		result.Imported != 2 || result.Skipped != 1 || result.Invalid != 1 {
		t.Fatalf("import result = %#v, want completed/4/4/2/1/1", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Row != 4 {
		t.Fatalf("row errors = %#v, want exactly row 4 (header counted)", result.Errors)
	}

	var inOrg int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts
		WHERE organization_id = $1 AND lower(email) IN ('one@example.test', 'two@example.test')
	`, organizationID).Scan(&inOrg); err != nil {
		t.Fatalf("count imported contacts: %v", err)
	}
	if inOrg != 2 {
		t.Fatalf("contacts in org = %d, want 2", inOrg)
	}
	var elsewhere int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM contacts
		WHERE organization_id <> $1 AND lower(email) IN ('one@example.test', 'two@example.test')
	`, organizationID).Scan(&elsewhere); err != nil {
		t.Fatalf("count leaked contacts: %v", err)
	}
	if elsewhere != 0 {
		t.Fatalf("contacts leaked into other organizations (incl. %s): %d", otherOrganizationID, elsewhere)
	}
	var memberships int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM contact_list_members
		WHERE organization_id = $1 AND list_id = $2
	`, organizationID, listID).Scan(&memberships); err != nil {
		t.Fatalf("count list memberships: %v", err)
	}
	if memberships != 2 {
		t.Fatalf("list memberships = %d, want 2", memberships)
	}
}
