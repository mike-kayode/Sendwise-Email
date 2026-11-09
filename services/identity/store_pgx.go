package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/outbox"
)

// pgxStore is the production Store backed by the identity database.
type pgxStore struct {
	pool *pgxpool.Pool
}

// NewStore connects to the identity database.
func NewStore(ctx context.Context, databaseURL string) (Store, error) {
	pool, err := kit.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect identity database: %w", err)
	}
	return &pgxStore{pool: pool}, nil
}

// NewStoreFromPool adapts an existing pool (used by tests with a live database).
func NewStoreFromPool(pool *pgxpool.Pool) Store {
	return &pgxStore{pool: pool}
}

func (s *pgxStore) Ping(ctx context.Context) error { return kit.MustPing(ctx, s.pool) }
func (s *pgxStore) Close()                         { s.pool.Close() }

// Pool exposes the underlying pool for infrastructure wiring (outbox relay).
func (s *pgxStore) Pool() *pgxpool.Pool { return s.pool }

// HashToken hashes a high-entropy token with SHA-256 for at-rest storage.
func HashToken(token string) string { return kit.HashSecret(token) }

func (s *pgxStore) CreateUser(ctx context.Context, email, passwordHash, name string) (*User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id, email, name, status, password_hash, created_at
	`, email, passwordHash, name).Scan(
		&user.ID, &user.Email, &user.Name, &user.Status, &user.PasswordHash, &user.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailInUse
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &user, nil
}

func (s *pgxStore) CreateUserWithRegistration(ctx context.Context, email, passwordHash, name, organizationName, correlationID string) (*User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin registration transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var user User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, name, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id, email, name, status, password_hash, created_at
	`, email, passwordHash, name).Scan(
		&user.ID, &user.Email, &user.Name, &user.Status, &user.PasswordHash, &user.CreatedAt,
	)
	if isUniqueViolation(err) {
		return nil, ErrEmailInUse
	}
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	if err := outbox.Enqueue(ctx, tx, "identity.user_registered.v1",
		userRegisteredEvent(&user, organizationName, correlationID)); err != nil {
		return nil, fmt.Errorf("enqueue user_registered: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit registration: %w", err)
	}
	return &user, nil
}

func (s *pgxStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		SELECT id, email, name, status, password_hash, email_verified_at, last_login_at, created_at
		FROM users WHERE lower(email) = lower($1)
	`, email))
}

func (s *pgxStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		SELECT id, email, name, status, password_hash, email_verified_at, last_login_at, created_at
		FROM users WHERE id = $1
	`, id))
}

type rowScanner interface{ Scan(dest ...any) error }

func (s *pgxStore) scanUser(row rowScanner) (*User, error) {
	var user User
	var verifiedAt, lastLoginAt sql.NullTime
	err := row.Scan(&user.ID, &user.Email, &user.Name, &user.Status, &user.PasswordHash, &verifiedAt, &lastLoginAt, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if verifiedAt.Valid {
		user.EmailVerifiedAt = &verifiedAt.Time
	}
	if lastLoginAt.Valid {
		user.LastLoginAt = &lastLoginAt.Time
	}
	return &user, nil
}

func (s *pgxStore) SetPassword(ctx context.Context, userID, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, passwordHash, userID)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *pgxStore) RecordLogin(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	return nil
}

func (s *pgxStore) CreateVerificationToken(ctx context.Context, userID, email, tokenHash, purpose string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO verification_tokens (user_id, token_hash, email, purpose, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, tokenHash, email, purpose, expiresAt)
	if err != nil {
		return fmt.Errorf("create verification token: %w", err)
	}
	return nil
}

func (s *pgxStore) ConsumeVerificationToken(ctx context.Context, tokenHash, purpose string) (string, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", "", fmt.Errorf("begin consume token: %w", err)
	}
	defer tx.Rollback(ctx)

	var userID, email string
	err = tx.QueryRow(ctx, `
		SELECT user_id, email FROM verification_tokens
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
		FOR UPDATE
	`, tokenHash, purpose).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrInvalidToken
	}
	if err != nil {
		return "", "", fmt.Errorf("load verification token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE verification_tokens SET used_at = now()
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL
	`, tokenHash, purpose); err != nil {
		return "", "", fmt.Errorf("mark token used: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("commit token consumption: %w", err)
	}
	return userID, email, nil
}

func (s *pgxStore) MarkEmailVerified(ctx context.Context, userID, email, correlationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin verify transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var user User
	err = tx.QueryRow(ctx, `
		UPDATE users
		SET email_verified_at = COALESCE(email_verified_at, now()), status = 'active'
		WHERE id = $1
		RETURNING id, email, name, status, password_hash, created_at
	`, userID).Scan(&user.ID, &user.Email, &user.Name, &user.Status, &user.PasswordHash, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	if email == "" {
		email = user.Email
	}
	if err := outbox.Enqueue(ctx, tx, "identity.user_verified.v1",
		userVerifiedEvent(&user, correlationID)); err != nil {
		return fmt.Errorf("enqueue user_verified: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit verification: %w", err)
	}
	return nil
}

func (s *pgxStore) CreateSession(ctx context.Context, params SessionCreate) (*Session, error) {
	var session Session
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (
			user_id, device_info, ip_address, user_agent, refresh_token_hash,
			organization_id, organization_name, organization_slug, role, expires_at
		)
		VALUES ($1, $2, NULLIF($3, '')::inet, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, user_id, COALESCE(organization_id::text, ''), organization_name,
		          organization_slug, role, refresh_token_hash, expires_at
	`,
		params.UserID, params.DeviceInfo, params.IPAddress, params.UserAgent,
		params.RefreshTokenHash, nullUUID(params.OrganizationID), params.OrganizationName,
		params.OrganizationSlug, params.Role, params.ExpiresAt,
	).Scan(&session.ID, &session.UserID, &session.OrganizationID, &session.OrganizationName,
		&session.OrganizationSlug, &session.Role, &session.RefreshTokenHash, &session.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &session, nil
}

func (s *pgxStore) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+` WHERE id = $1`, sessionID))
}

func (s *pgxStore) LookupRefreshToken(ctx context.Context, tokenHash string) (*Session, bool, error) {
	session, err := s.scanSession(s.pool.QueryRow(ctx, sessionSelect+`
		WHERE (refresh_token_hash = $1 OR previous_refresh_token_hash = $1)
		  AND revoked_at IS NULL AND expires_at > now()
	`, tokenHash))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, ErrInvalidToken
		}
		return nil, false, err
	}
	reused := session.RefreshTokenHash != tokenHash
	return session, reused, nil
}

const sessionSelect = `
	SELECT id, user_id, COALESCE(organization_id::text, ''), organization_name,
	       organization_slug, role, refresh_token_hash,
	       COALESCE(previous_refresh_token_hash, ''), expires_at, revoked_at
	FROM sessions`

func (s *pgxStore) scanSession(row rowScanner) (*Session, error) {
	var session Session
	err := row.Scan(&session.ID, &session.UserID, &session.OrganizationID, &session.OrganizationName,
		&session.OrganizationSlug, &session.Role, &session.RefreshTokenHash,
		&session.PreviousRefreshTokenHash, &session.ExpiresAt, &session.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &session, nil
}

func (s *pgxStore) RotateRefreshToken(ctx context.Context, sessionID, presentedHash, newHash string, expiresAt time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions
		SET previous_refresh_token_hash = refresh_token_hash,
		    refresh_token_hash = $3,
		    expires_at = $4
		WHERE id = $1 AND refresh_token_hash = $2 AND revoked_at IS NULL AND expires_at > now()
	`, sessionID, presentedHash, newHash, expiresAt)
	if err != nil {
		return fmt.Errorf("rotate refresh token: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// The presented token no longer matches the current hash: either it was
	// already rotated (possible theft) or the session is gone.
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)`, sessionID).Scan(&exists); err != nil {
		return fmt.Errorf("check session after failed rotation: %w", err)
	}
	if exists {
		if err := s.RevokeAllUserSessionsBySession(ctx, sessionID); err != nil {
			return err
		}
		return ErrRefreshReuse
	}
	return ErrInvalidToken
}

func (s *pgxStore) RevokeAllUserSessionsBySession(ctx context.Context, sessionID string) error {
	var userID string
	if err := s.pool.QueryRow(ctx, `SELECT user_id FROM sessions WHERE id = $1`, sessionID).Scan(&userID); err != nil {
		return fmt.Errorf("load session owner: %w", err)
	}
	return s.RevokeAllUserSessions(ctx, userID)
}

func (s *pgxStore) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, sessionID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func (s *pgxStore) RevokeSessionByToken(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE (refresh_token_hash = $1 OR previous_refresh_token_hash = $1) AND revoked_at IS NULL
	`, tokenHash)
	if err != nil {
		return fmt.Errorf("revoke session by token: %w", err)
	}
	return nil
}

func (s *pgxStore) RevokeAllUserSessions(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

func (s *pgxStore) WriteAuthAudit(ctx context.Context, userID *string, action, ipAddress, userAgent string, metadata map[string]any) error {
	var id any
	if userID != nil && *userID != "" {
		id = *userID
	}
	// auth_audit.metadata is NOT NULL DEFAULT '{}'; never pass nil.
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	if string(payload) == "null" {
		payload = []byte("{}")
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO auth_audit (user_id, action, ip_address, user_agent, metadata)
		VALUES ($1, $2, NULLIF($3, '')::inet, $4, $5)
	`, id, action, ipAddress, userAgent, payload)
	if err != nil {
		return fmt.Errorf("write auth audit: %w", err)
	}
	return nil
}

func (s *pgxStore) EnqueueMailRetry(ctx context.Context, action string, encryptedMessage []byte, attempts int, nextAttemptAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mail_delivery_outbox (action, encrypted_message, attempts, next_attempt_at)
		VALUES ($1, $2, $3, $4)
	`, action, encryptedMessage, attempts, nextAttemptAt)
	if err != nil {
		return fmt.Errorf("insert transactional email retry: %w", err)
	}
	return nil
}

func (s *pgxStore) ClaimMailRetries(ctx context.Context, limit int, leaseDuration time.Duration) ([]mailRetryJob, error) {
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM mail_delivery_outbox
			WHERE attempts < 5
			  AND next_attempt_at <= now()
			  AND (lease_expires_at IS NULL OR lease_expires_at <= now())
			ORDER BY next_attempt_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE mail_delivery_outbox AS outbox
		SET lease_token = gen_random_uuid(),
		    lease_expires_at = now() + ($2 * interval '1 second')
		FROM candidates
		WHERE outbox.id = candidates.id
		RETURNING outbox.id::text, outbox.lease_token::text, outbox.action,
		          outbox.encrypted_message, outbox.attempts
	`, limit, int64(leaseDuration.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("claim transactional email retries: %w", err)
	}
	defer rows.Close()

	jobs := make([]mailRetryJob, 0, limit)
	for rows.Next() {
		var job mailRetryJob
		if err := rows.Scan(&job.id, &job.leaseToken, &job.action, &job.encryptedMessage, &job.attempts); err != nil {
			return nil, fmt.Errorf("scan transactional email retry: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transactional email retries: %w", err)
	}
	return jobs, nil
}

func (s *pgxStore) CompleteMailRetry(ctx context.Context, id, leaseToken string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM mail_delivery_outbox
		WHERE id = $1 AND lease_token = $2
	`, id, leaseToken)
	if err != nil {
		return fmt.Errorf("delete transactional email retry: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("transactional email retry %s lease was lost", id)
	}
	return nil
}

func (s *pgxStore) RescheduleMailRetry(ctx context.Context, id, leaseToken string, attempts int, nextAttemptAt time.Time, lastError string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mail_delivery_outbox
		SET attempts = $3,
		    encrypted_message = CASE WHEN $3 >= 5 THEN ''::bytea ELSE encrypted_message END,
		    next_attempt_at = $4, last_error = $5,
		    lease_token = NULL, lease_expires_at = NULL
		WHERE id = $1 AND lease_token = $2
	`, id, leaseToken, attempts, nextAttemptAt, lastError)
	if err != nil {
		return fmt.Errorf("update transactional email retry: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("transactional email retry %s lease was lost", id)
	}
	return nil
}

func nullUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// VerifyPassword checks a plaintext password against an argon2id hash.
func VerifyPassword(encoded, password string) (bool, error) {
	return auth.VerifyPassword(encoded, password)
}

// HashPassword produces an argon2id password hash.
func HashPassword(password string) (string, error) {
	return auth.HashPassword(password)
}
