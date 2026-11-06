package backfill

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestRunProjectsIdentityAndWorkspaceIdempotently(t *testing.T) {
	identity := testutil.NewPostgresWithMigrations(t, "services/identity/migrations")
	workspace := testutil.NewPostgresWithMigrations(t, "services/workspace/migrations")
	legacy := testutil.NewPostgres(t)
	ctx := context.Background()

	userID := uuid.NewString()
	orgID := uuid.NewString()
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, 'backfill@example.test', 'identity-password-hash', 'Backfill User', 'active')
	`, userID); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Backfill Workspace', 'backfill-workspace')
	`, orgID); err != nil {
		t.Fatalf("seed workspace organization: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, orgID, userID); err != nil {
		t.Fatalf("seed workspace membership: %v", err)
	}

	for run := 1; run <= 2; run++ {
		summary, err := Run(ctx, identity, workspace, legacy)
		if err != nil {
			t.Fatalf("backfill run %d: %v", run, err)
		}
		if summary.Users != 1 || summary.Organizations != 1 || summary.Memberships != 1 {
			t.Fatalf("run %d summary = %+v", run, summary)
		}
	}

	var userCount, organizationCount, membershipCount int
	if err := legacy.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, userID).Scan(&userCount); err != nil {
		t.Fatalf("count projected user: %v", err)
	}
	if err := legacy.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id = $1`, orgID).Scan(&organizationCount); err != nil {
		t.Fatalf("count projected organization: %v", err)
	}
	if err := legacy.QueryRow(ctx, `
		SELECT count(*) FROM organization_members WHERE organization_id = $1 AND user_id = $2
	`, orgID, userID).Scan(&membershipCount); err != nil {
		t.Fatalf("count projected membership: %v", err)
	}
	if userCount != 1 || organizationCount != 1 || membershipCount != 1 {
		t.Fatalf("projected counts = user:%d org:%d membership:%d", userCount, organizationCount, membershipCount)
	}
}
