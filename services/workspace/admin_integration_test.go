package workspace

import (
	"context"
	"testing"

	"github.com/khayode/email-platform/internal/testutil"
)

func TestPostgresAdminOrganizationListingFiltersAndCountsMembers(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	var acmeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ('Acme Partners', 'acme-partners')
		RETURNING id
	`).Scan(&acmeID); err != nil {
		t.Fatalf("insert workspace fixture: %v", err)
	}
	var memberID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO memberships (organization_id, user_id, role)
		VALUES ($1, gen_random_uuid(), 'owner')
		RETURNING user_id
	`, acmeID).Scan(&memberID); err != nil {
		t.Fatalf("insert workspace membership fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (name, slug) VALUES ('Other Company', 'other-company')
	`); err != nil {
		t.Fatalf("insert other workspace fixture: %v", err)
	}

	store := &pgxStore{pool: pool}
	workspaces, total, err := store.ListAdminOrganizations(ctx, "ACME", 1, 0)
	if err != nil {
		t.Fatalf("list admin workspaces: %v", err)
	}
	if total != 1 || len(workspaces) != 1 {
		t.Fatalf("workspace result = %d rows, total %d; want 1, 1", len(workspaces), total)
	}
	if workspaces[0].ID != acmeID || workspaces[0].Name != "Acme Partners" || workspaces[0].MemberCount != 1 {
		t.Fatalf("unexpected workspace metadata: %+v", workspaces[0])
	}
	byID, idTotal, err := store.ListAdminOrganizations(ctx, acmeID, 1, 0)
	if err != nil {
		t.Fatalf("search admin workspaces by ID: %v", err)
	}
	if idTotal != 1 || len(byID) != 1 || byID[0].ID != acmeID {
		t.Fatalf("workspace ID search = %+v, total %d; want the matching workspace", byID, idTotal)
	}

	organization, err := store.GetOrganization(ctx, acmeID)
	if err != nil {
		t.Fatalf("load admin workspace detail: %v", err)
	}
	members, err := store.ListMembers(ctx, acmeID)
	if err != nil {
		t.Fatalf("list admin workspace memberships: %v", err)
	}
	if organization.ID != acmeID || organization.Name != "Acme Partners" ||
		len(members) != 1 || members[0].UserID != memberID || members[0].Role != "owner" {
		t.Fatalf("unexpected workspace detail or memberships: organization=%+v members=%+v", organization, members)
	}
}
