package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

const (
	verificationTokenTTL = 24 * time.Hour
	resetTokenTTL        = time.Hour
	maxPasswordLength    = 200
	minPasswordLength    = 10
	refreshCookieName    = "sendwise_refresh"
)

// Handler serves the public Identity HTTP contract consumed through the gateway.
type Handler struct {
	store     Store
	tokens    *kit.Manager
	workspace WorkspaceClient
	mailer    Mailer
	cfg       Config
	logger    *slog.Logger

	authLimiter *kit.RateLimiter
	dummyHash   string
	// retryDelay between membership lookup attempts (overridden in tests).
	retryDelay time.Duration
}

// NewHandler wires the Identity HTTP handler.
func NewHandler(store Store, tokens *kit.Manager, workspace WorkspaceClient, mailer Mailer, cfg Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if mailer == nil {
		mailer = NewLogMailer(logger)
	}
	// Precomputed hash so failed logins cost the same CPU as successful ones.
	dummyHash, err := HashPassword("dummy-password-for-timing-equality")
	if err != nil {
		panic(fmt.Sprintf("identity: compute dummy password hash: %v", err))
	}
	return &Handler{
		store:       store,
		tokens:      tokens,
		workspace:   workspace,
		mailer:      mailer,
		cfg:         cfg,
		logger:      logger,
		authLimiter: kit.NewRateLimiter(30, 10),
		dummyHash:   dummyHash,
		retryDelay:  250 * time.Millisecond,
	}
}

// Routes returns the Identity HTTP router.
func (h *Handler) Routes() http.Handler {
	r := http.NewServeMux()

	authRate := kit.RateLimit(h.authLimiter)

	r.Handle("POST /auth/register", authRate(http.HandlerFunc(h.register)))
	r.Handle("POST /auth/login", authRate(http.HandlerFunc(h.login)))
	r.Handle("POST /auth/refresh", authRate(http.HandlerFunc(h.refresh)))
	r.Handle("POST /auth/verify", authRate(http.HandlerFunc(h.verifyEmail)))
	r.Handle("POST /auth/resend-verification", authRate(http.HandlerFunc(h.resendVerification)))
	r.Handle("POST /auth/forgot-password", authRate(http.HandlerFunc(h.forgotPassword)))
	r.Handle("POST /auth/reset-password", authRate(http.HandlerFunc(h.resetPassword)))
	r.Handle("POST /auth/logout", http.HandlerFunc(h.logout))
	r.Handle("GET /auth/me", h.tokens.Middleware(http.HandlerFunc(h.me)))
	r.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		kit.JSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "identity"})
	})
	r.HandleFunc("GET /readyz", h.ready)

	return kit.Chain(r, kit.Recover(h.logger), kit.AccessLog(h.logger), kit.RequestID)
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		kit.Error(w, http.StatusServiceUnavailable, "not_ready", "Identity database is not reachable.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- request/response payloads -------------------------------------------

type registerRequest struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	Name             string `json:"name"`
	OrganizationName string `json:"organization_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type verifyRequest struct {
	Token string `json:"token"`
}

type emailRequest struct {
	Email string `json:"email"`
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}

type userResponse struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type organizationResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

type sessionResponse struct {
	AccessToken  string               `json:"access_token"`
	TokenType    string               `json:"token_type"`
	ExpiresIn    int64                `json:"expires_in"`
	User         userResponse         `json:"user"`
	Organization organizationResponse `json:"organization"`
}

func toUserResponse(u *User) userResponse {
	return userResponse{
		ID:              u.ID,
		Email:           u.Email,
		Name:            u.Name,
		Status:          u.Status,
		EmailVerifiedAt: u.EmailVerifiedAt,
		CreatedAt:       u.CreatedAt,
	}
}

// --- helpers ---------------------------------------------------------------

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func validEmail(raw string) bool {
	if raw == "" || len(raw) > 320 {
		return false
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return false
	}
	return addr.Address == raw || normalizeEmail(addr.Address) == normalizeEmail(raw)
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func clientIP(r *http.Request) string {
	return kit.ClientKey(r)
}

func (h *Handler) audit(r *http.Request, userID *string, action string, metadata map[string]any) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.WriteAuthAudit(ctx, userID, action, clientIP(r), r.UserAgent(), metadata); err != nil {
		h.logger.Warn("auth audit write failed", "action", action, "error", err)
	}
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(h.cfg.RefreshTokenTTL),
		MaxAge:   int(h.cfg.RefreshTokenTTL.Seconds()),
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// defaultMembershipWithRetry asks Workspace for the user's default membership,
// retrying briefly to absorb the race between registration and projection.
func (h *Handler) defaultMembershipWithRetry(ctx context.Context, userID string) (*WorkspaceMembership, error) {
	if h.workspace == nil {
		return nil, ErrWorkspaceUnavailable
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * h.retryDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		membership, err := h.workspace.DefaultMembership(ctx, userID)
		if err == nil {
			return membership, nil
		}
		if errors.Is(err, ErrNoOrganization) {
			lastErr = err
			continue
		}
		if errors.Is(err, ErrWorkspaceUnavailable) {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

func (h *Handler) issueSession(r *http.Request, user *User, membership *WorkspaceMembership) (*Session, sessionResponse, string, error) {
	refreshRaw, err := randomToken()
	if err != nil {
		return nil, sessionResponse{}, "", err
	}
	session, err := h.store.CreateSession(r.Context(), SessionCreate{
		UserID:           user.ID,
		OrganizationID:   membership.OrganizationID,
		OrganizationName: membership.Name,
		OrganizationSlug: membership.Slug,
		Role:             membership.Role,
		DeviceInfo:       r.Header.Get("User-Agent"),
		IPAddress:        clientIP(r),
		UserAgent:        r.UserAgent(),
		RefreshTokenHash: HashToken(refreshRaw),
		ExpiresAt:        time.Now().Add(h.cfg.RefreshTokenTTL),
	})
	if err != nil {
		return nil, sessionResponse{}, "", fmt.Errorf("create session: %w", err)
	}

	access, err := h.tokens.IssueAccess(user.ID, user.Email, membership.OrganizationID, membership.Role, session.ID)
	if err != nil {
		return nil, sessionResponse{}, "", fmt.Errorf("issue access token: %w", err)
	}
	return session, sessionResponse{
		AccessToken: access,
		TokenType:   "bearer",
		ExpiresIn:   int64(h.cfg.AccessTokenTTL.Seconds()),
		User:        toUserResponse(user),
		Organization: organizationResponse{
			ID:   membership.OrganizationID,
			Name: membership.Name,
			Slug: membership.Slug,
			Role: membership.Role,
		},
	}, refreshRaw, nil
}

// --- endpoints --------------------------------------------------------------

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := kit.Decode(w, r, &req); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	req.Email = normalizeEmail(req.Email)
	req.Name = strings.TrimSpace(req.Name)
	req.OrganizationName = strings.TrimSpace(req.OrganizationName)

	switch {
	case !validEmail(req.Email):
		kit.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	case len(req.Password) < minPasswordLength || len(req.Password) > maxPasswordLength:
		kit.Error(w, http.StatusBadRequest, "weak_password", fmt.Sprintf("Password must be between %d and %d characters.", minPasswordLength, maxPasswordLength))
		return
	case req.Name == "" || len(req.Name) > 200:
		kit.Error(w, http.StatusBadRequest, "invalid_name", "Name is required.")
		return
	case req.OrganizationName == "" || len(req.OrganizationName) > 200:
		kit.Error(w, http.StatusBadRequest, "invalid_organization_name", "Organization name is required.")
		return
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		h.logger.Error("hash password", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	correlationID := r.Header.Get("X-Request-ID")
	user, err := h.store.CreateUserWithRegistration(r.Context(), req.Email, passwordHash, req.Name, req.OrganizationName, correlationID)
	if errors.Is(err, ErrEmailInUse) {
		kit.Error(w, http.StatusConflict, "email_in_use", "An account with this email address already exists.")
		return
	}
	if err != nil {
		h.logger.Error("create user", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	h.audit(r, &user.ID, "register", map[string]any{"organization_name": req.OrganizationName})
	if err := h.sendVerificationEmail(r, user); err != nil {
		h.logger.Error("send verification email", "error", err, "provider", h.mailer.Provider())
		kit.Error(w, http.StatusServiceUnavailable, "verification_email_unavailable", "Your account was created, but the verification email could not be sent. Request a new link before signing in.")
		return
	}

	kit.JSON(w, http.StatusCreated, map[string]string{
		"message": "Account created. Check your inbox to verify your email address.",
		"email":   user.Email,
	})
}

func (h *Handler) sendVerificationEmail(r *http.Request, user *User) error {
	token, err := randomToken()
	if err != nil {
		return fmt.Errorf("generate verification token: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	err = h.store.CreateVerificationToken(ctx, user.ID, user.Email, HashToken(token), "account_verification", time.Now().Add(verificationTokenTTL))
	if err != nil {
		return fmt.Errorf("store verification token: %w", err)
	}
	link := h.cfg.AppBaseURL + "/auth/verify?token=" + token
	msg := MailMessage{
		To:      user.Email,
		Subject: "Verify your email address",
		HTML:    verificationEmailHTML(user.Name, link, h.cfg.AppBaseURL),
		Text: "Hi " + user.Name + ",\n\nVerify your email address by opening this link:\n\n" + link +
			"\n\nIf you did not create an account, you can ignore this message.\n",
	}
	if err := h.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send verification email: %w", err)
	}
	return nil
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := kit.Decode(w, r, &req); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	email := normalizeEmail(req.Email)

	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		h.logger.Error("lookup user", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	if errors.Is(err, ErrNotFound) {
		_, _ = VerifyPassword(h.dummyHash, req.Password)
		h.audit(r, nil, "login_failed", map[string]any{"email": email, "reason": "unknown_user"})
		kit.Error(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}

	ok, err := VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		h.logger.Error("verify password", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if !ok {
		h.audit(r, &user.ID, "login_failed", map[string]any{"email": email, "reason": "bad_password"})
		kit.Error(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	if user.EmailVerifiedAt == nil {
		h.audit(r, &user.ID, "login_blocked_unverified", nil)
		kit.Error(w, http.StatusForbidden, "email_not_verified", "Verify your email address before signing in.")
		return
	}

	membership, err := h.defaultMembershipWithRetry(r.Context(), user.ID)
	switch {
	case errors.Is(err, ErrNoOrganization):
		h.audit(r, &user.ID, "login_blocked_no_organization", nil)
		kit.Error(w, http.StatusConflict, "workspace_pending", "Your workspace is still being provisioned. Please try again in a few seconds.")
		return
	case errors.Is(err, ErrWorkspaceUnavailable):
		h.audit(r, &user.ID, "login_blocked_workspace_unavailable", nil)
		w.Header().Set("Retry-After", "5")
		kit.Error(w, http.StatusServiceUnavailable, "workspace_unavailable", "Your workspace could not be loaded. Please try again.")
		return
	case err != nil && !errors.Is(err, ErrWorkspaceUnavailable):
		h.logger.Error("resolve membership", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	session, resp, refreshToken, err := h.issueSession(r, user, membership)
	if err != nil {
		h.logger.Error("issue session", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if err := h.store.RecordLogin(r.Context(), user.ID); err != nil {
		h.logger.Warn("record login", "error", err)
	}
	h.audit(r, &user.ID, "login", map[string]any{"session_id": session.ID, "organization_id": membership.OrganizationID})

	h.setRefreshCookie(w, refreshToken)
	kit.JSON(w, http.StatusOK, resp)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	refreshToken := ""
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		refreshToken = cookie.Value
	}
	if refreshToken == "" && r.Body != nil && r.ContentLength != 0 {
		if err := kit.Decode(w, r, &req); err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
			return
		}
		refreshToken = req.RefreshToken
	}
	if strings.TrimSpace(refreshToken) == "" {
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.")
		return
	}
	presentedHash := HashToken(refreshToken)

	session, reused, err := h.store.LookupRefreshToken(r.Context(), presentedHash)
	if errors.Is(err, ErrInvalidToken) {
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.")
		return
	}
	if err != nil {
		h.logger.Error("lookup refresh token", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if reused {
		// A superseded token was replayed: assume theft and kill the session family.
		h.logger.Warn("refresh token reuse detected", "session_id", session.ID, "user_id", session.UserID)
		if revokeErr := h.store.RevokeAllUserSessions(r.Context(), session.UserID); revokeErr != nil {
			h.logger.Error("revoke sessions after reuse", "error", revokeErr)
		}
		h.audit(r, &session.UserID, "refresh_reuse_detected", map[string]any{"session_id": session.ID})
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusUnauthorized, "refresh_token_reuse", "Your session was terminated for security reasons. Please sign in again.")
		return
	}

	user, err := h.store.GetUserByID(r.Context(), session.UserID)
	if errors.Is(err, ErrNotFound) {
		if revokeErr := h.store.RevokeSession(r.Context(), session.ID); revokeErr != nil {
			h.logger.Error("revoke session for missing user", "session_id", session.ID, "error", revokeErr)
		}
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.")
		return
	}
	if err != nil {
		h.logger.Error("load session user", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if user.EmailVerifiedAt == nil {
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusForbidden, "email_not_verified", "Verify your email address before signing in.")
		return
	}

	refreshRaw, err := randomToken()
	if err != nil {
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	newHash := HashToken(refreshRaw)
	access, err := h.tokens.IssueAccess(user.ID, user.Email, session.OrganizationID, session.Role, session.ID)
	if err != nil {
		h.logger.Error("issue access token", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	err = h.store.RotateRefreshToken(r.Context(), session.ID, presentedHash, newHash, time.Now().Add(h.cfg.RefreshTokenTTL))
	if errors.Is(err, ErrRefreshReuse) {
		h.logger.Warn("refresh token reuse detected", "session_id", session.ID, "user_id", session.UserID)
		h.audit(r, &user.ID, "refresh_reuse_detected", map[string]any{"session_id": session.ID})
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusUnauthorized, "refresh_token_reuse", "Your session was terminated for security reasons. Please sign in again.")
		return
	}
	if errors.Is(err, ErrInvalidToken) {
		h.clearRefreshCookie(w)
		kit.Error(w, http.StatusUnauthorized, "invalid_refresh_token", "The refresh token is invalid or expired.")
		return
	}
	if err != nil {
		h.logger.Error("rotate refresh token", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	h.audit(r, &user.ID, "refresh", map[string]any{"session_id": session.ID})

	h.setRefreshCookie(w, refreshRaw)
	kit.JSON(w, http.StatusOK, sessionResponse{
		AccessToken: access,
		TokenType:   "bearer",
		ExpiresIn:   int64(h.cfg.AccessTokenTTL.Seconds()),
		User:        toUserResponse(user),
		Organization: organizationResponse{
			ID:   session.OrganizationID,
			Name: session.OrganizationName,
			Slug: session.OrganizationSlug,
			Role: session.Role,
		},
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := kit.Decode(w, r, &req); err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
			return
		}
	}
	refreshToken := req.RefreshToken
	if cookie, err := r.Cookie(refreshCookieName); err == nil {
		refreshToken = cookie.Value
	}

	var sessionID string
	var userID string
	if claims, ok := kit.FromContext(r.Context()); ok {
		sessionID = claims.SessionID
		userID = claims.Subject
	} else if header := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(header, "Bearer ") {
		if claims, err := h.tokens.Parse(strings.TrimPrefix(header, "Bearer ")); err == nil {
			sessionID = claims.SessionID
			userID = claims.Subject
		}
	}

	revoked := false
	if refreshToken != "" {
		if err := h.store.RevokeSessionByToken(r.Context(), HashToken(refreshToken)); err == nil {
			revoked = true
		} else if !errors.Is(err, ErrInvalidToken) {
			h.logger.Error("revoke session by refresh token", "error", err)
			kit.Error(w, http.StatusInternalServerError, "internal_error", "The session could not be revoked.")
			return
		}
	}
	if !revoked && sessionID != "" {
		if err := h.store.RevokeSession(r.Context(), sessionID); err == nil {
			revoked = true
		} else {
			h.logger.Error("revoke session by access token", "error", err)
			kit.Error(w, http.StatusInternalServerError, "internal_error", "The session could not be revoked.")
			return
		}
	}
	if userID != "" {
		h.audit(r, &userID, "logout", map[string]any{"session_id": sessionID})
	}
	h.clearRefreshCookie(w)
	kit.JSON(w, http.StatusOK, map[string]bool{"revoked": revoked})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	claims, ok := kit.FromContext(r.Context())
	if !ok {
		kit.Error(w, http.StatusUnauthorized, "unauthenticated", "A valid access token is required.")
		return
	}
	user, err := h.store.GetUserByID(r.Context(), claims.Subject)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusUnauthorized, "unauthenticated", "The account no longer exists.")
		return
	}
	if err != nil {
		h.logger.Error("load user for /auth/me", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	org := organizationResponse{ID: claims.OrganizationID, Role: claims.Role}

	// Prefer the session snapshot carried by the sid claim: it is local and
	// works even when the Workspace service is briefly unavailable.
	if claims.SessionID != "" {
		if session, err := h.store.GetSession(r.Context(), claims.SessionID); err == nil && session.UserID == user.ID {
			org.Name = session.OrganizationName
			org.Slug = session.OrganizationSlug
			if org.ID == "" {
				org.ID = session.OrganizationID
			}
			if org.Role == "" {
				org.Role = session.Role
			}
			kit.JSON(w, http.StatusOK, map[string]any{"user": toUserResponse(user), "organization": org})
			return
		}
	}

	if org.ID != "" && h.workspace != nil {
		if workspaceOrg, err := h.workspace.Organization(r.Context(), org.ID); err == nil {
			org.Name = workspaceOrg.Name
			org.Slug = workspaceOrg.Slug
			kit.JSON(w, http.StatusOK, map[string]any{"user": toUserResponse(user), "organization": org})
			return
		}
	}

	if org.ID == "" {
		kit.Error(w, http.StatusForbidden, "missing_organization", "The access token is not associated with an organization.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"user": toUserResponse(user), "organization": org})
}

func (h *Handler) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := kit.Decode(w, r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "A verification token is required.")
		return
	}
	userID, email, err := h.store.ConsumeVerificationToken(r.Context(), HashToken(req.Token), "account_verification")
	if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusBadRequest, "invalid_token", "This verification link is invalid or has expired.")
		return
	}
	if err != nil {
		h.logger.Error("consume verification token", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if err := h.store.MarkEmailVerified(r.Context(), userID, email, r.Header.Get("X-Request-ID")); err != nil {
		h.logger.Error("mark email verified", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	h.audit(r, &userID, "account_verification", nil)
	kit.JSON(w, http.StatusOK, map[string]string{"message": "Email verified. You can now sign in."})
}

func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	var req emailRequest
	if err := kit.Decode(w, r, &req); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	email := normalizeEmail(req.Email)
	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err == nil && user.EmailVerifiedAt == nil {
		if err := h.sendVerificationEmail(r, user); err != nil {
			h.logger.Error("resend verification email", "error", err, "provider", h.mailer.Provider())
		}
	}
	// Always the same response: do not reveal whether the address exists.
	kit.JSON(w, http.StatusOK, map[string]string{
		"message": "If that address needs verification, a new link has been sent.",
	})
}

func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req emailRequest
	if err := kit.Decode(w, r, &req); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	email := normalizeEmail(req.Email)

	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err == nil {
		token, tokenErr := randomToken()
		if tokenErr == nil {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			defer cancel()
			if storeErr := h.store.CreateVerificationToken(ctx, user.ID, user.Email, HashToken(token), "password_reset", time.Now().Add(resetTokenTTL)); storeErr != nil {
				h.logger.Error("store reset token", "error", storeErr)
			} else {
				link := h.cfg.AppBaseURL + "/auth/reset-password?token=" + token
				msg := MailMessage{
					To:      user.Email,
					Subject: "Reset your password",
					HTML:    resetEmailHTML(user.Name, link, h.cfg.AppBaseURL),
					Text: "Hi " + user.Name + ",\n\nReset your password by opening this link:\n\n" + link +
						"\n\nIf you did not request this, you can ignore this message.\n",
				}
				sendCtx, sendCancel := context.WithTimeout(ctx, 10*time.Second)
				defer sendCancel()
				if sendErr := h.mailer.Send(sendCtx, msg); sendErr != nil {
					h.logger.Error("send reset email", "error", sendErr, "provider", h.mailer.Provider())
				}
			}
		}
	} else if !errors.Is(err, ErrNotFound) {
		h.logger.Error("lookup user for reset", "error", err)
	}

	h.audit(r, nil, "forgot_password", map[string]any{"email": email})
	kit.JSON(w, http.StatusOK, map[string]string{
		"message": "If that address has an account, a reset link has been sent.",
	})
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := kit.Decode(w, r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "A reset token and password are required.")
		return
	}
	if len(req.Password) < minPasswordLength || len(req.Password) > maxPasswordLength {
		kit.Error(w, http.StatusBadRequest, "weak_password", fmt.Sprintf("Password must be between %d and %d characters.", minPasswordLength, maxPasswordLength))
		return
	}

	userID, email, err := h.store.ConsumeVerificationToken(r.Context(), HashToken(req.Token), "password_reset")
	if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusBadRequest, "invalid_token", "This reset link is invalid or has expired.")
		return
	}
	if err != nil {
		h.logger.Error("consume reset token", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	passwordHash, err := HashPassword(req.Password)
	if err != nil {
		h.logger.Error("hash password", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if err := h.store.SetPassword(r.Context(), userID, passwordHash); err != nil {
		h.logger.Error("set password", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	if err := h.store.RevokeAllUserSessions(r.Context(), userID); err != nil {
		h.logger.Warn("revoke sessions after reset", "error", err)
	}
	h.audit(r, &userID, "password_reset", map[string]any{"email": email})

	kit.JSON(w, http.StatusOK, map[string]string{"message": "Password updated. You can now sign in."})
}
