package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/httpx"
)

type userSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type organizationSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

type authResponse struct {
	AccessToken  string              `json:"access_token"`
	TokenType    string              `json:"token_type"`
	ExpiresIn    int                 `json:"expires_in"`
	User         userSummary         `json:"user"`
	Organization organizationSummary `json:"organization"`
}

type registerInput struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	Name             string `json:"name"`
	OrganizationName string `json:"organization_name"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var input registerInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}

	email, err := normalizeEmail(input.Email)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}
	if err := validatePassword(input.Password); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}

	input.Name = strings.TrimSpace(input.Name)
	input.OrganizationName = strings.TrimSpace(input.OrganizationName)
	if input.Name == "" {
		input.Name = strings.Split(email, "@")[0]
	}
	if input.OrganizationName == "" {
		input.OrganizationName = input.Name + "'s workspace"
	}

	passwordHash, err := auth.HashPassword(input.Password)
	if err != nil {
		s.logger.Error("hash password", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the account.")
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the account.")
		return
	}
	defer tx.Rollback(r.Context())

	var userID string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO users (email, password_hash, name, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id
	`, email, passwordHash, input.Name).Scan(&userID)
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "email_in_use", "An account with this email already exists.")
		return
	}
	if err != nil {
		s.logger.Error("create user", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the account.")
		return
	}

	var orgID, orgName, orgSlug string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO organizations (name, slug)
		VALUES ($1, $2)
		RETURNING id, name, slug
	`, input.OrganizationName, slugify(input.OrganizationName)).Scan(&orgID, &orgName, &orgSlug)
	if err != nil {
		s.logger.Error("create organization", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the workspace.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, orgID, userID); err != nil {
		s.logger.Error("create membership", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the workspace.")
		return
	}

	// Registration events (identity.user_registered) are produced exclusively by
	// the Identity service. The legacy registration path remains only as a
	// documented rollback option and is not routed through the API gateway.
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit registration", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the account.")
		return
	}

	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the account.")
		return
	}
	tokenStr := hex.EncodeToString(token)

	if _, err := s.pool.Exec(r.Context(), `
		INSERT INTO verification_tokens (user_id, token, email, expires_at)
		VALUES ($1, $2, $3, now() + interval '24 hours')
	`, userID, tokenStr, email); err != nil {
		s.logger.Error("store verification token", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the account.")
		return
	}

	verifyURL := fmt.Sprintf("%s/auth/verify?token=%s", s.cfg.AppBaseURL, tokenStr)
	if err := sendVerificationEmail(s.cfg, email, input.Name, verifyURL); err != nil {
		s.logger.Error("send verification email", "error", err)
	}

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"message": "Account created. Please check your email to verify your address.",
		"email":   email,
	})
}

// verifyAccount verifies a user's email address using a verification token.
func (s *Server) verifyAccount(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Verification token is required.")
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin verify transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not verify account.")
		return
	}
	defer tx.Rollback(r.Context())

	var userID, email string
	var expiresAt time.Time
	err = tx.QueryRow(r.Context(), `
		SELECT user_id, email, expires_at
		FROM verification_tokens
		WHERE token = $1 AND used_at IS NULL
	`, token).Scan(&userID, &email, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "invalid_token", "Invalid or expired verification token.")
		return
	}
	if err != nil {
		s.logger.Error("load verification token", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not verify account.")
		return
	}

	if time.Now().After(expiresAt) {
		httpx.Error(w, http.StatusGone, "token_expired", "Verification token has expired.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE users
		SET email_verified_at = now(), status = 'active'
		WHERE id = $1 AND email_verified_at IS NULL
	`, userID); err != nil {
		s.logger.Error("verify user", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not verify account.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE verification_tokens
		SET used_at = now()
		WHERE token = $1
	`, token); err != nil {
		s.logger.Error("mark token used", "error", err)
	}

	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit verification", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not verify account.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"message": "Email verified successfully. You can now sign in.",
	})
}

// sendVerificationEmail sends an account confirmation email via Resend.
func sendVerificationEmail(cfg config.Config, to, name, verifyURL string) error {
	if cfg.ResendAPIKey == "" {
		return fmt.Errorf("resend API key not configured")
	}

	type resendRequest struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Html    string   `json:"html"`
	}

	body := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<body style="font-family: sans-serif; line-height: 1.6; color: #333;">
			<h2>Welcome to %s, %s!</h2>
			<p>Please verify your email address to activate your account.</p>
			<p><a href="%s" style="background-color: #4f46e5; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">Verify Email Address</a></p>
			<p>Or copy and paste this link: %s</p>
			<p>This link expires in 24 hours.</p>
			<p>If you didn't create an account, you can safely ignore this email.</p>
		</body>
		</html>
	`, cfg.ResendFromName, name, verifyURL, verifyURL)

	reqBody := resendRequest{
		From:    fmt.Sprintf("%s <%s>", cfg.ResendFromName, cfg.ResendFromEmail),
		To:      []string{to},
		Subject: "Verify your email address",
		Html:    body,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal resend request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("create resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send resend request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input loginInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}

	email, err := normalizeEmail(input.Email)
	if err != nil || input.Password == "" {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}

	var userID, name, passwordHash, status string
	err = s.pool.QueryRow(r.Context(), `
		SELECT id, name, password_hash, status
		FROM users
		WHERE lower(email) = $1
	`, email).Scan(&userID, &name, &passwordHash, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if err != nil {
		s.logger.Error("load user for login", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not sign in.")
		return
	}

	valid, err := auth.VerifyPassword(passwordHash, input.Password)
	if err != nil || !valid {
		httpx.Error(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if status != "active" {
		httpx.Error(w, http.StatusForbidden, "account_inactive", "This account is not active.")
		return
	}

	var orgID, orgName, orgSlug, role string
	err = s.pool.QueryRow(r.Context(), `
		SELECT o.id, o.name, o.slug, m.role
		FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY m.created_at
		LIMIT 1
	`, userID).Scan(&orgID, &orgName, &orgSlug, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusForbidden, "no_organization", "This account is not a member of a workspace.")
		return
	}
	if err != nil {
		s.logger.Error("load membership", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not sign in.")
		return
	}

	_, _ = s.pool.Exec(r.Context(), `UPDATE users SET last_login_at = now() WHERE id = $1`, userID)

	token, err := s.tokens.IssueAccess(userID, orgID, role)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not issue an access token.")
		return
	}

	httpx.JSON(w, http.StatusOK, authResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.tokens.TTL().Seconds()),
		User:        userSummary{ID: userID, Name: name, Email: email},
		Organization: organizationSummary{
			ID: orgID, Name: orgName, Slug: orgSlug, Role: role,
		},
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	claims := s.claims(r)

	var user userSummary
	if err := s.pool.QueryRow(r.Context(), `
		SELECT id, name, email FROM users WHERE id = $1
	`, claims.Subject).Scan(&user.ID, &user.Name, &user.Email); err != nil {
		httpx.Error(w, http.StatusNotFound, "user_not_found", "The user could not be found.")
		return
	}

	var org organizationSummary
	if err := s.pool.QueryRow(r.Context(), `
		SELECT o.id, o.name, o.slug, m.role
		FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1 AND m.organization_id = $2
	`, claims.Subject, claims.OrganizationID).Scan(&org.ID, &org.Name, &org.Slug, &org.Role); err != nil {
		httpx.Error(w, http.StatusForbidden, "no_membership", "You are not a member of this workspace.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"user":         user,
		"organization": org,
	})
}

func normalizeEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	address, err := mail.ParseAddress(raw)
	if err != nil || !strings.EqualFold(address.Address, raw) {
		return "", errors.New("invalid email address")
	}
	return strings.ToLower(address.Address), nil
}

func validatePassword(password string) error {
	if len(password) < 10 {
		return errors.New("Password must be at least 10 characters.")
	}
	if len(password) > 1024 {
		return errors.New("Password is too long.")
	}
	return nil
}

var slugSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := slugSanitizer.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "workspace"
	}
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug + "-" + uuid.NewString()[:6]
}

type forgotPasswordInput struct {
	Email string `json:"email"`
}

type resetPasswordInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// forgotPassword generates a password reset token and emails it to the user.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var input forgotPasswordInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}

	var userID, name string
	err = s.pool.QueryRow(r.Context(), `
		SELECT id, name FROM users WHERE lower(email) = $1
	`, email).Scan(&userID, &name)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"message": "If an account with this email exists, a password reset link has been sent.",
		})
		return
	}

	var status string
	s.pool.QueryRow(r.Context(), `SELECT status FROM users WHERE id = $1`, userID).Scan(&status)
	if status != "active" {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"message": "If an account with this email exists, a password reset link has been sent.",
		})
		return
	}

	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		s.logger.Error("generate reset token", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process request.")
		return
	}
	tokenStr := hex.EncodeToString(token)

	if _, err := s.pool.Exec(r.Context(), `
		INSERT INTO verification_tokens (user_id, token, email, purpose, expires_at)
		VALUES ($1, $2, $3, 'password_reset', now() + interval '1 hour')
	`, userID, tokenStr, email); err != nil {
		s.logger.Error("store reset token", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process request.")
		return
	}

	resetURL := fmt.Sprintf("%s/auth/reset-password?token=%s", s.cfg.AppBaseURL, tokenStr)
	if err := sendPasswordResetEmail(s.cfg, email, name, resetURL); err != nil {
		s.logger.Error("send password reset email", "error", err)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"message": "If an account with this email exists, a password reset link has been sent.",
	})
}

// resetPassword sets a new password using a valid reset token.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var input resetPasswordInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if input.Token == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Reset token is required.")
		return
	}
	if err := validatePassword(input.Password); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin reset transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reset password.")
		return
	}
	defer tx.Rollback(r.Context())

	var userID string
	var expiresAt time.Time
	err = tx.QueryRow(r.Context(), `
		SELECT user_id, expires_at
		FROM verification_tokens
		WHERE token = $1 AND purpose = 'password_reset' AND used_at IS NULL
	`, input.Token).Scan(&userID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "invalid_token", "Invalid or expired reset token.")
		return
	}
	if err != nil {
		s.logger.Error("load reset token", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reset password.")
		return
	}

	if time.Now().After(expiresAt) {
		httpx.Error(w, http.StatusGone, "token_expired", "Reset token has expired.")
		return
	}

	passwordHash, err := auth.HashPassword(input.Password)
	if err != nil {
		s.logger.Error("hash password", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reset password.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE users
		SET password_hash = $1, updated_at = now()
		WHERE id = $2
	`, passwordHash, userID); err != nil {
		s.logger.Error("update password", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reset password.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE verification_tokens
		SET used_at = now()
		WHERE token = $1
	`, input.Token); err != nil {
		s.logger.Error("mark token used", "error", err)
	}

	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit reset", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reset password.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"message": "Password reset successfully. You can now sign in.",
	})
}

// sendPasswordResetEmail sends a password reset email via Resend.
func sendPasswordResetEmail(cfg config.Config, to, name, resetURL string) error {
	if cfg.ResendAPIKey == "" {
		return fmt.Errorf("resend API key not configured")
	}

	type resendRequest struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Html    string   `json:"html"`
	}

	body := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<body style="font-family: sans-serif; line-height: 1.6; color: #333;">
			<h2>Password Reset</h2>
			<p>Hello %s,</p>
			<p>You requested a password reset for your Sendwise account.</p>
			<p><a href="%s" style="background-color: #4f46e5; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">Reset Password</a></p>
			<p>Or copy and paste this link: %s</p>
			<p>This link expires in 1 hour.</p>
			<p>If you didn't request this, you can safely ignore this email.</p>
		</body>
		</html>
	`, name, resetURL, resetURL)

	reqBody := resendRequest{
		From:    fmt.Sprintf("%s <%s>", cfg.ResendFromName, cfg.ResendFromEmail),
		To:      []string{to},
		Subject: "Reset your Sendwise password",
		Html:    body,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal resend request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.resend.com/emails", strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("create resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send resend request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
