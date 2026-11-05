package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "migrations", name)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(content)
}

func TestImportMigrationsAreAdditive(t *testing.T) {
	initial := readMigration(t, "0001_init.sql")
	upgrade := readMigration(t, "0002_contact_imports.sql")
	repair := readMigration(t, "0006_migration_repairs.sql")
	listImport := readMigration(t, "0008_contact_import_lists.sql")

	if !strings.Contains(initial, "CREATE TABLE contact_imports") {
		t.Fatal("initial migration must create contact_imports")
	}
	if strings.Contains(upgrade, "CREATE TABLE contact_imports") {
		t.Fatal("contact import upgrade must not recreate the table")
	}
	for _, migration := range []string{upgrade, repair} {
		for _, column := range []string{"attempts", "next_attempt_at", "lease_expires_at", "lease_token"} {
			expected := "ADD COLUMN IF NOT EXISTS " + column
			if !strings.Contains(migration, expected) {
				t.Errorf("migration is missing idempotent addition %q", expected)
			}
		}
		if !strings.Contains(listImport, "ADD COLUMN IF NOT EXISTS list_id uuid REFERENCES contact_lists(id) ON DELETE SET NULL") {
			t.Fatal("list import migration must add a nullable, safe list reference")
		}
	}
}

func TestVerificationTokenUpdateTriggerHasUpdatedAtColumn(t *testing.T) {
	initial := readMigration(t, "0005_verification_tokens.sql")
	repair := readMigration(t, "0006_migration_repairs.sql")

	updatedAtColumn := regexp.MustCompile(`(?m)^\s*updated_at\s+timestamptz NOT NULL DEFAULT now\(\)$`)
	if !updatedAtColumn.MatchString(initial) {
		t.Fatal("verification_tokens must define updated_at before installing its update trigger")
	}
	if !strings.Contains(repair, "ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now()") {
		t.Fatal("forward repair must add updated_at to existing verification_tokens tables")
	}
}

func TestEventOutboxAndInboxMigrationHasLeaseAndIdempotencyKeys(t *testing.T) {
	migration := readMigration(t, "0007_event_outbox_inbox.sql")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS event_outbox",
		"event_id         uuid PRIMARY KEY",
		"lease_token      uuid",
		"lease_expires_at timestamptz",
		"CREATE TABLE IF NOT EXISTS event_inbox",
		"PRIMARY KEY (consumer_name, event_id)",
		"WHERE published_at IS NULL",
	} {
		if !strings.Contains(migration, expected) {
			t.Errorf("event migration is missing %q", expected)
		}
	}
}
