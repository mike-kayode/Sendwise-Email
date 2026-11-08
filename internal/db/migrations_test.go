package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

func TestDomainOwnershipMigrationIsSafeAndIdempotent(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL migration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}

	schema := "domain_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create migration test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop migration test schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close migration test connection: %v", err)
		}
	})

	config.RuntimeParams["search_path"] = schema + ",public"
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect migration test schema: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `
			CREATE TABLE sending_domains (
				id uuid PRIMARY KEY,
				organization_id uuid NOT NULL,
				domain text NOT NULL,
				status text NOT NULL,
				created_at timestamptz NOT NULL
			);
			INSERT INTO sending_domains (id, organization_id, domain, status, created_at) VALUES
				('10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', 'Shared.Example', 'pending', '2026-01-01T00:00:00Z'),
				('10000000-0000-4000-8000-000000000002', '20000000-0000-4000-8000-000000000002', 'shared.example', 'verified', '2026-01-03T00:00:00Z'),
				('10000000-0000-4000-8000-000000000003', '20000000-0000-4000-8000-000000000003', 'SHARED.EXAMPLE', 'pending', '2026-01-02T00:00:00Z'),
				('10000000-0000-4000-8000-000000000004', '20000000-0000-4000-8000-000000000004', 'oldest.example', 'pending', '2026-01-01T00:00:00Z'),
				('10000000-0000-4000-8000-000000000005', '20000000-0000-4000-8000-000000000005', 'oldest.example', 'misconfigured', '2026-01-02T00:00:00Z')
		`); err != nil {
		t.Fatalf("seed duplicate sending domains: %v", err)
	}

	migration := readMigration(t, "0019_workspace_delivery_routing.sql")
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := conn.Exec(ctx, migration); err != nil {
			t.Fatalf("apply domain ownership migration attempt %d: %v", attempt, err)
		}

		rows, err := conn.Query(ctx, `
			SELECT id::text, organization_id::text, owner_conflict
			FROM sending_domains
			WHERE lower(domain) IN ('shared.example', 'oldest.example')
			ORDER BY lower(domain), created_at, id
		`)
		if err != nil {
			t.Fatalf("read migrated domain ownership: %v", err)
		}
		defer rows.Close()
		conflicts := make(map[string]bool)
		for rows.Next() {
			var id, organizationID string
			var ownerConflict bool
			if err := rows.Scan(&id, &organizationID, &ownerConflict); err != nil {
				t.Fatalf("scan migrated domain ownership: %v", err)
			}
			conflicts[id] = ownerConflict
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate migrated domain ownership: %v", err)
		}
		if len(conflicts) != 5 {
			t.Fatalf("migration changed row count: found %d rows, want 5", len(conflicts))
		}
		wantConflicts := map[string]bool{
			"10000000-0000-4000-8000-000000000001": true,
			"10000000-0000-4000-8000-000000000002": false,
			"10000000-0000-4000-8000-000000000003": true,
			"10000000-0000-4000-8000-000000000004": false,
			"10000000-0000-4000-8000-000000000005": true,
		}
		for id, want := range wantConflicts {
			if got, ok := conflicts[id]; !ok || got != want {
				t.Errorf("owner_conflict for %s = %t (found %t), want %t", id, got, ok, want)
			}
		}

		_, err = conn.Exec(ctx, `
			INSERT INTO sending_domains (id, organization_id, domain, status, created_at)
			VALUES ('10000000-0000-4000-8000-000000000006', '20000000-0000-4000-8000-000000000006', 'shared.example', 'pending', now())
		`)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
			t.Fatalf("global unique owner index error = %v, want unique violation", err)
		}
		if postgresError.ConstraintName != "sending_domains_domain_global_idx" {
			t.Fatalf("duplicate rejected by constraint %q, want global owner index", postgresError.ConstraintName)
		}
	}
}
