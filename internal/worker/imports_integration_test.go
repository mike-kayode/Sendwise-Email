package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

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
		INSERT INTO contact_imports (organization_id, file_name, source_data, mapping)
		VALUES ($1, 'contacts.csv', $2, $3::jsonb)
		RETURNING id
	`, organizationID, source, mappingJSON).Scan(&importID); err != nil {
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
}
