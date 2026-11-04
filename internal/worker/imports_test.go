package worker

import (
	"strings"
	"testing"

	"github.com/khayode/email-platform/internal/contacts"
)

func TestCountContactImportRows(t *testing.T) {
	source := []byte("email,first_name\none@example.com,Ada\ntwo@example.com,Grace\n")
	mapping := &contacts.CSVColumnMapping{Email: "email", FirstName: "first_name"}
	rows, err := countContactImportRows(source, mapping)
	if err != nil {
		t.Fatalf("countContactImportRows() error = %v", err)
	}
	if rows != 2 {
		t.Fatalf("row count = %d, want 2", rows)
	}
}

func TestCountContactImportRowsStopsAtLimit(t *testing.T) {
	var source strings.Builder
	source.WriteString("email\n")
	for range contacts.MaxCSVImportRows + 1 {
		source.WriteString("person@example.com\n")
	}
	mapping := &contacts.CSVColumnMapping{Email: "email"}
	rows, err := countContactImportRows([]byte(source.String()), mapping)
	if err != nil {
		t.Fatalf("countContactImportRows() error = %v", err)
	}
	if rows != contacts.MaxCSVImportRows+1 {
		t.Fatalf("row count = %d, want limit+1", rows)
	}
}

func TestAppendImportErrorCapsStoredDetails(t *testing.T) {
	var details []contactImportRowError
	for row := 1; row <= contactImportMaxErrors+10; row++ {
		appendImportError(&details, row, "Invalid CSV row.")
	}
	if len(details) != contactImportMaxErrors {
		t.Fatalf("stored errors = %d, want cap %d", len(details), contactImportMaxErrors)
	}
}
