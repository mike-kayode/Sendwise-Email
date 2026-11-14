package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khayode/email-platform/internal/outbox"
)

type adminMFAState struct {
	Secret  string
	Enabled bool
}

type adminAuditRecord struct {
	ID          int64           `json:"id"`
	ActorUserID *string         `json:"actor_user_id,omitempty"`
	ActorRole   string          `json:"actor_role"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type,omitempty"`
	TargetID    string          `json:"target_id,omitempty"`
	WorkspaceID *string         `json:"workspace_id,omitempty"`
	BeforeState json.RawMessage `json:"before_state,omitempty"`
	AfterState  json.RawMessage `json:"after_state,omitempty"`
	Reason      string          `json:"reason"`
	IPAddress   string          `json:"ip_address,omitempty"`
	UserAgent   string          `json:"user_agent,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (s *pgxStore) GetAdminMFA(ctx context.Context, userID string) (adminMFAState, error) {
	var state adminMFAState
	err := s.pool.QueryRow(ctx, `
		SELECT secret, enabled FROM admin_mfa WHERE user_id = $1
	`, userID).Scan(&state.Secret, &state.Enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return adminMFAState{}, nil
		}
		return adminMFAState{}, fmt.Errorf("load admin MFA state: %w", err)
	}
	return state, nil
}

func (s *pgxStore) SaveAdminMFASetup(ctx context.Context, userID, secret string, recoveryCodeHashes []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin admin MFA setup: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO admin_mfa (user_id, secret, enabled)
		VALUES ($1, $2, false)
		ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, enabled = false, updated_at = now()
		WHERE admin_mfa.enabled = false
	`, userID, secret)
	if err != nil {
		return fmt.Errorf("save admin MFA setup: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("admin MFA is already enabled")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM admin_mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("replace admin recovery codes: %w", err)
	}
	for _, codeHash := range recoveryCodeHashes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO admin_mfa_recovery_codes (user_id, code_hash) VALUES ($1, $2)
		`, userID, codeHash); err != nil {
			return fmt.Errorf("store admin recovery code: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit admin MFA setup: %w", err)
	}
	return nil
}

func (s *pgxStore) EnableAdminMFA(ctx context.Context, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE admin_mfa SET enabled = true, updated_at = now()
		WHERE user_id = $1 AND enabled = false
	`, userID)
	if err != nil {
		return fmt.Errorf("enable admin MFA: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("admin MFA setup is missing or already enabled")
	}
	return nil
}

func (s *pgxStore) ConsumeAdminRecoveryCode(ctx context.Context, userID, codeHash string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE admin_mfa_recovery_codes SET used_at = now()
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
	`, userID, codeHash)
	if err != nil {
		return false, fmt.Errorf("consume admin recovery code: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *pgxStore) MarkAdminSessionMFAVerified(ctx context.Context, sessionID, userID string, verifiedAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET admin_mfa_verified_at = $3
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL AND expires_at > now()
	`, sessionID, userID, verifiedAt)
	if err != nil {
		return fmt.Errorf("mark admin session MFA verified: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *pgxStore) AdminMFALocked(ctx context.Context, userID string) (bool, error) {
	var locked bool
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(locked_until > now(), false)
		FROM admin_mfa_lockouts WHERE user_id = $1
	`, userID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check admin MFA lockout: %w", err)
	}
	return locked, nil
}

func (s *pgxStore) RecordAdminMFAFailure(ctx context.Context, userID string) (bool, error) {
	var locked bool
	err := s.pool.QueryRow(ctx, `
		INSERT INTO admin_mfa_lockouts (user_id, failed_attempts)
		VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET
			failed_attempts = CASE
				WHEN admin_mfa_lockouts.locked_until IS NOT NULL
				 AND admin_mfa_lockouts.locked_until <= now() THEN 1
				ELSE admin_mfa_lockouts.failed_attempts + 1
			END,
			locked_until = CASE
				WHEN CASE
					WHEN admin_mfa_lockouts.locked_until IS NOT NULL
					 AND admin_mfa_lockouts.locked_until <= now() THEN 1
					ELSE admin_mfa_lockouts.failed_attempts + 1
				END >= 5 THEN now() + interval '15 minutes'
				ELSE admin_mfa_lockouts.locked_until
			END,
			updated_at = now()
		RETURNING COALESCE(locked_until > now(), false)
	`, userID).Scan(&locked)
	if err != nil {
		return false, fmt.Errorf("record admin MFA failure: %w", err)
	}
	return locked, nil
}

func (s *pgxStore) ClearAdminMFAFailures(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM admin_mfa_lockouts WHERE user_id = $1
	`, userID)
	if err != nil {
		return fmt.Errorf("clear admin MFA failures: %w", err)
	}
	return nil
}

func (s *pgxStore) WriteAdminAudit(ctx context.Context, record adminAuditRecord) error {
	return insertAdminAudit(ctx, s.pool, record)
}

type adminAuditExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type adminUserQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func insertAdminAudit(ctx context.Context, executor adminAuditExecutor, record adminAuditRecord) error {
	_, err := executor.Exec(ctx, `
		INSERT INTO admin_audit (
			actor_user_id, actor_role, action, target_type, target_id, workspace_id,
			before_state, after_state, reason, ip_address, user_agent, request_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, '')::inet, $11, $12
		)
	`, record.ActorUserID, record.ActorRole, record.Action, record.TargetType, record.TargetID,
		record.WorkspaceID, nullableJSON(record.BeforeState), nullableJSON(record.AfterState),
		record.Reason, record.IPAddress, record.UserAgent, record.RequestID)
	if err != nil {
		return fmt.Errorf("write admin audit: %w", err)
	}
	return nil
}

func (s *pgxStore) ListAdminAudit(ctx context.Context, limit, offset int) ([]adminAuditRecord, error) {
	records, _, err := s.ListAdminAuditFiltered(ctx, "", "", limit, offset)
	return records, err
}

func (s *pgxStore) ListAdminAuditFiltered(ctx context.Context, action, actorID string, limit, offset int) ([]adminAuditRecord, int, error) {
	filter := `
		WHERE ($1 = '' OR action = $1)
		  AND ($2 = '' OR actor_user_id::text = $2)
	`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit `+filter, action, actorID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin audit records: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_user_id::text, actor_role, action, target_type, target_id,
		       workspace_id::text, before_state, after_state, reason,
		       COALESCE(host(ip_address), ''), user_agent, request_id, created_at
		FROM admin_audit
	`+filter+`
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`, action, actorID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()
	records := make([]adminAuditRecord, 0, limit)
	for rows.Next() {
		var record adminAuditRecord
		var actorID, workspaceID *string
		if err := rows.Scan(&record.ID, &actorID, &record.ActorRole, &record.Action,
			&record.TargetType, &record.TargetID, &workspaceID, &record.BeforeState,
			&record.AfterState, &record.Reason, &record.IPAddress, &record.UserAgent,
			&record.RequestID, &record.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan admin audit record: %w", err)
		}
		record.ActorUserID = actorID
		record.WorkspaceID = workspaceID
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin audit: %w", err)
	}
	return records, total, nil
}

func (s *pgxStore) ListAdminWorkspaceNotes(ctx context.Context, workspaceID string, limit int) ([]adminWorkspaceNote, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_user_id::text, after_state->>'note', reason, created_at
		FROM admin_audit
		WHERE action = 'admin_workspace_note_added'
		  AND target_type = 'workspace'
		  AND target_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list admin workspace notes: %w", err)
	}
	defer rows.Close()
	notes := make([]adminWorkspaceNote, 0, limit)
	for rows.Next() {
		var note adminWorkspaceNote
		if err := rows.Scan(&note.ID, &note.ActorUserID, &note.Note, &note.Reason, &note.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan admin workspace note: %w", err)
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin workspace notes: %w", err)
	}
	return notes, nil
}

func (s *pgxStore) ListAdminUsers(ctx context.Context, search, status string, limit, offset int) ([]adminUserSummary, int, error) {
	filter := `
		WHERE ($1 = '' OR email ILIKE '%' || $1 || '%' OR name ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR status = $2)
	`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users `+filter, search, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin users: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, email, name, status, platform_role, email_verified_at, last_login_at, created_at
		FROM users
	`+filter+`
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`, search, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()
	users := make([]adminUserSummary, 0, limit)
	for rows.Next() {
		var user adminUserSummary
		var verifiedAt, lastLoginAt sql.NullTime
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.Status,
			&user.PlatformRole, &verifiedAt, &lastLoginAt, &user.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan admin user: %w", err)
		}
		if verifiedAt.Valid {
			user.EmailVerifiedAt = &verifiedAt.Time
		}
		if lastLoginAt.Valid {
			user.LastLoginAt = &lastLoginAt.Time
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin users: %w", err)
	}
	return users, total, nil
}

func (s *pgxStore) GetAdminUser(ctx context.Context, userID string) (adminUserSummary, error) {
	return getAdminUser(ctx, s.pool, userID)
}

func (s *pgxStore) GetAdminUserCounts(ctx context.Context) (adminUserCounts, error) {
	var counts adminUserCounts
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)::int,
		       count(*) FILTER (WHERE status = 'active')::int,
		       count(*) FILTER (WHERE status = 'pending')::int,
		       count(*) FILTER (WHERE status = 'suspended')::int,
		       count(*) FILTER (WHERE status = 'closed')::int
		FROM users
	`).Scan(&counts.Total, &counts.Active, &counts.Pending, &counts.Suspended, &counts.Closed)
	if err != nil {
		return adminUserCounts{}, fmt.Errorf("count admin users: %w", err)
	}
	return counts, nil
}

func getAdminUser(ctx context.Context, queryer adminUserQueryer, userID string) (adminUserSummary, error) {
	var user adminUserSummary
	var verifiedAt, lastLoginAt sql.NullTime
	err := queryer.QueryRow(ctx, `
		SELECT id, email, name, status, platform_role, email_verified_at, last_login_at, created_at
		FROM users
		WHERE id = $1
	`, userID).Scan(&user.ID, &user.Email, &user.Name, &user.Status,
		&user.PlatformRole, &verifiedAt, &lastLoginAt, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return adminUserSummary{}, ErrNotFound
	}
	if err != nil {
		return adminUserSummary{}, fmt.Errorf("load admin user: %w", err)
	}
	if verifiedAt.Valid {
		user.EmailVerifiedAt = &verifiedAt.Time
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	return user, nil
}

func (s *pgxStore) SetAdminUserStatus(ctx context.Context, userID, status string, record adminAuditRecord) (adminUserSummary, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("begin admin user status update: %w", err)
	}
	defer tx.Rollback(ctx)

	var lockedUserID string
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return adminUserSummary{}, 0, ErrNotFound
		}
		return adminUserSummary{}, 0, fmt.Errorf("lock admin user for status update: %w", err)
	}
	before, err := getAdminUser(ctx, tx, userID)
	if err != nil {
		return adminUserSummary{}, 0, err
	}
	if (before.Status != "active" && before.Status != "suspended") || before.Status == status {
		return adminUserSummary{}, 0, ErrInvalidUserStatus
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status = $2, updated_at = now() WHERE id = $1`, userID, status); err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("update admin user status: %w", err)
	}
	sessionsRevoked := 0
	if status == "suspended" {
		tag, err := tx.Exec(ctx, `
			UPDATE sessions SET revoked_at = now()
			WHERE user_id = $1 AND revoked_at IS NULL
		`, userID)
		if err != nil {
			return adminUserSummary{}, 0, fmt.Errorf("revoke suspended user's sessions: %w", err)
		}
		sessionsRevoked = int(tag.RowsAffected())
	}
	beforeState, err := json.Marshal(map[string]string{"status": before.Status})
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("marshal admin user before state: %w", err)
	}
	afterState, err := json.Marshal(map[string]any{
		"status": status, "sessions_revoked": sessionsRevoked,
	})
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("marshal admin user after state: %w", err)
	}
	record.BeforeState = beforeState
	record.AfterState = afterState
	if err := insertAdminAudit(ctx, tx, record); err != nil {
		return adminUserSummary{}, 0, err
	}
	updated, err := getAdminUser(ctx, tx, userID)
	if err != nil {
		return adminUserSummary{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("commit admin user status update: %w", err)
	}
	return updated, sessionsRevoked, nil
}

func (s *pgxStore) VerifyAdminUserEmail(ctx context.Context, userID string, record adminAuditRecord) (adminUserSummary, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return adminUserSummary{}, fmt.Errorf("begin admin email verification: %w", err)
	}
	defer tx.Rollback(ctx)

	var lockedUserID string
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return adminUserSummary{}, ErrNotFound
		}
		return adminUserSummary{}, fmt.Errorf("lock admin user for verification: %w", err)
	}
	before, err := getAdminUser(ctx, tx, userID)
	if err != nil {
		return adminUserSummary{}, err
	}
	if before.Status != "pending" || before.EmailVerifiedAt != nil {
		return adminUserSummary{}, ErrInvalidUserStatus
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET email_verified_at = now(), status = 'active', updated_at = now()
		WHERE id = $1
	`, userID); err != nil {
		return adminUserSummary{}, fmt.Errorf("verify admin user's email: %w", err)
	}
	user := &User{ID: before.ID, Email: before.Email, Name: before.Name, Status: "active"}
	if err := outbox.Enqueue(ctx, tx, "identity.user_verified.v1", userVerifiedEvent(user, record.RequestID)); err != nil {
		return adminUserSummary{}, fmt.Errorf("enqueue admin user_verified event: %w", err)
	}
	beforeState, err := json.Marshal(map[string]any{
		"status": before.Status, "email_verified": false,
	})
	if err != nil {
		return adminUserSummary{}, fmt.Errorf("marshal admin verification before state: %w", err)
	}
	afterState, err := json.Marshal(map[string]any{
		"status": "active", "email_verified": true,
	})
	if err != nil {
		return adminUserSummary{}, fmt.Errorf("marshal admin verification after state: %w", err)
	}
	record.BeforeState = beforeState
	record.AfterState = afterState
	if err := insertAdminAudit(ctx, tx, record); err != nil {
		return adminUserSummary{}, err
	}
	updated, err := getAdminUser(ctx, tx, userID)
	if err != nil {
		return adminUserSummary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return adminUserSummary{}, fmt.Errorf("commit admin email verification: %w", err)
	}
	return updated, nil
}

func (s *pgxStore) DemoteAdminUser(ctx context.Context, userID string, record adminAuditRecord) (adminUserSummary, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("begin admin role removal: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('sendwise_platform_role_changes'))`); err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("serialize admin role changes: %w", err)
	}
	var lockedUserID string
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return adminUserSummary{}, 0, ErrNotFound
		}
		return adminUserSummary{}, 0, fmt.Errorf("lock admin user for role removal: %w", err)
	}
	before, err := getAdminUser(ctx, tx, userID)
	if err != nil {
		return adminUserSummary{}, 0, err
	}
	if before.PlatformRole == PlatformRoleNone {
		return adminUserSummary{}, 0, ErrInvalidUserStatus
	}
	if before.PlatformRole == PlatformRoleSuperAdmin && before.Status == "active" {
		var activeSuperAdmins int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM users
			WHERE platform_role = 'super_admin' AND status = 'active'
		`).Scan(&activeSuperAdmins); err != nil {
			return adminUserSummary{}, 0, fmt.Errorf("count active super admins: %w", err)
		}
		if activeSuperAdmins <= 1 {
			return adminUserSummary{}, 0, ErrLastSuperAdmin
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET platform_role = 'none', updated_at = now()
		WHERE id = $1
	`, userID); err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("remove admin platform role: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("revoke demoted user's sessions: %w", err)
	}
	sessionsRevoked := int(tag.RowsAffected())
	beforeState, err := json.Marshal(map[string]string{"platform_role": before.PlatformRole})
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("marshal admin role before state: %w", err)
	}
	afterState, err := json.Marshal(map[string]any{
		"platform_role": PlatformRoleNone, "sessions_revoked": sessionsRevoked,
	})
	if err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("marshal admin role after state: %w", err)
	}
	record.BeforeState = beforeState
	record.AfterState = afterState
	if err := insertAdminAudit(ctx, tx, record); err != nil {
		return adminUserSummary{}, 0, err
	}
	updated, err := getAdminUser(ctx, tx, userID)
	if err != nil {
		return adminUserSummary{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return adminUserSummary{}, 0, fmt.Errorf("commit admin role removal: %w", err)
	}
	return updated, sessionsRevoked, nil
}

func (s *pgxStore) RevokeAdminUserSessions(ctx context.Context, userID string, record adminAuditRecord) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin admin session revocation: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := getAdminUser(ctx, tx, userID); err != nil {
		return 0, err
	}
	var activeSessions int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL
	`, userID).Scan(&activeSessions); err != nil {
		return 0, fmt.Errorf("count admin target sessions: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke admin target sessions: %w", err)
	}
	revoked := int(tag.RowsAffected())
	beforeState, err := json.Marshal(map[string]int{"active_sessions": activeSessions})
	if err != nil {
		return 0, fmt.Errorf("marshal admin session before state: %w", err)
	}
	afterState, err := json.Marshal(map[string]int{"sessions_revoked": revoked})
	if err != nil {
		return 0, fmt.Errorf("marshal admin session after state: %w", err)
	}
	record.BeforeState = beforeState
	record.AfterState = afterState
	if err := insertAdminAudit(ctx, tx, record); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit admin session revocation: %w", err)
	}
	return revoked, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return []byte(value)
}
