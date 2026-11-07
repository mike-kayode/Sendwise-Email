package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/testutil"
)

func newActorSyncServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		pool:   testutil.NewPostgres(t),
		logger: observability.NewLogger("test", "api"),
	}
}

func actorClaims(subject, email, organizationID string) *auth.Claims {
	claims := &auth.Claims{}
	claims.Subject = subject
	claims.Email = email
	claims.OrganizationID = organizationID
	claims.Role = auth.RoleOwner
	return claims
}

// A login must never 500 because a legacy user with the same email exists
// under a different id: the pair goes to backfill_user_id_map and the clash is
// skipped, idempotently.
func TestEnsureActorRowsSkipsEmailClashWithoutFailing(t *testing.T) {
	server := newActorSyncServer(t)
	ctx := context.Background()

	const email = "actor-clash@example.test"
	identityUserID := uuid.NewString()
	legacyHolderID := uuid.NewString()
	organizationID := uuid.NewString()

	// The identity id is already a legacy row (different email) and a
	// different legacy row holds the identity email: re-keying would collide
	// on the primary key.
	if _, err := server.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, 'other@example.test', 'legacy-hash', 'Occupied Row', 'active')
	`, identityUserID); err != nil {
		t.Fatalf("seed occupying legacy user: %v", err)
	}
	if _, err := server.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'holder-hash', 'Email Holder', 'active')
	`, legacyHolderID, email); err != nil {
		t.Fatalf("seed legacy email holder: %v", err)
	}
	if _, err := server.pool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Actor Clash Org', $2)
	`, organizationID, "actor-clash-"+organizationID); err != nil {
		t.Fatalf("seed legacy organization: %v", err)
	}

	claims := actorClaims(identityUserID, email, organizationID)
	for run := 1; run <= 2; run++ {
		if err := server.ensureActorRows(ctx, claims); err != nil {
			t.Fatalf("run %d: ensureActorRows must not fail on an email clash: %v", run, err)
		}
	}

	var userCount int
	if err := server.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 2 {
		t.Fatalf("users total = %d, want 2 (clash skipped, nothing duplicated)", userCount)
	}
	var emailHolders int
	if err := server.pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email,
	).Scan(&emailHolders); err != nil {
		t.Fatalf("count email holders: %v", err)
	}
	if emailHolders != 1 {
		t.Fatalf("rows holding %s = %d, want 1", email, emailHolders)
	}
	var occupantEmail string
	if err := server.pool.QueryRow(ctx,
		`SELECT email FROM users WHERE id = $1`, identityUserID,
	).Scan(&occupantEmail); err != nil {
		t.Fatalf("read occupying row: %v", err)
	}
	if occupantEmail != "other@example.test" {
		t.Fatalf("occupying row email = %q, must be untouched", occupantEmail)
	}

	var mappingRows int
	if err := server.pool.QueryRow(ctx,
		`SELECT count(*) FROM backfill_user_id_map WHERE identity_user_id = $1 AND legacy_user_id = $2`,
		identityUserID, legacyHolderID,
	).Scan(&mappingRows); err != nil {
		t.Fatalf("count mapping rows: %v", err)
	}
	if mappingRows != 1 {
		t.Fatalf("mapping rows = %d, want 1 after two runs", mappingRows)
	}

	// The membership still attaches: the identity id has a legacy row, so the
	// foreign key resolves.
	var memberships int
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM organization_members
		WHERE organization_id = $1 AND user_id = $2
	`, organizationID, identityUserID).Scan(&memberships); err != nil {
		t.Fatalf("count membership: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("membership rows = %d, want 1", memberships)
	}
}

// The safe case: a legacy user holding the email under a different id is
// re-keyed onto the identity id, carrying its membership with it.
func TestEnsureActorRowsRekeysLegacyEmailClash(t *testing.T) {
	server := newActorSyncServer(t)
	ctx := context.Background()

	const email = "actor-rekey@example.test"
	legacyUserID := uuid.NewString()
	identityUserID := uuid.NewString()
	organizationID := uuid.NewString()

	if _, err := server.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'legacy-hash', 'Legacy Actor', 'active')
	`, legacyUserID, email); err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	if _, err := server.pool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Actor Rekey Org', $2)
	`, organizationID, "actor-rekey-"+organizationID); err != nil {
		t.Fatalf("seed legacy organization: %v", err)
	}
	if _, err := server.pool.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, organizationID, legacyUserID); err != nil {
		t.Fatalf("seed legacy membership: %v", err)
	}

	claims := actorClaims(identityUserID, email, organizationID)
	for run := 1; run <= 2; run++ {
		if err := server.ensureActorRows(ctx, claims); err != nil {
			t.Fatalf("run %d: ensureActorRows must not fail on an email clash: %v", run, err)
		}
	}

	var userCount int
	if err := server.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 1 {
		t.Fatalf("users total = %d, want 1 (re-keyed, not duplicated)", userCount)
	}
	var holder string
	if err := server.pool.QueryRow(ctx,
		`SELECT id FROM users WHERE lower(email) = lower($1)`, email,
	).Scan(&holder); err != nil {
		t.Fatalf("find email holder: %v", err)
	}
	if holder != identityUserID {
		t.Fatalf("email held by %s, want the identity id %s", holder, identityUserID)
	}

	// The membership followed the re-key, and no row is left orphaned.
	var moved, orphaned int
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM organization_members
		WHERE organization_id = $1 AND user_id = $2
	`, organizationID, identityUserID).Scan(&moved); err != nil {
		t.Fatalf("count moved membership: %v", err)
	}
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM organization_members m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE u.id IS NULL
	`).Scan(&orphaned); err != nil {
		t.Fatalf("count orphaned memberships: %v", err)
	}
	if moved != 1 || orphaned != 0 {
		t.Fatalf("moved = %d, orphaned = %d; want 1 and 0", moved, orphaned)
	}

	var mappingRows int
	if err := server.pool.QueryRow(ctx,
		`SELECT count(*) FROM backfill_user_id_map`,
	).Scan(&mappingRows); err != nil {
		t.Fatalf("count mapping rows: %v", err)
	}
	if mappingRows != 0 {
		t.Fatalf("mapping rows = %d, want 0 on the re-key path", mappingRows)
	}
}
