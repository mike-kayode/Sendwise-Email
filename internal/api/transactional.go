package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/httpx"
	"github.com/khayode/email-platform/internal/transactional"
)

const transactionalAPIKeyPrefixLength = 24

type transactionalKeyContext struct {
	ID             string
	OrganizationID string
	Scopes         []string
	TestMode       bool
}

type transactionalKeyContextKey struct{}

type transactionalAPIKeyInput struct {
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	IPAllowlist []string `json:"ip_allowlist"`
	TestMode    bool     `json:"test_mode"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
}

type transactionalAPIKeyResponse struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	KeyPrefix   string     `json:"key_prefix"`
	Scopes      []string   `json:"scopes"`
	IPAllowlist []string   `json:"ip_allowlist"`
	TestMode    bool       `json:"test_mode"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	GraceUntil  *time.Time `json:"grace_until,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	PlainKey    string     `json:"plain_key,omitempty"`
}

type transactionalMessageResponse struct {
	ID          string                           `json:"id"`
	MessageID   string                           `json:"message_id"`
	Status      string                           `json:"status"`
	From        string                           `json:"from"`
	Subject     string                           `json:"subject"`
	HTML        string                           `json:"html,omitempty"`
	Text        string                           `json:"text,omitempty"`
	CreatedAt   time.Time                        `json:"created_at"`
	AcceptedAt  *time.Time                       `json:"accepted_at,omitempty"`
	DeliveredAt *time.Time                       `json:"delivered_at,omitempty"`
	Recipients  []transactionalRecipientResponse `json:"recipients,omitempty"`
}

type transactionalRecipientResponse struct {
	Email        string    `json:"email"`
	Type         string    `json:"type"`
	Status       string    `json:"status"`
	SMTPCode     *int      `json:"smtp_code,omitempty"`
	EnhancedCode string    `json:"enhanced_code,omitempty"`
	Response     string    `json:"response,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type transactionalWebhookInput struct {
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled,omitempty"`
}

type transactionalWebhookResponse struct {
	ID                  string     `json:"id"`
	URL                 string     `json:"url"`
	Events              []string   `json:"events"`
	Enabled             bool       `json:"enabled"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	DisabledAt          *time.Time `json:"disabled_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	SigningSecret       string     `json:"signing_secret,omitempty"`
}

type transactionalTemplateInput struct {
	Name    *string `json:"name"`
	Subject *string `json:"subject"`
	HTML    *string `json:"html"`
	Text    *string `json:"text"`
}

type transactionalTemplateResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	CurrentVersion int       `json:"current_version"`
	Subject        string    `json:"subject"`
	HTML           string    `json:"html"`
	Text           string    `json:"text"`
	Variables      []string  `json:"variables"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type transactionalTemplateListItem struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	CurrentVersion int       `json:"current_version"`
	Subject        string    `json:"subject"`
	Variables      []string  `json:"variables"`
	UpdatedAt      time.Time `json:"updated_at"`
}

var transactionalWebhookEvents = map[string]bool{
	"message.queued": true, "message.sending": true, "message.sent": true,
	"message.test_mode": true, "message.delivery_delayed": true,
	"message.delivered": true, "message.deferred": true, "message.bounced": true,
	"message.failed": true, "message.complained": true, "message.suppressed": true,
	"message.rejected": true,
}

func (s *Server) transactionalKeyAuth(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, status, err := s.authenticateTransactionalKey(r, scope)
			if err != nil {
				if status >= http.StatusInternalServerError {
					s.logger.Error("authenticate transactional API key", "error", err)
					httpx.Error(w, status, "internal_error", "Could not authenticate the transactional API key.")
					return
				}
				httpx.Error(w, status, "invalid_api_key", err.Error())
				return
			}
			ctx := context.WithValue(r.Context(), transactionalKeyContextKey{}, key)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *Server) authenticateTransactionalKey(r *http.Request, requiredScope string) (*transactionalKeyContext, int, error) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) < transactionalAPIKeyPrefixLength+16 || len(token) > 160 {
		return nil, http.StatusUnauthorized, errors.New("A valid transactional bearer key is required.")
	}
	prefix := token[:transactionalAPIKeyPrefixLength]
	digest := sha256.Sum256([]byte(token))
	key := &transactionalKeyContext{}
	var storedHash string
	var allowedNetworks []string
	err := s.pool.QueryRow(r.Context(), `
		SELECT id::text, organization_id::text, key_hash, scopes, ip_allowlist::text[], test_mode
		FROM transactional_api_keys
		WHERE key_prefix = $1 AND revoked_at IS NULL
		  AND (grace_until IS NULL OR grace_until > now())
		  AND (expires_at IS NULL OR expires_at > now())
	`, prefix).Scan(
		&key.ID, &key.OrganizationID, &storedHash, &key.Scopes, &allowedNetworks, &key.TestMode,
	)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && subtle.ConstantTimeCompare([]byte(storedHash), []byte(hex.EncodeToString(digest[:]))) != 1 {
		return nil, http.StatusUnauthorized, errors.New("A valid transactional bearer key is required.")
	}
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("load transactional API key: %w", err)
	}
	if len(allowedNetworks) > 0 && !transactionalIPAllowed(r.RemoteAddr, allowedNetworks) {
		return nil, http.StatusForbidden, errors.New("This IP address is not allowed to use the transactional API key.")
	}
	hasScope := false
	for _, scope := range key.Scopes {
		if scope == requiredScope {
			hasScope = true
			break
		}
	}
	if !hasScope {
		return nil, http.StatusForbidden, errors.New("The transactional API key does not have the required scope.")
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE transactional_api_keys SET last_used_at = now()
		WHERE id = $1 AND revoked_at IS NULL
		  AND (grace_until IS NULL OR grace_until > now())
		  AND (expires_at IS NULL OR expires_at > now())
	`, key.ID)
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("update transactional API key usage: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return nil, http.StatusUnauthorized, errors.New("A valid transactional bearer key is required.")
	}
	return key, 0, nil
}

func transactionalIPAllowed(remoteAddr string, allowedNetworks []string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, rawNetwork := range allowedNetworks {
		_, network, err := net.ParseCIDR(rawNetwork)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) listTransactionalAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, name, key_prefix, scopes, ip_allowlist::text[], test_mode,
		       last_used_at, expires_at, grace_until, revoked_at, created_at
		FROM transactional_api_keys
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list transactional API keys", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional API keys.")
		return
	}
	defer rows.Close()

	keys := make([]transactionalAPIKeyResponse, 0)
	for rows.Next() {
		var key transactionalAPIKeyResponse
		if err := rows.Scan(
			&key.ID, &key.Name, &key.KeyPrefix, &key.Scopes, &key.IPAllowlist, &key.TestMode,
			&key.LastUsedAt, &key.ExpiresAt, &key.GraceUntil, &key.RevokedAt, &key.CreatedAt,
		); err != nil {
			s.logger.Error("scan transactional API key", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional API keys.")
			return
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate transactional API keys", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional API keys.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": keys})
}

func (s *Server) createTransactionalAPIKey(w http.ResponseWriter, r *http.Request) {
	var input transactionalAPIKeyInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 100 || strings.ContainsAny(input.Name, "\r\n") {
		httpx.Error(w, http.StatusBadRequest, "invalid_name", "A key name between 1 and 100 characters is required.")
		return
	}
	scopes, err := normalizeTransactionalScopes(input.Scopes)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_scopes", err.Error())
		return
	}
	networks, err := normalizeTransactionalIPAllowlist(input.IPAllowlist)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_ip_allowlist", err.Error())
		return
	}
	expiresAt, err := parseTransactionalKeyExpiry(input.ExpiresAt)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_expiry", err.Error())
		return
	}

	plain, prefix, keyHash, err := newTransactionalAPIKey(input.TestMode)
	if err != nil {
		s.logger.Error("generate transactional API key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional API key.")
		return
	}
	orgID := s.organizationID(r)
	var key transactionalAPIKeyResponse
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO transactional_api_keys
			(organization_id, name, key_prefix, key_hash, scopes, ip_allowlist, test_mode, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6::cidr[], $7, $8)
		RETURNING id::text, name, key_prefix, scopes, ip_allowlist::text[], test_mode,
		          last_used_at, expires_at, grace_until, revoked_at, created_at
	`, orgID, input.Name, prefix, keyHash, scopes, networks, input.TestMode, expiresAt).Scan(
		&key.ID, &key.Name, &key.KeyPrefix, &key.Scopes, &key.IPAllowlist, &key.TestMode,
		&key.LastUsedAt, &key.ExpiresAt, &key.GraceUntil, &key.RevokedAt, &key.CreatedAt,
	)
	if err != nil {
		s.logger.Error("create transactional API key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional API key.")
		return
	}
	key.PlainKey = plain
	s.recordTransactionalAudit(r, "transactional.api_key_created", key.ID)
	httpx.JSON(w, http.StatusCreated, key)
}

func (s *Server) rotateTransactionalAPIKey(w http.ResponseWriter, r *http.Request) {
	orgID, oldID := s.organizationID(r), r.PathValue("id")
	if _, err := uuid.Parse(oldID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_api_key_id", "The API key ID is invalid.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin transactional key rotation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional API key.")
		return
	}
	defer tx.Rollback(r.Context())

	var oldKey transactionalAPIKeyResponse
	err = tx.QueryRow(r.Context(), `
		SELECT id::text, name, test_mode
		FROM transactional_api_keys
		WHERE id = $1 AND organization_id = $2 AND revoked_at IS NULL
		FOR UPDATE
	`, oldID, orgID).Scan(&oldKey.ID, &oldKey.Name, &oldKey.TestMode)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "api_key_not_found", "The API key could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load transactional API key for rotation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional API key.")
		return
	}
	plain, prefix, keyHash, err := newTransactionalAPIKey(oldKey.TestMode)
	if err != nil {
		s.logger.Error("generate rotated transactional API key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional API key.")
		return
	}
	err = tx.QueryRow(r.Context(), `
		INSERT INTO transactional_api_keys
			(organization_id, name, key_prefix, key_hash, scopes, ip_allowlist, test_mode, rotated_from, expires_at)
		SELECT organization_id, name, $3, $4, scopes, ip_allowlist, test_mode, id, expires_at
		FROM transactional_api_keys
		WHERE id = $1 AND organization_id = $2
		RETURNING id::text, name, key_prefix, scopes, ip_allowlist::text[], test_mode,
		          last_used_at, expires_at, grace_until, revoked_at, created_at
	`, oldID, orgID, prefix, keyHash).Scan(
		&oldKey.ID, &oldKey.Name, &oldKey.KeyPrefix, &oldKey.Scopes, &oldKey.IPAllowlist, &oldKey.TestMode,
		&oldKey.LastUsedAt, &oldKey.ExpiresAt, &oldKey.GraceUntil, &oldKey.RevokedAt, &oldKey.CreatedAt,
	)
	if err != nil {
		s.logger.Error("insert rotated transactional API key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional API key.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE transactional_api_keys SET grace_until = now() + interval '24 hours'
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), orgID); err != nil {
		s.logger.Error("set transactional API key grace period", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional API key.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit transactional API key rotation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional API key.")
		return
	}
	oldKey.PlainKey = plain
	s.recordTransactionalAudit(r, "transactional.api_key_rotated", oldID)
	httpx.JSON(w, http.StatusCreated, oldKey)
}

func (s *Server) revokeTransactionalAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_api_key_id", "The API key ID is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE transactional_api_keys SET revoked_at = COALESCE(revoked_at, now()), grace_until = NULL
		WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r))
	if err != nil {
		s.logger.Error("revoke transactional API key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not revoke transactional API key.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "api_key_not_found", "The API key could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.api_key_revoked", id)
	httpx.NoContent(w)
}

func (s *Server) sendTransactionalMessage(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TransactionalEnabled {
		httpx.Error(w, http.StatusServiceUnavailable, "transactional_disabled", "Transactional delivery is disabled on this server.")
		return
	}
	key, ok := r.Context().Value(transactionalKeyContextKey{}).(*transactionalKeyContext)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "A valid transactional bearer key is required.")
		return
	}
	var input transactional.MessageInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	receipt, err := transactional.NewStore(s.pool).Enqueue(r.Context(), transactional.EnqueueOptions{
		OrganizationID: key.OrganizationID,
		APIKeyID:       key.ID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Mode:           key.TestMode,
		EnforceLimits:  true,
	}, input)
	if err != nil {
		switch {
		case errors.Is(err, transactional.ErrWorkspaceDisabled):
			httpx.Error(w, http.StatusForbidden, "transactional_disabled", err.Error())
		case errors.Is(err, transactional.ErrWorkspacePaused):
			httpx.Error(w, http.StatusForbidden, "transactional_paused", err.Error())
		case errors.Is(err, transactional.ErrDailyLimit):
			httpx.Error(w, http.StatusTooManyRequests, "daily_limit_exceeded", err.Error())
		case errors.Is(err, transactional.ErrMonthlyLimit):
			httpx.Error(w, http.StatusTooManyRequests, "monthly_limit_exceeded", err.Error())
		default:
			httpx.Error(w, http.StatusBadRequest, "invalid_message", err.Error())
		}
		return
	}
	httpx.JSON(w, http.StatusAccepted, receipt)
}

func (s *Server) listTransactionalMessages(w http.ResponseWriter, r *http.Request) {
	orgID, ok := transactionalReadOrganizationID(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "A valid transactional bearer key is required.")
		return
	}
	limit, offset := pagination(r)
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, message_id, status, from_email, subject, created_at
		FROM transactional_messages
		WHERE organization_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3
	`, orgID, limit, offset)
	if err != nil {
		s.logger.Error("list transactional messages", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional messages.")
		return
	}
	defer rows.Close()
	items := make([]transactionalMessageResponse, 0)
	for rows.Next() {
		var item transactionalMessageResponse
		if err := rows.Scan(&item.ID, &item.MessageID, &item.Status, &item.From, &item.Subject, &item.CreatedAt); err != nil {
			s.logger.Error("scan transactional message", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional messages.")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate transactional messages", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional messages.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getTransactionalMessage(w http.ResponseWriter, r *http.Request) {
	orgID, ok := transactionalReadOrganizationID(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "invalid_api_key", "A valid transactional bearer key is required.")
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_message_id", "The message ID is invalid.")
		return
	}
	var item transactionalMessageResponse
	err := s.pool.QueryRow(r.Context(), `
		SELECT id::text, message_id, status, from_email, subject, html, text_content,
		       created_at, accepted_at, delivered_at
		FROM transactional_messages
		WHERE id = $1 AND organization_id = $2
	`, id, orgID).Scan(
		&item.ID, &item.MessageID, &item.Status, &item.From, &item.Subject,
		&item.HTML, &item.Text, &item.CreatedAt, &item.AcceptedAt, &item.DeliveredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "message_not_found", "The transactional message could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get transactional message", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional message.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT email, recipient_type, status, smtp_code, enhanced_code, smtp_response, updated_at
		FROM transactional_message_recipients
		WHERE message_id = $1
		ORDER BY created_at, id
	`, item.ID)
	if err != nil {
		s.logger.Error("list transactional message recipients", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional message recipients.")
		return
	}
	defer rows.Close()
	item.Recipients = make([]transactionalRecipientResponse, 0)
	for rows.Next() {
		var recipient transactionalRecipientResponse
		if err := rows.Scan(
			&recipient.Email, &recipient.Type, &recipient.Status, &recipient.SMTPCode,
			&recipient.EnhancedCode, &recipient.Response, &recipient.UpdatedAt,
		); err != nil {
			s.logger.Error("scan transactional message recipient", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional message recipients.")
			return
		}
		item.Recipients = append(item.Recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate transactional message recipients", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional message recipients.")
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func transactionalReadOrganizationID(r *http.Request) (string, bool) {
	if key, ok := r.Context().Value(transactionalKeyContextKey{}).(*transactionalKeyContext); ok {
		return key.OrganizationID, key.OrganizationID != ""
	}
	claims, ok := auth.FromContext(r.Context())
	if !ok || claims.OrganizationID == "" {
		return "", false
	}
	return claims.OrganizationID, true
}

func (s *Server) getTransactionalUsage(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	var acceptedToday, acceptedThisMonth, deliveredThisMonth, bouncedThisMonth, complainedThisMonth int64
	err := s.pool.QueryRow(r.Context(), `
		SELECT
			COALESCE(sum(accepted_count) FILTER (WHERE usage_date = current_date), 0),
			COALESCE(sum(accepted_count), 0),
			COALESCE(sum(delivered_count), 0),
			COALESCE(sum(bounced_count), 0),
			COALESCE(sum(complained_count), 0)
		FROM transactional_usage_daily
		WHERE organization_id = $1 AND usage_date >= date_trunc('month', current_date)::date
	`, orgID).Scan(
		&acceptedToday, &acceptedThisMonth, &deliveredThisMonth, &bouncedThisMonth, &complainedThisMonth,
	)
	if err != nil {
		s.logger.Error("load transactional usage", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional usage.")
		return
	}
	var dailyLimit, monthlyLimit int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT daily_limit, monthly_limit FROM transactional_settings WHERE organization_id = $1
	`, orgID).Scan(&dailyLimit, &monthlyLimit); err != nil {
		s.logger.Error("load transactional usage limits", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional usage limits.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"accepted_today": acceptedToday, "accepted_this_month": acceptedThisMonth,
		"delivered_this_month": deliveredThisMonth, "bounced_this_month": bouncedThisMonth,
		"complained_this_month": complainedThisMonth,
		"daily_limit":           dailyLimit, "monthly_limit": monthlyLimit,
	})
}

func (s *Server) listTransactionalTemplates(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	rows, err := s.pool.Query(r.Context(), `
		SELECT t.id::text, t.name, t.current_version, v.subject, v.variables, t.updated_at
		FROM transactional_templates t
		JOIN transactional_template_versions v
		  ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.organization_id = $1
		ORDER BY t.updated_at DESC, t.id
		LIMIT $2 OFFSET $3
	`, s.organizationID(r), limit, offset)
	if err != nil {
		s.logger.Error("list transactional templates", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional templates.")
		return
	}
	defer rows.Close()
	items := make([]transactionalTemplateListItem, 0)
	for rows.Next() {
		var item transactionalTemplateListItem
		if err := rows.Scan(
			&item.ID, &item.Name, &item.CurrentVersion, &item.Subject, &item.Variables, &item.UpdatedAt,
		); err != nil {
			s.logger.Error("scan transactional template", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional templates.")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate transactional templates", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional templates.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) createTransactionalTemplate(w http.ResponseWriter, r *http.Request) {
	var input transactionalTemplateInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if input.Name == nil || input.Subject == nil || (input.HTML == nil && input.Text == nil) {
		httpx.Error(w, http.StatusBadRequest, "invalid_template", "A name, subject, and HTML or text body are required.")
		return
	}
	name := strings.TrimSpace(*input.Name)
	if name == "" || len(name) > 100 || strings.ContainsAny(name, "\r\n") {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_name", "The template name must be between 1 and 100 characters.")
		return
	}
	html, text := "", ""
	if input.HTML != nil {
		html = *input.HTML
	}
	if input.Text != nil {
		text = *input.Text
	}
	source := transactional.Template{Subject: *input.Subject, HTML: html, Text: text}
	variables, err := validateTransactionalTemplate(source)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_template", err.Error())
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin transactional template creation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional template.")
		return
	}
	defer tx.Rollback(r.Context())
	orgID := s.organizationID(r)
	var id string
	var createdAt, updatedAt time.Time
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO transactional_templates (organization_id, name)
		VALUES ($1, $2)
		RETURNING id::text, created_at, updated_at
	`, orgID, name).Scan(&id, &createdAt, &updatedAt); err != nil {
		s.logger.Error("insert transactional template", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional template.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO transactional_template_versions
			(template_id, version, subject, html, text_content, variables, created_by)
		VALUES ($1, 1, $2, $3, $4, $5, $6)
	`, id, source.Subject, source.HTML, source.Text, variables, transactionalTemplateActor(r)); err != nil {
		s.logger.Error("insert transactional template version", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional template.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit transactional template creation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional template.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.template_created", "")
	response := transactionalTemplateResponse{
		ID: id, Name: name, CurrentVersion: 1, Subject: source.Subject,
		HTML: source.HTML, Text: source.Text, Variables: variables,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	httpx.JSON(w, http.StatusCreated, response)
}

func (s *Server) getTransactionalTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_id", "The template ID is invalid.")
		return
	}
	template, err := s.loadTransactionalTemplate(r, id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "template_not_found", "The transactional template could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load transactional template", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional template.")
		return
	}
	httpx.JSON(w, http.StatusOK, template)
}

func (s *Server) updateTransactionalTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_id", "The template ID is invalid.")
		return
	}
	var input transactionalTemplateInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if input.Name == nil && input.Subject == nil && input.HTML == nil && input.Text == nil {
		httpx.Error(w, http.StatusBadRequest, "empty_template_update", "At least one template field must be provided.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin transactional template update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional template.")
		return
	}
	defer tx.Rollback(r.Context())

	var template transactionalTemplateResponse
	err = tx.QueryRow(r.Context(), `
		SELECT t.id::text, t.name, t.current_version, v.subject, v.html, v.text_content,
		       v.variables, t.created_at, t.updated_at
		FROM transactional_templates t
		JOIN transactional_template_versions v
		  ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.id = $1 AND t.organization_id = $2
		FOR UPDATE OF t
	`, id, s.organizationID(r)).Scan(
		&template.ID, &template.Name, &template.CurrentVersion, &template.Subject,
		&template.HTML, &template.Text, &template.Variables, &template.CreatedAt, &template.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "template_not_found", "The transactional template could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load transactional template for update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional template.")
		return
	}
	if input.Name != nil {
		template.Name = strings.TrimSpace(*input.Name)
	}
	if template.Name == "" || len(template.Name) > 100 || strings.ContainsAny(template.Name, "\r\n") {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_name", "The template name must be between 1 and 100 characters.")
		return
	}
	source := transactional.Template{Subject: template.Subject, HTML: template.HTML, Text: template.Text}
	contentChanged := input.Subject != nil || input.HTML != nil || input.Text != nil
	if input.Subject != nil {
		source.Subject = *input.Subject
	}
	if input.HTML != nil {
		source.HTML = *input.HTML
	}
	if input.Text != nil {
		source.Text = *input.Text
	}
	variables := template.Variables
	if contentChanged {
		variables, err = validateTransactionalTemplate(source)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_template", err.Error())
			return
		}
		template.CurrentVersion++
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE transactional_templates
		SET name = $3, current_version = $4, updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r), template.Name, template.CurrentVersion); err != nil {
		s.logger.Error("update transactional template metadata", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional template.")
		return
	}
	if contentChanged {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO transactional_template_versions
				(template_id, version, subject, html, text_content, variables, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, id, template.CurrentVersion, source.Subject, source.HTML, source.Text, variables, transactionalTemplateActor(r)); err != nil {
			s.logger.Error("insert transactional template version", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional template.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit transactional template update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional template.")
		return
	}
	template.Subject, template.HTML, template.Text = source.Subject, source.HTML, source.Text
	template.Variables = variables
	template.UpdatedAt = time.Now().UTC()
	s.recordTransactionalAudit(r, "transactional.template_updated", "")
	httpx.JSON(w, http.StatusOK, template)
}

func (s *Server) deleteTransactionalTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_id", "The template ID is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM transactional_templates WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r))
	if err != nil {
		s.logger.Error("delete transactional template", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete transactional template.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "template_not_found", "The transactional template could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.template_deleted", "")
	httpx.NoContent(w)
}

func (s *Server) loadTransactionalTemplate(r *http.Request, id string) (transactionalTemplateResponse, error) {
	var template transactionalTemplateResponse
	err := s.pool.QueryRow(r.Context(), `
		SELECT t.id::text, t.name, t.current_version, v.subject, v.html, v.text_content,
		       v.variables, t.created_at, t.updated_at
		FROM transactional_templates t
		JOIN transactional_template_versions v
		  ON v.template_id = t.id AND v.version = t.current_version
		WHERE t.id = $1 AND t.organization_id = $2
	`, id, s.organizationID(r)).Scan(
		&template.ID, &template.Name, &template.CurrentVersion, &template.Subject,
		&template.HTML, &template.Text, &template.Variables, &template.CreatedAt, &template.UpdatedAt,
	)
	return template, err
}

func validateTransactionalTemplate(source transactional.Template) ([]string, error) {
	if strings.TrimSpace(source.Subject) == "" || strings.ContainsAny(source.Subject, "\r\n") {
		return nil, errors.New("template subject is required and must not contain line breaks")
	}
	if source.HTML == "" && source.Text == "" {
		return nil, errors.New("template must include HTML or text content")
	}
	if len(source.Subject)+len(source.HTML)+len(source.Text) > 1<<20 {
		return nil, errors.New("template content exceeds the 1 MiB limit")
	}
	return transactional.TemplateVariableNames(source)
}

func transactionalTemplateActor(r *http.Request) any {
	if claims, ok := auth.FromContext(r.Context()); ok {
		if id, err := uuid.Parse(claims.Subject); err == nil {
			return id.String()
		}
	}
	return nil
}

func (s *Server) listTransactionalWebhooks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, url, events, enabled, consecutive_failures, disabled_at, created_at
		FROM transactional_webhooks
		WHERE organization_id = $1
		ORDER BY created_at DESC, id DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list transactional webhooks", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional webhooks.")
		return
	}
	defer rows.Close()
	webhooks := make([]transactionalWebhookResponse, 0)
	for rows.Next() {
		var webhook transactionalWebhookResponse
		if err := rows.Scan(
			&webhook.ID, &webhook.URL, &webhook.Events, &webhook.Enabled,
			&webhook.ConsecutiveFailures, &webhook.DisabledAt, &webhook.CreatedAt,
		); err != nil {
			s.logger.Error("scan transactional webhook", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional webhooks.")
			return
		}
		webhooks = append(webhooks, webhook)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate transactional webhooks", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list transactional webhooks.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": webhooks})
}

func (s *Server) createTransactionalWebhook(w http.ResponseWriter, r *http.Request) {
	var input transactionalWebhookInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if err := transactional.ValidateWebhookURL(input.URL); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_url", err.Error())
		return
	}
	events, err := validateTransactionalWebhookEvents(input.Events)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_events", err.Error())
		return
	}
	secret, err := transactional.NewWebhookSigningSecret()
	if err != nil {
		s.logger.Error("generate transactional webhook secret", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional webhook.")
		return
	}
	encryptedSecret, err := transactional.EncryptWebhookSecret(s.cfg.DKIMEncryptionKey, secret)
	if err != nil {
		s.logger.Error("encrypt transactional webhook secret", "error", err)
		httpx.Error(w, http.StatusServiceUnavailable, "webhook_encryption_unavailable", "The webhook secret encryption key is not configured.")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	var webhook transactionalWebhookResponse
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO transactional_webhooks
			(organization_id, url, encrypted_secret, events, enabled)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, url, events, enabled, consecutive_failures, disabled_at, created_at
	`, s.organizationID(r), strings.TrimSpace(input.URL), encryptedSecret, events, enabled).Scan(
		&webhook.ID, &webhook.URL, &webhook.Events, &webhook.Enabled,
		&webhook.ConsecutiveFailures, &webhook.DisabledAt, &webhook.CreatedAt,
	)
	if err != nil {
		s.logger.Error("create transactional webhook", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create transactional webhook.")
		return
	}
	webhook.SigningSecret = secret
	s.recordTransactionalAudit(r, "transactional.webhook_created", "")
	httpx.JSON(w, http.StatusCreated, webhook)
}

func (s *Server) updateTransactionalWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_id", "The webhook ID is invalid.")
		return
	}
	var input transactionalWebhookInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if err := transactional.ValidateWebhookURL(input.URL); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_url", err.Error())
		return
	}
	events, err := validateTransactionalWebhookEvents(input.Events)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_events", err.Error())
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE transactional_webhooks
		SET url = $3, events = $4, enabled = $5,
		    disabled_at = CASE WHEN $5 THEN NULL ELSE disabled_at END,
		    consecutive_failures = CASE WHEN $5 THEN 0 ELSE consecutive_failures END,
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r), strings.TrimSpace(input.URL), events, enabled)
	if err != nil {
		s.logger.Error("update transactional webhook", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional webhook.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "webhook_not_found", "The webhook could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.webhook_updated", "")
	s.listTransactionalWebhooksByID(w, r, id)
}

func (s *Server) rotateTransactionalWebhookSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_id", "The webhook ID is invalid.")
		return
	}
	secret, err := transactional.NewWebhookSigningSecret()
	if err != nil {
		s.logger.Error("generate rotated transactional webhook secret", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional webhook secret.")
		return
	}
	encryptedSecret, err := transactional.EncryptWebhookSecret(s.cfg.DKIMEncryptionKey, secret)
	if err != nil {
		s.logger.Error("encrypt rotated transactional webhook secret", "error", err)
		httpx.Error(w, http.StatusServiceUnavailable, "webhook_encryption_unavailable", "The webhook secret encryption key is not configured.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE transactional_webhooks SET encrypted_secret = $3, updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r), encryptedSecret)
	if err != nil {
		s.logger.Error("rotate transactional webhook secret", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not rotate transactional webhook secret.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "webhook_not_found", "The webhook could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.webhook_secret_rotated", "")
	httpx.JSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
}

func (s *Server) deleteTransactionalWebhook(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_webhook_id", "The webhook ID is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM transactional_webhooks WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r))
	if err != nil {
		s.logger.Error("delete transactional webhook", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete transactional webhook.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "webhook_not_found", "The webhook could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.webhook_deleted", "")
	httpx.NoContent(w)
}

func (s *Server) listTransactionalWebhooksByID(w http.ResponseWriter, r *http.Request, id string) {
	var webhook transactionalWebhookResponse
	err := s.pool.QueryRow(r.Context(), `
		SELECT id::text, url, events, enabled, consecutive_failures, disabled_at, created_at
		FROM transactional_webhooks WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r)).Scan(
		&webhook.ID, &webhook.URL, &webhook.Events, &webhook.Enabled,
		&webhook.ConsecutiveFailures, &webhook.DisabledAt, &webhook.CreatedAt,
	)
	if err != nil {
		s.logger.Error("load updated transactional webhook", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load updated transactional webhook.")
		return
	}
	httpx.JSON(w, http.StatusOK, webhook)
}

func validateTransactionalWebhookEvents(events []string) ([]string, error) {
	if len(events) == 0 || len(events) > len(transactionalWebhookEvents) {
		return nil, errors.New("select between one and twelve supported transactional events")
	}
	normalized := make([]string, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		event = strings.TrimSpace(event)
		if !transactionalWebhookEvents[event] {
			return nil, fmt.Errorf("unsupported transactional event %q", event)
		}
		if !seen[event] {
			normalized = append(normalized, event)
			seen[event] = true
		}
	}
	return normalized, nil
}

func (s *Server) getTransactionalSettings(w http.ResponseWriter, r *http.Request) {
	var settings map[string]any
	var enabled bool
	var status, pauseReason string
	var requestsPerSecond, dailyLimit, monthlyLimit, maxRecipients, minimumSampleSize int
	var maxMessageBytes int64
	var maxBounceRate, maxComplaintRate float32
	err := s.pool.QueryRow(r.Context(), `
		SELECT enabled, status, pause_reason, requests_per_second, daily_limit, monthly_limit,
		       max_recipients, max_message_bytes, max_bounce_rate, max_complaint_rate, minimum_sample_size
		FROM transactional_settings WHERE organization_id = $1
	`, s.organizationID(r)).Scan(
		&enabled, &status, &pauseReason, &requestsPerSecond, &dailyLimit, &monthlyLimit,
		&maxRecipients, &maxMessageBytes, &maxBounceRate, &maxComplaintRate, &minimumSampleSize,
	)
	if err != nil {
		s.logger.Error("load transactional settings", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load transactional settings.")
		return
	}
	settings = map[string]any{
		"enabled": enabled, "status": status, "pause_reason": pauseReason,
		"requests_per_second": requestsPerSecond, "daily_limit": dailyLimit,
		"monthly_limit": monthlyLimit, "max_recipients": maxRecipients,
		"max_message_bytes": maxMessageBytes, "max_bounce_rate": maxBounceRate,
		"max_complaint_rate": maxComplaintRate, "minimum_sample_size": minimumSampleSize,
	}
	httpx.JSON(w, http.StatusOK, settings)
}

func (s *Server) updateTransactionalSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled     bool   `json:"enabled"`
		Status      string `json:"status"`
		PauseReason string `json:"pause_reason"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		if input.Enabled {
			input.Status = "active"
		} else {
			input.Status = "disabled"
		}
	}
	if input.Status != "active" && input.Status != "paused" && input.Status != "disabled" {
		httpx.Error(w, http.StatusBadRequest, "invalid_status", "Status must be active, paused, or disabled.")
		return
	}
	if input.Status == "disabled" {
		input.Enabled = false
	} else if input.Status == "active" || input.Status == "paused" {
		input.Enabled = true
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE transactional_settings
		SET enabled = $2, status = $3, pause_reason = $4, updated_at = now()
		WHERE organization_id = $1
	`, s.organizationID(r), input.Enabled, input.Status, input.PauseReason)
	if err != nil {
		s.logger.Error("update transactional settings", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update transactional settings.")
		return
	}
	if tag.RowsAffected() != 1 {
		httpx.Error(w, http.StatusNotFound, "settings_not_found", "Transactional settings could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.settings_updated", "")
	s.getTransactionalSettings(w, r)
}

func (s *Server) recordTransactionalAudit(r *http.Request, eventType, keyID string) {
	claims := s.claims(r)
	var actorUserID any
	if claims != nil && claims.Subject != "" {
		actorUserID = claims.Subject
	}
	var apiKeyID any
	if keyID != "" {
		apiKeyID = keyID
	}
	if _, err := s.pool.Exec(r.Context(), `
		INSERT INTO transactional_audit_events (organization_id, actor_user_id, api_key_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, $5)
	`, s.organizationID(r), actorUserID, apiKeyID, eventType, json.RawMessage(`{}`)); err != nil {
		s.logger.Error("record transactional audit event", "error", err, "event_type", eventType)
	}
}

func normalizeTransactionalScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{"send", "read"}, nil
	}
	allowed := map[string]bool{"send": true, "read": true}
	normalized := make([]string, 0, len(scopes))
	seen := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if !allowed[scope] {
			return nil, fmt.Errorf("unsupported scope %q; allowed scopes are send and read", scope)
		}
		if !seen[scope] {
			normalized = append(normalized, scope)
			seen[scope] = true
		}
	}
	if len(normalized) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	return normalized, nil
}

func normalizeTransactionalIPAllowlist(rawNetworks []string) ([]string, error) {
	networks := make([]string, 0, len(rawNetworks))
	seen := make(map[string]bool, len(rawNetworks))
	for _, raw := range rawNetworks {
		raw = strings.TrimSpace(raw)
		if ip := net.ParseIP(raw); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			raw = fmt.Sprintf("%s/%d", ip.String(), bits)
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid IP address or CIDR %q", raw)
		}
		normalized := network.String()
		if !seen[normalized] {
			networks = append(networks, normalized)
			seen[normalized] = true
		}
	}
	return networks, nil
}

func parseTransactionalKeyExpiry(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errors.New("expiration must be an RFC3339 timestamp")
	}
	if !expiresAt.After(time.Now()) {
		return nil, errors.New("expiration must be in the future")
	}
	return &expiresAt, nil
}

func newTransactionalAPIKey(testMode bool) (plain, prefix, keyHash string, err error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", "", fmt.Errorf("generate API key material: %w", err)
	}
	mode := "live"
	if testMode {
		mode = "test"
	}
	plain = "sw_tx_" + mode + "_" + base64.RawURLEncoding.EncodeToString(random)
	prefix = plain[:transactionalAPIKeyPrefixLength]
	digest := sha256.Sum256([]byte(plain))
	return plain, prefix, hex.EncodeToString(digest[:]), nil
}
