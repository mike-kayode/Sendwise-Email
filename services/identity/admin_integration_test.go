package identity

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/testutil"
)

func TestPostgresAdminAuditIsAppendOnly(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO admin_audit (actor_role, action, reason)
		VALUES ('operator', 'test_bootstrap', 'integration test')
	`); err != nil {
		t.Fatalf("insert admin audit row: %v", err)
	}
	auditStore := NewStoreFromPool(pool).(adminAuditSearchStore)
	auditRows, auditTotal, err := auditStore.ListAdminAuditFiltered(ctx, "test_bootstrap", "", 10, 0)
	if err != nil {
		t.Fatalf("filter admin audit rows: %v", err)
	}
	if auditTotal != 1 || len(auditRows) != 1 || auditRows[0].Action != "test_bootstrap" {
		t.Fatalf("filtered audit result = %d rows, total %d; want 1, 1", len(auditRows), auditTotal)
	}
	for _, statement := range []string{
		`UPDATE admin_audit SET reason = 'changed'`,
		`DELETE FROM admin_audit`,
		`TRUNCATE admin_audit`,
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Errorf("%s unexpectedly modified append-only admin_audit", statement)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit`).Scan(&count); err != nil {
		t.Fatalf("count admin audit rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("admin audit row count = %d after rejected mutations, want 1", count)
	}
}

func TestPostgresAdminUsersSearchIsPaginatedAndMetadataOnly(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (email, password_hash, name, status, platform_role, email_verified_at, last_login_at)
		VALUES
			('alice@example.test', 'not-returned', 'Alice Example', 'active', 'none', now(), now()),
			('bob@example.test', 'also-not-returned', 'Bob Example', 'pending', 'support_admin', NULL, NULL)
	`); err != nil {
		t.Fatalf("insert admin user fixtures: %v", err)
	}

	store := NewStoreFromPool(pool).(adminUserStore)
	users, total, err := store.ListAdminUsers(ctx, "alice", "active", 1, 0)
	if err != nil {
		t.Fatalf("list admin users: %v", err)
	}
	if total != 1 || len(users) != 1 {
		t.Fatalf("admin user result count = %d, total = %d; want 1, 1", len(users), total)
	}
	if users[0].Email != "alice@example.test" || users[0].Name != "Alice Example" ||
		users[0].Status != "active" || users[0].PlatformRole != PlatformRoleNone {
		t.Fatalf("unexpected admin user metadata: %+v", users[0])
	}
	if users[0].EmailVerifiedAt == nil || users[0].LastLoginAt == nil {
		t.Fatalf("expected verification and last-login timestamps: %+v", users[0])
	}
	counts, err := store.GetAdminUserCounts(ctx)
	if err != nil {
		t.Fatalf("count admin users: %v", err)
	}
	if counts.Total != 2 || counts.Active != 1 || counts.Pending != 1 {
		t.Fatalf("admin user counts = %+v; want total=2 active=1 pending=1", counts)
	}
	details, err := store.GetAdminUser(ctx, users[0].ID)
	if err != nil {
		t.Fatalf("load admin user detail: %v", err)
	}
	if details.ID != users[0].ID || details.Email != "alice@example.test" ||
		details.Name != "Alice Example" || details.Status != "active" {
		t.Fatalf("unexpected admin user details: %+v", details)
	}

	page, pageTotal, err := store.ListAdminUsers(ctx, "", "", 1, 1)
	if err != nil {
		t.Fatalf("list second admin user page: %v", err)
	}
	if pageTotal != 2 || len(page) != 1 {
		t.Fatalf("second page count = %d, total = %d; want 1, 2", len(page), pageTotal)
	}
}

func TestPostgresAdminUserStatusChangeRevokesSessionsAndAuditsAtomically(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status)
		VALUES ('suspend@example.test', 'not-returned', 'Suspended User', 'active')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert admin status fixture: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (user_id, refresh_token_hash, expires_at)
		VALUES ($1, 'active-session-one', now() + interval '1 hour'),
		       ($1, 'active-session-two', now() + interval '1 hour')
	`, userID); err != nil {
		t.Fatalf("insert active sessions: %v", err)
	}

	store := NewStoreFromPool(pool).(*pgxStore)
	record := adminAuditRecord{
		ActorRole:  PlatformRoleSuperAdmin,
		Action:     "admin_user_status_changed",
		TargetType: "user",
		TargetID:   userID,
		Reason:     "Confirmed abuse report",
	}
	updated, revoked, err := store.SetAdminUserStatus(ctx, userID, "suspended", record)
	if err != nil {
		t.Fatalf("suspend admin target: %v", err)
	}
	if updated.Status != "suspended" || revoked != 2 {
		t.Fatalf("updated user=%+v sessions revoked=%d; want suspended, 2", updated, revoked)
	}
	var activeSessions, auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL
	`, userID).Scan(&activeSessions); err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM admin_audit
		WHERE action = 'admin_user_status_changed' AND target_id = $1
	`, userID).Scan(&auditCount); err != nil {
		t.Fatalf("count status audit entries: %v", err)
	}
	if activeSessions != 0 || auditCount != 1 {
		t.Fatalf("active sessions=%d audit entries=%d; want 0 and 1", activeSessions, auditCount)
	}
	auditStore := NewStoreFromPool(pool).(adminAuditSearchStore)
	audits, _, err := auditStore.ListAdminAuditFiltered(ctx, "admin_user_status_changed", "", 10, 0)
	if err != nil {
		t.Fatalf("load user status audit: %v", err)
	}
	var before, after map[string]any
	if len(audits) == 1 {
		if err := json.Unmarshal(audits[0].BeforeState, &before); err != nil {
			t.Fatalf("decode audit before state: %v", err)
		}
		if err := json.Unmarshal(audits[0].AfterState, &after); err != nil {
			t.Fatalf("decode audit after state: %v", err)
		}
	}

	if len(audits) != 1 || audits[0].Reason != "Confirmed abuse report" ||
		before["status"] != "active" || after["status"] != "suspended" ||
		after["sessions_revoked"] != float64(2) {
		t.Fatalf("unexpected user status audit record: %+v", audits)
	}
}

func TestPostgresAdminEmailVerificationWritesOutboxAndAuditAtomically(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status)
		VALUES ('verify@example.test', 'not-returned', 'Verify User', 'pending')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert admin verification fixture: %v", err)
	}

	store := NewStoreFromPool(pool).(*pgxStore)
	record := adminAuditRecord{
		ActorRole:  PlatformRoleSuperAdmin,
		Action:     "admin_user_email_verified",
		TargetType: "user",
		TargetID:   userID,
		Reason:     "Verified through support case",
		RequestID:  "admin-verification-test",
	}
	updated, err := store.VerifyAdminUserEmail(ctx, userID, record)
	if err != nil {
		t.Fatalf("verify admin target email: %v", err)
	}
	if updated.Status != "active" || updated.EmailVerifiedAt == nil {
		t.Fatalf("verified user metadata = %+v; want active and verified", updated)
	}
	var eventCount, auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM event_outbox WHERE subject = 'identity.user_verified.v1'
	`).Scan(&eventCount); err != nil {
		t.Fatalf("count user verified outbox events: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM admin_audit
		WHERE action = 'admin_user_email_verified' AND target_id = $1 AND reason = $2
	`, userID, record.Reason).Scan(&auditCount); err != nil {
		t.Fatalf("count email verification audit rows: %v", err)
	}
	if eventCount != 1 || auditCount != 1 {
		t.Fatalf("outbox events=%d audit rows=%d; want 1 and 1", eventCount, auditCount)
	}
}

func TestPostgresAdminRoleDemotionRevokesSessionsAndProtectsLastSuperAdmin(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	var firstAdminID, targetAdminID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status, platform_role)
		VALUES ('first-admin@example.test', 'hash', 'First Admin', 'active', 'super_admin')
		RETURNING id
	`).Scan(&firstAdminID); err != nil {
		t.Fatalf("insert first super admin: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status, platform_role)
		VALUES ('target-admin@example.test', 'hash', 'Target Admin', 'active', 'super_admin')
		RETURNING id
	`).Scan(&targetAdminID); err != nil {
		t.Fatalf("insert target super admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (user_id, refresh_token_hash, expires_at)
		VALUES ($1, 'target-admin-session', now() + interval '1 hour')
	`, targetAdminID); err != nil {
		t.Fatalf("insert target admin session: %v", err)
	}
	store := NewStoreFromPool(pool).(*pgxStore)
	record := adminAuditRecord{
		ActorUserID: &firstAdminID,
		ActorRole:   PlatformRoleSuperAdmin,
		Action:      "admin_platform_role_removed",
		TargetType:  "user",
		TargetID:    targetAdminID,
		Reason:      "Operator access ended",
	}
	updated, revoked, err := store.DemoteAdminUser(ctx, targetAdminID, record)
	if err != nil {
		t.Fatalf("demote target super admin: %v", err)
	}
	if updated.PlatformRole != PlatformRoleNone || revoked != 1 {
		t.Fatalf("demoted user=%+v sessions revoked=%d; want none, 1", updated, revoked)
	}
	if _, _, err := store.DemoteAdminUser(ctx, firstAdminID, record); !errors.Is(err, ErrLastSuperAdmin) {
		t.Fatalf("demoting last active super admin error = %v; want ErrLastSuperAdmin", err)
	}
	var role string
	if err := pool.QueryRow(ctx, `SELECT platform_role FROM users WHERE id = $1`, firstAdminID).Scan(&role); err != nil {
		t.Fatalf("read remaining super-admin role: %v", err)
	}
	if role != PlatformRoleSuperAdmin {
		t.Fatalf("last super-admin role = %q; want super_admin", role)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM admin_audit
		WHERE action = 'admin_platform_role_removed'
	`).Scan(&auditCount); err != nil {
		t.Fatalf("count role-demotion audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("role-demotion audit count=%d; want exactly one successful demotion", auditCount)
	}
}

func TestPostgresAdminWorkspaceNotesAreAuditedAndWorkspaceScoped(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	store := NewStoreFromPool(pool).(*pgxStore)
	for _, entry := range []struct {
		workspaceID string
		note        string
	}{
		{"workspace-a", "First note"},
		{"workspace-a", "Second note"},
		{"workspace-b", "Other workspace"},
	} {
		afterState, err := json.Marshal(map[string]string{"note": entry.note})
		if err != nil {
			t.Fatalf("marshal workspace note: %v", err)
		}
		if err := store.WriteAdminAudit(ctx, adminAuditRecord{
			ActorRole:  PlatformRoleSuperAdmin,
			Action:     "admin_workspace_note_added",
			TargetType: "workspace",
			TargetID:   entry.workspaceID,
			AfterState: afterState,
			Reason:     "Integration test note",
		}); err != nil {
			t.Fatalf("write workspace note audit: %v", err)
		}
	}

	notes, err := store.ListAdminWorkspaceNotes(ctx, "workspace-a", 1)
	if err != nil {
		t.Fatalf("list workspace notes: %v", err)
	}
	if len(notes) != 1 || notes[0].Note != "Second note" || notes[0].Reason != "Integration test note" {
		t.Fatalf("workspace note result = %+v; want latest workspace-a note only", notes)
	}
}

func TestPostgresAdminPasswordResetUsesHashedSingleUseToken(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status, email_verified_at)
		VALUES ('reset-target@example.test', 'hash', 'Reset Target', 'active', now())
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert password reset target: %v", err)
	}
	store := NewStoreFromPool(pool)
	rawToken := "single-use-admin-reset-token"
	tokenHash := HashToken(rawToken)
	if err := store.CreateVerificationToken(ctx, userID, "reset-target@example.test",
		tokenHash, "password_reset", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create admin password reset token: %v", err)
	}
	var persistedHash string
	if err := pool.QueryRow(ctx, `SELECT token_hash FROM verification_tokens WHERE user_id = $1`, userID).Scan(&persistedHash); err != nil {
		t.Fatalf("read stored password reset token hash: %v", err)
	}
	if persistedHash != tokenHash || persistedHash == rawToken {
		t.Fatalf("stored token hash = %q; want hashed token, not raw token", persistedHash)
	}
	consumedUserID, email, err := store.ConsumeVerificationToken(ctx, tokenHash, "password_reset")
	if err != nil || consumedUserID != userID || email != "reset-target@example.test" {
		t.Fatalf("consume admin password reset token = %q, %q, %v", consumedUserID, email, err)
	}
	if _, _, err := store.ConsumeVerificationToken(ctx, tokenHash, "password_reset"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("reusing password reset token error = %v; want ErrInvalidToken", err)
	}
}
