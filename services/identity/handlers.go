package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/kit"
)

const (
	verificationTokenTTL = 24 * time.Hour
	resetTokenTTL        = time.Hour
	maxPasswordLength    = 200
	minPasswordLength    = 10
	refreshCookieName    = "sendwise_refresh"
)

var ErrMailRetryQueued = errors.New("email delivery failed; retry queued")

// Handler serves the public Identity HTTP contract consumed through the gateway.
type Handler struct {
	store     Store
	tokens    *kit.Manager
	workspace WorkspaceClient
	mailer    Mailer
	cfg       Config
	logger    *slog.Logger

	authLimiter        *kit.RateLimiter
	dummyHash          string
	resendEmailLimiter *kit.RateLimiter
	resendIPLimiter    *kit.RateLimiter
	mailRetries        chan mailRetryJob
	// retryDelay between membership lookup attempts (overridden in tests).
	retryDelay     time.Duration
	mailRetryDelay time.Duration
}

type mailRetryJob struct {
	id               string
	leaseToken       string
	action           string
	message          MailMessage
	encryptedMessage []byte
	attempts         int
}

type durableMailRetryStore interface {
	EnqueueMailRetry(context.Context, string, []byte, int, time.Time) error
	ClaimMailRetries(context.Context, int, time.Duration) ([]mailRetryJob, error)
	CompleteMailRetry(context.Context, string, string) error
	RescheduleMailRetry(context.Context, string, string, int, time.Time, string) error
}

func mailRecipientDomain(address string) string {
	_, domain, ok := strings.Cut(strings.TrimSpace(address), "@")
	if !ok || domain == "" {
		return "unknown"
	}
	return strings.ToLower(domain)
}

func (h *Handler) logMailFailure(action, recipient string, err error) {
	h.logger.Error(action,
		"provider", h.mailer.Provider(),
		"recipient_domain", mailRecipientDomain(recipient),
		"error", err,
	)
}

// NewHandler wires the Identity HTTP handler.
func NewHandler(store Store, tokens *kit.Manager, workspace WorkspaceClient, mailer Mailer, cfg Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if mailer == nil {
		mailer = NewLogMailer(logger)
	}
	if reason := mailSenderDegradation(cfg); reason != "" {
		logger.Warn("transactional mail is degraded by sender configuration", "reason", reason)
	}
	// Precomputed hash so failed logins cost the same CPU as successful ones.
	dummyHash, err := HashPassword("dummy-password-for-timing-equality")
	if err != nil {
		panic(fmt.Sprintf("identity: compute dummy password hash: %v", err))
	}
	return &Handler{
		store:              store,
		tokens:             tokens.WithSessionStore(localSessionStore{store: store}),
		workspace:          workspace,
		mailer:             mailer,
		cfg:                cfg,
		logger:             logger,
		authLimiter:        kit.NewRateLimiter(30, 10),
		dummyHash:          dummyHash,
		resendEmailLimiter: kit.NewRateLimiter(5, 2),
		resendIPLimiter:    kit.NewRateLimiter(5, 2),
		mailRetries:        make(chan mailRetryJob, 128),
		retryDelay:         250 * time.Millisecond,
		mailRetryDelay:     time.Second,
	}
}

// RunMailRetries sends queued transactional messages. The production PostgreSQL
// store provides durable, encrypted retries.
func (h *Handler) RunMailRetries(ctx context.Context) {
	if store, ok := h.store.(durableMailRetryStore); ok {
		h.runDurableMailRetries(ctx, store)
		return
	}

	const workers = 4
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-h.mailRetries:
					h.retryMail(ctx, job)
				}
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
}

func (h *Handler) runDurableMailRetries(ctx context.Context, store durableMailRetryStore) {
	const batchSize = 4
	const leaseDuration = time.Minute
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		jobs, err := store.ClaimMailRetries(ctx, batchSize, leaseDuration)
		if err != nil {
			h.logger.Error("claim transactional email retries", "error", err)
		} else {
			for _, job := range jobs {
				h.retryDurableMail(ctx, store, job)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) retryDurableMail(ctx context.Context, store durableMailRetryStore, job mailRetryJob) {
	payload, err := delivery.DecryptSecret(h.cfg.MailOutboxEncryptionKey, job.encryptedMessage)
	if err != nil {
		h.logger.Error("decrypt transactional email retry",
			"retry_id", job.id, "error", err,
		)
		if completeErr := store.CompleteMailRetry(ctx, job.id, job.leaseToken); completeErr != nil {
			h.logger.Error("discard unreadable transactional email retry",
				"retry_id", job.id, "error", completeErr,
			)
		}
		return
	}
	if err := json.Unmarshal([]byte(payload), &job.message); err != nil {
		h.logger.Error("decode transactional email retry",
			"retry_id", job.id, "error", err,
		)
		if completeErr := store.CompleteMailRetry(ctx, job.id, job.leaseToken); completeErr != nil {
			h.logger.Error("discard invalid transactional email retry",
				"retry_id", job.id, "error", completeErr,
			)
		}
		return
	}

	attempt := job.attempts + 1
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = h.mailer.Send(sendCtx, job.message)
	cancel()
	if err == nil {
		if completeErr := store.CompleteMailRetry(ctx, job.id, job.leaseToken); completeErr != nil {
			h.logger.Error("complete transactional email retry",
				"retry_id", job.id, "error", completeErr,
			)
			return
		}
		h.logger.Info("transactional email retry succeeded",
			"action", job.action,
			"recipient_domain", mailRecipientDomain(job.message.To),
			"attempt", attempt,
		)
		return
	}

	h.logger.Error("transactional email retry failed",
		"action", job.action,
		"recipient_domain", mailRecipientDomain(job.message.To),
		"attempt", attempt,
		"error", err,
	)
	if attempt >= 5 {
		if updateErr := store.RescheduleMailRetry(ctx, job.id, job.leaseToken, attempt, time.Now(), err.Error()); updateErr != nil {
			h.logger.Error("record exhausted transactional email retry",
				"retry_id", job.id, "error", updateErr,
			)
		}
		return
	}

	delay := h.mailRetryDelay * time.Duration(1<<(attempt-1))
	if err := store.RescheduleMailRetry(ctx, job.id, job.leaseToken, attempt, time.Now().Add(delay), err.Error()); err != nil {
		h.logger.Error("reschedule transactional email retry",
			"retry_id", job.id, "error", err,
		)
	}
}

func (h *Handler) retryMail(ctx context.Context, job mailRetryJob) {
	for attempt := job.attempts + 1; attempt <= 5; attempt++ {
		delay := h.mailRetryDelay * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := h.mailer.Send(sendCtx, job.message)
		cancel()
		if err == nil {
			h.logger.Info("transactional email retry succeeded",
				"action", job.action,
				"recipient_domain", mailRecipientDomain(job.message.To),
				"attempt", attempt,
			)
			return
		}
		h.logger.Error("transactional email retry failed",
			"action", job.action,
			"recipient_domain", mailRecipientDomain(job.message.To),
			"attempt", attempt,
			"error", err,
		)
	}
}

func (h *Handler) queueMail(ctx context.Context, action string, message MailMessage) error {
	if store, ok := h.store.(durableMailRetryStore); ok {
		if h.cfg.MailOutboxEncryptionKey == "" {
			return fmt.Errorf("queue transactional email: DKIM_ENCRYPTION_KEY is not configured")
		}
		payload, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("marshal transactional email: %w", err)
		}
		encrypted, err := delivery.EncryptSecret(h.cfg.MailOutboxEncryptionKey, string(payload))
		if err != nil {
			return fmt.Errorf("encrypt transactional email: %w", err)
		}
		if err := store.EnqueueMailRetry(ctx, action, encrypted, 0, time.Now()); err != nil {
			return fmt.Errorf("enqueue durable transactional email: %w", err)
		}
		return nil
	}

	select {
	case h.mailRetries <- mailRetryJob{action: action, message: message}:
		return nil
	default:
		return errors.New("transactional email queue is full")
	}
}

func (h *Handler) deliverMail(ctx context.Context, action string, message MailMessage) error {
	if err := h.mailer.Send(ctx, message); err != nil {
		h.logMailFailure(action, message.To, err)
		if store, ok := h.store.(durableMailRetryStore); ok {
			if h.cfg.MailOutboxEncryptionKey == "" {
				return fmt.Errorf("queue transactional email retry: DKIM_ENCRYPTION_KEY is not configured")
			}
			payload, marshalErr := json.Marshal(message)
			if marshalErr != nil {
				return fmt.Errorf("marshal transactional email retry: %w", marshalErr)
			}
			encrypted, encryptErr := delivery.EncryptSecret(h.cfg.MailOutboxEncryptionKey, string(payload))
			if encryptErr != nil {
				return fmt.Errorf("encrypt transactional email retry: %w", encryptErr)
			}
			delay := h.mailRetryDelay
			if delay <= 0 {
				delay = time.Second
			}
			if queueErr := store.EnqueueMailRetry(ctx, action, encrypted, 1, time.Now().Add(delay)); queueErr != nil {
				return fmt.Errorf("enqueue durable transactional email retry: %w", queueErr)
			}
			return fmt.Errorf("%w: %v", ErrMailRetryQueued, err)
		}
		select {
		case h.mailRetries <- mailRetryJob{action: action, message: message, attempts: 1}:
			return fmt.Errorf("%w: %v", ErrMailRetryQueued, err)
		default:
			return fmt.Errorf("send email and enqueue retry: %w", err)
		}
	}
	return nil
}

type localSessionStore struct {
	store Store
}

func (s localSessionStore) Active(ctx context.Context, userID, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return session.UserID == userID && session.RevokedAt == nil && session.ExpiresAt.After(time.Now()), nil
}

// Routes returns the Identity HTTP router.
func (h *Handler) Routes() http.Handler {
	r := http.NewServeMux()

	authRate := kit.RateLimit(h.authLimiter)
	internal := kit.Chain(http.HandlerFunc(h.checkSession), kit.ServiceAuth(h.cfg.ServiceToken))
	internalMail := kit.Chain(http.HandlerFunc(h.sendInvitationEmail), kit.ServiceAuth(h.cfg.ServiceToken))

	r.Handle("GET /internal/sessions/{sessionID}", internal)
	r.Handle("POST /internal/mail/invitation", internalMail)
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

func (h *Handler) checkSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	userID := r.URL.Query().Get("user_id")
	if _, err := uuid.Parse(sessionID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_session_id", "The session id must be a UUID.")
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	session, err := h.store.GetSession(r.Context(), sessionID)
	if errors.Is(err, ErrNotFound) {
		kit.JSON(w, http.StatusOK, map[string]bool{"active": false})
		return
	}
	if err != nil {
		h.logger.Error("check session", "error", err, "session_id", sessionID)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	active := session.UserID == userID && session.RevokedAt == nil && session.ExpiresAt.After(time.Now())
	kit.JSON(w, http.StatusOK, map[string]bool{"active": active})
}

func (h *Handler) sendInvitationEmail(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email            string `json:"email"`
		Role             string `json:"role"`
		OrganizationName string `json:"organization_name"`
		AcceptURL        string `json:"accept_url"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The invitation email request is invalid.")
		return
	}
	request.Email = normalizeEmail(request.Email)
	request.OrganizationName = strings.TrimSpace(request.OrganizationName)
	switch request.Role {
	case "admin", "manager", "author", "analyst", "viewer":
	default:
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The invitation email request is incomplete.")
		return
	}
	if !validEmail(request.Email) || request.OrganizationName == "" || strings.ContainsAny(request.OrganizationName, "\r\n") ||
		request.AcceptURL == "" || strings.ContainsAny(request.AcceptURL, "\r\n") {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The invitation email request is incomplete.")
		return
	}
	link := html.EscapeString(request.AcceptURL)
	organization := html.EscapeString(request.OrganizationName)
	role := html.EscapeString(request.Role)
	message := MailMessage{
		To:      request.Email,
		Subject: "You are invited to join " + request.OrganizationName,
		HTML:    "<p>You have been invited to join <strong>" + organization + "</strong> as " + role + ".</p><p><a href=\"" + link + "\">Accept invitation</a></p>",
		Text:    "You have been invited to join " + request.OrganizationName + " as " + request.Role + ".\n\nAccept the invitation: " + request.AcceptURL,
	}
	if err := h.deliverMail(r.Context(), "send invitation email", message); err != nil && !errors.Is(err, ErrMailRetryQueued) {
		h.logger.Error("enqueue invitation email retry", "recipient_domain", mailRecipientDomain(request.Email), "error", err)
		kit.Error(w, http.StatusBadGateway, "email_delivery_failed", "The invitation email could not be delivered.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		kit.Error(w, http.StatusServiceUnavailable, "not_ready", "Identity database is not reachable.")
		return
	}
	mailStatus := "ready"
	mailReason := ""
	if reason := mailSenderDegradation(h.cfg); reason != "" {
		mailStatus, mailReason = "degraded", reason
	} else if probe, ok := h.mailer.(interface{ Probe(context.Context) error }); ok {
		if err := probe.Probe(ctx); err != nil {
			h.logger.Error("SMTP readiness probe failed", "error", err)
			mailStatus, mailReason = "degraded", "relay_probe_failed"
		}
	}
	if mailStatus == "degraded" {
		kit.JSON(w, http.StatusOK, map[string]string{
			"status":      "ready",
			"mail_status": mailStatus,
			"mail_reason": mailReason,
		})
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": "ready", "mail_status": mailStatus})
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
		h.logger.Error("prepare verification email", "recipient_domain", mailRecipientDomain(user.Email), "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "verification_email_unavailable", "Your account was created, but the verification email could not be queued. Please request a new link.")
		return
	}

	kit.JSON(w, http.StatusCreated, map[string]string{
		"message":         "Account created. Your verification email is queued; if delivery fails, we'll keep retrying.",
		"email":           user.Email,
		"delivery_status": "queued",
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
	if err := h.queueMail(ctx, "send verification email", msg); err != nil {
		return fmt.Errorf("queue verification email: %w", err)
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
	if !h.resendEmailLimiter.Allow(email) || !h.resendIPLimiter.Allow(kit.ClientKey(r)) {
		w.Header().Set("Retry-After", "60")
		kit.Error(w, http.StatusTooManyRequests, "rate_limited", "Too many verification requests. Please wait before trying again.")
		return
	}
	user, err := h.store.GetUserByEmail(r.Context(), email)
	if err == nil && user.EmailVerifiedAt == nil {
		if err := h.sendVerificationEmail(r, user); err != nil {
			h.logger.Error("queue resend verification email",
				"recipient_domain", mailRecipientDomain(user.Email),
				"error", err,
			)
			kit.Error(w, http.StatusServiceUnavailable, "verification_email_unavailable", "We couldn't queue the verification email. Please try again.")
			return
		}
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		h.logger.Error("lookup user for verification resend", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	// Always the same response: do not reveal whether the address exists.
	kit.JSON(w, http.StatusOK, map[string]string{
		"message": "If that address needs verification, a new link is queued. If delivery fails, we'll keep retrying.",
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
				if sendErr := h.deliverMail(sendCtx, "send reset email", msg); sendErr != nil && !errors.Is(sendErr, ErrMailRetryQueued) {
					h.logger.Error("enqueue reset email retry", "recipient_domain", mailRecipientDomain(user.Email), "error", sendErr)
				}
			}
		}
	} else if !errors.Is(err, ErrNotFound) {
		h.logger.Error("lookup user for reset", "error", err)
	}

	h.audit(r, nil, "forgot_password", map[string]any{"email": email})
	kit.JSON(w, http.StatusOK, map[string]string{
		"message": "If that address has an account, a reset link will be sent. If it does not arrive shortly, try again.",
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
