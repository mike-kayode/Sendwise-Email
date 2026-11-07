package backfill

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/testutil"
)

// logRecorder captures log lines so tests can assert on the audit trail.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *logRecorder) WithGroup(string) slog.Handler            { return r }
func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	var b strings.Builder
	rec.Attrs(func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, rec.Message+b.String())
	return nil
}

func (r *logRecorder) contains(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range r.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// userForeignKeyColumns discovers every foreign-key column that references
// users(id) in the pool's current schema.
func userForeignKeyColumns(t *testing.T, pool *pgxpool.Pool) []referencedColumn {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT kcu.table_name, kcu.column_name
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON  kcu.constraint_schema = tc.constraint_schema
		  AND kcu.constraint_name   = tc.constraint_name
		  AND kcu.table_schema      = tc.table_schema
		  AND kcu.table_name        = tc.table_name
		JOIN information_schema.constraint_column_usage ccu
		  ON  ccu.constraint_schema = tc.constraint_schema
		  AND ccu.constraint_name   = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema  = current_schema()
		  AND ccu.table_schema = current_schema()
		  AND ccu.table_name   = 'users'
		ORDER BY kcu.table_name, kcu.column_name
	`)
	if err != nil {
		t.Fatalf("list foreign keys to users: %v", err)
	}
	defer rows.Close()
	var refs []referencedColumn
	for rows.Next() {
		var ref referencedColumn
		if err := rows.Scan(&ref.table, &ref.column); err != nil {
			t.Fatalf("scan foreign key to users: %v", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign keys to users: %v", err)
	}
	return refs
}

// assertUserForeignKeysResolve fails when any row references a users(id) that
// no longer exists.
func assertUserForeignKeysResolve(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	refs := userForeignKeyColumns(t, pool)
	if len(refs) < 2 {
		t.Fatalf("discovered %d foreign keys to users, want at least 2", len(refs))
	}
	for _, ref := range refs {
		query := fmt.Sprintf(
			`SELECT count(*) FROM %s c LEFT JOIN users u ON u.id = c.%s WHERE c.%s IS NOT NULL AND u.id IS NULL`,
			pgx.Identifier{ref.table}.Sanitize(),
			pgx.Identifier{ref.column}.Sanitize(),
			pgx.Identifier{ref.column}.Sanitize(),
		)
		var orphans int
		if err := pool.QueryRow(context.Background(), query).Scan(&orphans); err != nil {
			t.Fatalf("check orphans in %s.%s: %v", ref.table, ref.column, err)
		}
		if orphans != 0 {
			t.Fatalf("%s.%s has %d rows pointing at a missing users(id)", ref.table, ref.column, orphans)
		}
	}
}

func countWhere(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

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
		summary, err := Run(ctx, identity, workspace, legacy, nil)
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

// A legacy user holding the same lower(email) under a different id must be
// re-keyed onto the identity id — with every foreign key moved — and the run
// must stay idempotent across repeats.
func TestRunRekeysLegacyEmailClashAndStaysIdempotent(t *testing.T) {
	identity := testutil.NewPostgresWithMigrations(t, "services/identity/migrations")
	workspace := testutil.NewPostgresWithMigrations(t, "services/workspace/migrations")
	legacy := testutil.NewPostgres(t)
	ctx := context.Background()

	const email = "clash@example.test"
	legacyUserID := uuid.NewString()
	identityUserID := uuid.NewString()
	legacyOrgID := uuid.NewString()
	wsOrgID := uuid.NewString()

	// The clash: a legacy user with the same email under a different id, with
	// rows in two other tables pointing at it.
	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'legacy-hash', 'Legacy Owner', 'active')
	`, legacyUserID, email); err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Legacy Org', $2)
	`, legacyOrgID, "legacy-org-"+legacyOrgID); err != nil {
		t.Fatalf("seed legacy organization: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, legacyOrgID, legacyUserID); err != nil {
		t.Fatalf("seed legacy membership: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO auth_sessions (user_id, organization_id, role, session_id)
		VALUES ($1, $2, 'owner', 'rekey-session')
	`, legacyUserID, legacyOrgID); err != nil {
		t.Fatalf("seed legacy auth session: %v", err)
	}

	// The identity side of the clash, plus a workspace membership the
	// projection wants to attach to the identity id.
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'identity-hash', 'Identity Owner', 'active')
	`, identityUserID, email); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Clash Workspace', $2)
	`, wsOrgID, "clash-workspace-"+wsOrgID); err != nil {
		t.Fatalf("seed workspace organization: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, wsOrgID, identityUserID); err != nil {
		t.Fatalf("seed workspace membership: %v", err)
	}

	recorder := &logRecorder{}
	logger := slog.New(recorder)

	for run := 1; run <= 2; run++ {
		summary, err := Run(ctx, identity, workspace, legacy, logger)
		if err != nil {
			t.Fatalf("backfill run %d must not fail on an email clash: %v", run, err)
		}
		if summary.Users != 1 || summary.Organizations != 1 || summary.Memberships != 1 ||
			summary.SkippedUsers != 0 || summary.SkippedMemberships != 0 {
			t.Fatalf("run %d summary = %+v", run, summary)
		}

		// Exactly one row holds the address, under the identity id.
		if got := countWhere(t, legacy, `SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email); got != 1 {
			t.Fatalf("run %d: rows holding %s = %d, want 1", run, email, got)
		}
		var holder string
		if err := legacy.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&holder); err != nil {
			t.Fatalf("run %d: find email holder: %v", run, err)
		}
		if holder != identityUserID {
			t.Fatalf("run %d: email held by %s, want the identity id %s", run, holder, identityUserID)
		}
		if got := countWhere(t, legacy, `SELECT count(*) FROM users`); got != 1 {
			t.Fatalf("run %d: users total = %d, want 1 (re-keyed, not duplicated)", run, got)
		}
		if got := countWhere(t, legacy, `SELECT count(*) FROM users WHERE id = $1`, legacyUserID); got != 0 {
			t.Fatalf("run %d: shadowed legacy row %s still present", run, legacyUserID)
		}

		// Every foreign key followed the re-key.
		if got := countWhere(t, legacy,
			`SELECT count(*) FROM organization_members WHERE user_id = $1`, legacyUserID); got != 0 {
			t.Fatalf("run %d: %d memberships still point at the legacy id", run, got)
		}
		if got := countWhere(t, legacy,
			`SELECT count(*) FROM organization_members WHERE organization_id = $1 AND user_id = $2`,
			legacyOrgID, identityUserID); got != 1 {
			t.Fatalf("run %d: legacy membership did not move to the identity id (got %d)", run, got)
		}
		var sessionOwner string
		if err := legacy.QueryRow(ctx,
			`SELECT user_id FROM auth_sessions WHERE session_id = 'rekey-session'`,
		).Scan(&sessionOwner); err != nil {
			t.Fatalf("run %d: read auth session: %v", run, err)
		}
		if sessionOwner != identityUserID {
			t.Fatalf("run %d: auth session owned by %s, want %s", run, sessionOwner, identityUserID)
		}

		// The re-key path must not need the mapping table.
		if got := countWhere(t, legacy, `SELECT count(*) FROM backfill_user_id_map`); got != 0 {
			t.Fatalf("run %d: mapping table has %d rows on the re-key path, want 0", run, got)
		}
		assertUserForeignKeysResolve(t, legacy)
	}

	if !recorder.contains("re-keyed legacy user onto identity id") {
		t.Fatalf("expected a re-key log line, got: %v", recorder.lines)
	}
}

// When re-keying is unsafe (the identity id is already a legacy row), the
// pair goes to backfill_user_id_map and the run skips instead of failing.
func TestRunSkipsUnsafeEmailClashIntoMappingTable(t *testing.T) {
	identity := testutil.NewPostgresWithMigrations(t, "services/identity/migrations")
	workspace := testutil.NewPostgresWithMigrations(t, "services/workspace/migrations")
	legacy := testutil.NewPostgres(t)
	ctx := context.Background()

	const email = "occupied@example.test"
	identityUserID := uuid.NewString()
	legacyHolderID := uuid.NewString()
	wsOrgID := uuid.NewString()

	// The identity id is already a legacy row with a different email, and a
	// different legacy row holds the identity email: re-keying would collide
	// on the primary key.
	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, 'other@example.test', 'legacy-hash', 'Occupied Row', 'active')
	`, identityUserID); err != nil {
		t.Fatalf("seed occupying legacy user: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'holder-hash', 'Email Holder', 'active')
	`, legacyHolderID, email); err != nil {
		t.Fatalf("seed legacy email holder: %v", err)
	}
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'identity-hash', 'Identity Owner', 'active')
	`, identityUserID, email); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Occupied Workspace', $2)
	`, wsOrgID, "occupied-workspace-"+wsOrgID); err != nil {
		t.Fatalf("seed workspace organization: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, wsOrgID, identityUserID); err != nil {
		t.Fatalf("seed workspace membership: %v", err)
	}

	recorder := &logRecorder{}
	logger := slog.New(recorder)

	for run := 1; run <= 2; run++ {
		summary, err := Run(ctx, identity, workspace, legacy, logger)
		if err != nil {
			t.Fatalf("backfill run %d must not fail on an unsafe clash: %v", run, err)
		}
		if summary.Users != 0 || summary.SkippedUsers != 1 || summary.Organizations != 1 ||
			summary.Memberships != 1 || summary.SkippedMemberships != 0 {
			t.Fatalf("run %d summary = %+v", run, summary)
		}

		// No duplicate emails, and neither legacy row was touched.
		if got := countWhere(t, legacy, `SELECT count(*) FROM users`); got != 2 {
			t.Fatalf("run %d: users total = %d, want 2", run, got)
		}
		if got := countWhere(t, legacy,
			`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email); got != 1 {
			t.Fatalf("run %d: rows holding %s = %d, want 1", run, email, got)
		}
		var occupantEmail string
		if err := legacy.QueryRow(ctx,
			`SELECT email FROM users WHERE id = $1`, identityUserID,
		).Scan(&occupantEmail); err != nil {
			t.Fatalf("run %d: read occupying row: %v", run, err)
		}
		if occupantEmail != "other@example.test" {
			t.Fatalf("run %d: occupying row email = %q, must be untouched", run, occupantEmail)
		}

		// The pair is recorded once, with a clear reason.
		if got := countWhere(t, legacy, `SELECT count(*) FROM backfill_user_id_map`); got != 1 {
			t.Fatalf("run %d: mapping rows = %d, want 1", run, got)
		}
		var mappedIdentity, mappedLegacy, reason string
		if err := legacy.QueryRow(ctx, `
			SELECT identity_user_id, legacy_user_id, reason FROM backfill_user_id_map
		`).Scan(&mappedIdentity, &mappedLegacy, &reason); err != nil {
			t.Fatalf("read mapping: %v", err)
		}
		if mappedIdentity != identityUserID || mappedLegacy != legacyHolderID {
			t.Fatalf("mapping = %s->%s, want %s->%s", mappedLegacy, mappedIdentity, legacyHolderID, identityUserID)
		}
		if !strings.Contains(reason, "already exists") {
			t.Fatalf("mapping reason = %q, want the unsafe-case explanation", reason)
		}
		assertUserForeignKeysResolve(t, legacy)
	}

	if !recorder.contains("backfill_user_id_map") {
		t.Fatalf("expected a skip log line naming the mapping table, got: %v", recorder.lines)
	}
}

// A re-key that fails mid-flight must roll back to the savepoint (leaving the
// legacy row and its references intact) and fall back to the mapping table.
func TestRunRollsBackFailedRekeyAndRecordsMapping(t *testing.T) {
	identity := testutil.NewPostgresWithMigrations(t, "services/identity/migrations")
	workspace := testutil.NewPostgresWithMigrations(t, "services/workspace/migrations")
	legacy := testutil.NewPostgres(t)
	ctx := context.Background()

	const email = "blocked@example.test"
	legacyUserID := uuid.NewString()
	identityUserID := uuid.NewString()
	legacyOrgID := uuid.NewString()
	wsOrgID := uuid.NewString()

	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'legacy-hash', 'Blocked Owner', 'active')
	`, legacyUserID, email); err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}

	if _, err := legacy.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Blocked Org', $2)
	`, legacyOrgID, "blocked-org-"+legacyOrgID); err != nil {
		t.Fatalf("seed legacy organization: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, legacyOrgID, legacyUserID); err != nil {
		t.Fatalf("seed legacy membership: %v", err)
	}
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'identity-hash', 'Blocked Identity', 'active')
	`, identityUserID, email); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, 'Blocked Workspace', $2)
	`, wsOrgID, "blocked-workspace-"+wsOrgID); err != nil {
		t.Fatalf("seed workspace organization: %v", err)
	}
	if _, err := workspace.Exec(ctx, `
		INSERT INTO memberships (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, wsOrgID, identityUserID); err != nil {
		t.Fatalf("seed workspace membership: %v", err)
	}

	// Make the foreign-key move fail on demand: the savepoint must absorb it.
	if _, err := legacy.Exec(ctx, `
		CREATE FUNCTION test_block_user_rekey() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 're-key blocked by test trigger';
		END;
		$$ LANGUAGE plpgsql
	`); err != nil {
		t.Fatalf("create trigger function: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		CREATE TRIGGER block_user_rekey
		BEFORE UPDATE OF user_id ON organization_members
		FOR EACH ROW EXECUTE FUNCTION test_block_user_rekey()
	`); err != nil {
		t.Fatalf("create blocking trigger: %v", err)
	}

	recorder := &logRecorder{}
	logger := slog.New(recorder)

	for run := 1; run <= 2; run++ {
		summary, err := Run(ctx, identity, workspace, legacy, logger)
		if err != nil {
			t.Fatalf("backfill run %d must fall back to the mapping table: %v", run, err)
		}
		if summary.Users != 0 || summary.SkippedUsers != 1 ||
			summary.Organizations != 1 || summary.Memberships != 0 ||
			summary.SkippedMemberships != 1 {
			t.Fatalf("run %d summary = %+v", run, summary)
		}

		// The rollback left the legacy row and its references untouched.
		if got := countWhere(t, legacy, `SELECT count(*) FROM users`); got != 1 {
			t.Fatalf("run %d: users total = %d, want 1", run, got)
		}
		var holderID, holderEmail string
		if err := legacy.QueryRow(ctx,
			`SELECT id, email FROM users WHERE lower(email) = lower($1)`, email,
		).Scan(&holderID, &holderEmail); err != nil {
			t.Fatalf("run %d: legacy row lost its identity: %v", run, err)
		}
		if holderID != legacyUserID || holderEmail != email {
			t.Fatalf("run %d: holder = %s/%s, want %s/%s (rollback must restore both)",
				run, holderID, holderEmail, legacyUserID, email)
		}
		if got := countWhere(t, legacy,
			`SELECT count(*) FROM organization_members WHERE user_id = $1`, legacyUserID); got != 1 {
			t.Fatalf("run %d: legacy membership moved despite the failed re-key", run)
		}

		if got := countWhere(t, legacy, `SELECT count(*) FROM backfill_user_id_map`); got != 1 {
			t.Fatalf("run %d: mapping rows = %d, want 1", run, got)
		}
		var reason string
		if err := legacy.QueryRow(ctx,
			`SELECT reason FROM backfill_user_id_map WHERE identity_user_id = $1`, identityUserID,
		).Scan(&reason); err != nil {
			t.Fatalf("read mapping: %v", err)
		}
		if !strings.Contains(reason, "re-key failed") {
			t.Fatalf("mapping reason = %q, want the re-key failure", reason)
		}
		assertUserForeignKeysResolve(t, legacy)
	}

	if !recorder.contains("re-key failed") {
		t.Fatalf("expected a failed-re-key log line, got: %v", recorder.lines)
	}
}

func TestRunRekeysUnconstrainedUUIDUserReference(t *testing.T) {
	identity := testutil.NewPostgresWithMigrations(t, "services/identity/migrations")
	workspace := testutil.NewPostgresWithMigrations(t, "services/workspace/migrations")
	legacy := testutil.NewPostgres(t)
	ctx := context.Background()

	const email = "unconstrained@example.test"
	legacyUserID := uuid.NewString()
	identityUserID := uuid.NewString()
	if _, err := legacy.Exec(ctx, `CREATE TABLE audit_events (actor_id uuid NOT NULL)`); err != nil {
		t.Fatalf("create unconstrained reference table: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'legacy-hash', 'Legacy User', 'active')
	`, legacyUserID, email); err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	if _, err := legacy.Exec(ctx, `INSERT INTO audit_events (actor_id) VALUES ($1)`, legacyUserID); err != nil {
		t.Fatalf("seed unconstrained actor id: %v", err)
	}
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'identity-hash', 'Identity User', 'active')
	`, identityUserID, email); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}

	if _, err := Run(ctx, identity, workspace, legacy, nil); err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	var actorID string
	if err := legacy.QueryRow(ctx, `SELECT actor_id FROM audit_events`).Scan(&actorID); err != nil {
		t.Fatalf("read moved actor id: %v", err)
	}
	if actorID != identityUserID {
		t.Fatalf("audit_events.actor_id = %s, want %s", actorID, identityUserID)
	}
}

func TestDryRunReportsRekeyPlanWithoutWriting(t *testing.T) {
	identity := testutil.NewPostgresWithMigrations(t, "services/identity/migrations")
	workspace := testutil.NewPostgresWithMigrations(t, "services/workspace/migrations")
	legacy := testutil.NewPostgres(t)
	ctx := context.Background()

	const email = "dry-run@example.test"
	legacyUserID := uuid.NewString()
	identityUserID := uuid.NewString()
	occupiedIdentityID := uuid.NewString()
	skippedLegacyUserID := uuid.NewString()
	if _, err := legacy.Exec(ctx, `CREATE TABLE audit_events (actor_id uuid NOT NULL)`); err != nil {
		t.Fatalf("create unconstrained reference table: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'legacy-hash', 'Legacy User', 'active')
	`, legacyUserID, email); err != nil {
		t.Fatalf("seed legacy user: %v", err)
	}
	if _, err := legacy.Exec(ctx, `INSERT INTO audit_events (actor_id) VALUES ($1)`, legacyUserID); err != nil {
		t.Fatalf("seed unconstrained actor id: %v", err)
	}
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, $2, 'identity-hash', 'Identity User', 'active')
	`, identityUserID, email); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	if _, err := legacy.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, 'occupied@example.test', 'legacy-hash', 'Occupied User', 'active'),
		       ($2, 'skip-dry-run@example.test', 'legacy-hash', 'Email Holder', 'active')
	`, occupiedIdentityID, skippedLegacyUserID); err != nil {
		t.Fatalf("seed occupied and skipped legacy users: %v", err)
	}
	if _, err := identity.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, 'skip-dry-run@example.test', 'identity-hash', 'Skipped Identity', 'active')
	`, occupiedIdentityID); err != nil {
		t.Fatalf("seed identity user with occupied legacy id: %v", err)
	}

	var output bytes.Buffer
	if err := DryRun(ctx, identity, workspace, legacy, &output); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	t.Logf("dry-run output:\n%s", output.String())
	for _, expected := range []string{
		"outcome=re-key",
		"outcome=skip reason=identity_id_already_exists",
		"UPDATE users.id:",
		".audit_events.actor_id (unconstrained UUID candidate)",
		"SUMMARY email_clashes=2 rekeys=1 skips=1",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("dry-run output missing %q:\n%s", expected, output.String())
		}
	}
	if got := countWhere(t, legacy, `SELECT count(*) FROM users WHERE id = $1`, legacyUserID); got != 1 {
		t.Fatalf("dry run changed legacy users: old id count = %d, want 1", got)
	}
	if got := countWhere(t, legacy, `SELECT count(*) FROM audit_events WHERE actor_id = $1`, legacyUserID); got != 1 {
		t.Fatalf("dry run changed unconstrained reference: old id count = %d, want 1", got)
	}
	if got := countWhere(t, legacy, `SELECT count(*) FROM users WHERE id = $1`, skippedLegacyUserID); got != 1 {
		t.Fatalf("dry run changed skipped legacy user: old id count = %d, want 1", got)
	}
}
