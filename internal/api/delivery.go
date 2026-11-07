package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/httpx"
	"github.com/khayode/email-platform/internal/messaging"
)

type deliveryService interface {
	ReplayDeadLetter(ctx context.Context, orgID, jobID string) error
}

type deadLetterItem struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	CampaignID     string     `json:"campaign_id"`
	ContactID      string     `json:"contact_id"`
	DeliveryJobID  string     `json:"delivery_job_id"`
	Reason         string     `json:"reason"`
	Attempts       int        `json:"attempts"`
	LastError      string     `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	ReplayedAt     *time.Time `json:"replayed_at,omitempty"`
}

type suppressionItem struct {
	Email          string    `json:"email"`
	Reason         string    `json:"reason"`
	Source         string    `json:"source"`
	OrganizationID string    `json:"organization_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type bounceWebhook struct {
	Event        string `json:"event"`
	Email        string `json:"email"`
	Domain       string `json:"domain"`
	SMTPCode     string `json:"smtp_code"`
	SMTPEnhanced string `json:"smtp_enhanced"`
	Reason       string `json:"reason"`
	CampaignID   string `json:"campaign_id,omitempty"`
	JobID        string `json:"job_id,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
}

type complaintWebhook struct {
	Event        string `json:"event"`
	Email        string `json:"email"`
	Domain       string `json:"domain"`
	FeedbackType string `json:"feedback_type"`
	CampaignID   string `json:"campaign_id,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
}

type DeliveryHandler struct {
	pool             *pgxpool.Pool
	tokens           *auth.Manager
	logger           *slog.Logger
	maxBounceRate    float64
	maxComplaintRate float64
	replayer         deliveryService
	publisher        publisher
	webhookSecret    string
	idempotency      *idempotencyStore
}

type publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

func NewDeliveryHandler(pool *pgxpool.Pool, tokens *auth.Manager, logger *slog.Logger, replayer deliveryService, publisher publisher, webhookSecret string, thresholds ...float64) *DeliveryHandler {
	maxBounceRate, maxComplaintRate := 0.05, 0.001
	if len(thresholds) > 0 {
		maxBounceRate = thresholds[0]
	}
	if len(thresholds) > 1 {
		maxComplaintRate = thresholds[1]
	}
	return &DeliveryHandler{
		pool:             pool,
		tokens:           tokens,
		logger:           logger,
		maxBounceRate:    maxBounceRate,
		maxComplaintRate: maxComplaintRate,
		replayer:         replayer,
		publisher:        publisher,
		webhookSecret:    webhookSecret,
		idempotency:      newIdempotencyStore(),
	}
}

// listDeadLetter returns dead-lettered jobs for the organization.
func (h *DeliveryHandler) listDeadLetter(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, organization_id, campaign_id, contact_id, delivery_job_id,
		       reason, attempts, last_error, created_at, replayed_at
		FROM dead_letter_queue
		WHERE organization_id = $1 AND replayed_at IS NULL
		ORDER BY created_at DESC
		LIMIT 100
	`, h.organizationID(r))
	if err != nil {
		h.logger.Error("list dead letter", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load dead-letter queue.")
		return
	}
	defer rows.Close()

	items := make([]deadLetterItem, 0)
	for rows.Next() {
		var item deadLetterItem
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.CampaignID, &item.ContactID,
			&item.DeliveryJobID, &item.Reason, &item.Attempts, &item.LastError,
			&item.CreatedAt, &item.ReplayedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not scan dead-letter item.")
			return
		}
		items = append(items, item)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// replayDeadLetter re-queues a dead-lettered delivery job for retry.
func (h *DeliveryHandler) replayDeadLetter(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Dead-letter job ID is required.")
		return
	}

	var dlqID, orgID string
	if err := h.pool.QueryRow(r.Context(), `
		SELECT id, organization_id FROM dead_letter_queue
		WHERE id = $1 AND organization_id = $2 AND replayed_at IS NULL
	`, jobID, h.organizationID(r)).Scan(&dlqID, &orgID); err != nil {
		if err == pgx.ErrNoRows {
			httpx.Error(w, http.StatusNotFound, "not_found", "Dead-letter job not found.")
			return
		}
		h.logger.Error("load dead letter", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load dead-letter job.")
		return
	}

	if h.replayer != nil {
		if err := h.replayer.ReplayDeadLetter(r.Context(), orgID, jobID); err != nil {
			h.logger.Error("replay dead letter", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not replay dead-letter job.")
			return
		}
	} else {
		if _, err := h.pool.Exec(r.Context(), `
			UPDATE delivery_jobs
			SET status = 'queued', dead_lettered_at = NULL, next_attempt_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'failed' AND dead_lettered_at IS NOT NULL
		`, jobID); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reset delivery job.")
			return
		}
		if h.publisher != nil {
			payload, _ := json.Marshal(map[string]string{
				"job_id":          jobID,
				"organization_id": orgID,
			})
			_ = h.publisher.Publish(r.Context(), messaging.SubjectDeliveryJobs, payload)
		}
	}

	if _, err := h.pool.Exec(r.Context(), `
		UPDATE dead_letter_queue SET replayed_at = now() WHERE id = $1
	`, dlqID); err != nil {
		h.logger.Warn("mark dead letter replayed", "error", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// listSuppressions returns suppressed contacts for the organization.
func (h *DeliveryHandler) listSuppressions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT email, reason, source, organization_id, created_at
		FROM suppressions
		WHERE organization_id = $1
		ORDER BY created_at DESC
		LIMIT 1000
	`, h.organizationID(r))
	if err != nil {
		h.logger.Error("list suppressions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load suppressions.")
		return
	}
	defer rows.Close()

	items := make([]suppressionItem, 0)
	for rows.Next() {
		var item suppressionItem
		item.OrganizationID = h.organizationID(r)
		if err := rows.Scan(&item.Email, &item.Reason, &item.Source, &item.OrganizationID, &item.CreatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not scan suppression.")
			return
		}
		items = append(items, item)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// deleteSuppression removes a suppression so the contact can receive mail again.
func (h *DeliveryHandler) deleteSuppression(w http.ResponseWriter, r *http.Request) {
	email := r.PathValue("email")
	if email == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Email is required.")
		return
	}

	if _, err := h.pool.Exec(r.Context(), `
		DELETE FROM suppressions
		WHERE organization_id = $1 AND lower(email) = lower($2)
	`, h.organizationID(r), email); err != nil {
		h.logger.Error("delete suppression", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove suppression.")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// bounceWebhook receives inbound bounce/DSN notifications from Stalwart (or
// another SMTP relay) and suppresses the affected contact.
func (h *DeliveryHandler) bounceWebhook(w http.ResponseWriter, r *http.Request) {
	var input bounceWebhook
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Invalid bounce webhook payload.")
		return
	}
	if input.Email == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Email is required.")
		return
	}

	orgID := h.organizationID(r)
	if input.Domain != "" {
		if err := h.pool.QueryRow(r.Context(), `
			SELECT organization_id FROM sending_domains
			WHERE lower(domain) = lower($1)
			LIMIT 1
		`, input.Domain).Scan(&orgID); err != nil && err != pgx.ErrNoRows {
			h.logger.Error("resolve domain org", "error", err)
		}
	}
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain", "Unable to resolve organization for domain.")
		return
	}

	reason := "hard_bounce"
	if strings.EqualFold(input.Event, "complaint") || strings.EqualFold(input.Event, "abuse") {
		reason = "complaint"
	} else if strings.EqualFold(input.Event, "unsubscribe") {
		reason = "unsubscribe"
	}

	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, lower($2), $3, 'bounce_webhook')
		ON CONFLICT (organization_id, lower(email)) DO NOTHING
	`, orgID, input.Email, reason); err != nil {
		h.logger.Error("record bounce suppression", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record suppression.")
		return
	}

	if _, err := h.pool.Exec(r.Context(), `
		UPDATE contacts
		SET status = 'suppressed', updated_at = now()
		WHERE organization_id = $1 AND lower(email) = lower($2)
	`, orgID, input.Email); err != nil {
		h.logger.Warn("mark contact suppressed", "error", err)
	}

	if reason == "hard_bounce" || reason == "complaint" {
		eventType := "bounced"
		if reason == "complaint" {
			eventType = "complained"
		}
		if err := h.recordReputationSignal(r.Context(), orgID, input.Email, input.CampaignID, eventType, input.Reason); err != nil {
			h.logger.Error("record reputation signal", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record delivery feedback.")
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// complaintWebhook receives inbound spam-complaint (FBL) notifications and
// suppresses the affected contact immediately.
func (h *DeliveryHandler) complaintWebhook(w http.ResponseWriter, r *http.Request) {
	var input complaintWebhook
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Invalid complaint webhook payload.")
		return
	}
	if input.Email == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Email is required.")
		return
	}

	orgID := h.organizationID(r)
	if input.Domain != "" {
		if err := h.pool.QueryRow(r.Context(), `
			SELECT organization_id FROM sending_domains
			WHERE lower(domain) = lower($1)
			LIMIT 1
		`, input.Domain).Scan(&orgID); err != nil && err != pgx.ErrNoRows {
			h.logger.Error("resolve domain org", "error", err)
		}
	}
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain", "Unable to resolve organization for domain.")
		return
	}

	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, lower($2), 'complaint', 'feedback_loop')
		ON CONFLICT (organization_id, lower(email)) DO NOTHING
	`, orgID, input.Email); err != nil {
		h.logger.Error("record complaint suppression", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record suppression.")
		return
	}

	if _, err := h.pool.Exec(r.Context(), `
		UPDATE contacts
		SET status = 'suppressed', updated_at = now()
		WHERE organization_id = $1 AND lower(email) = lower($2)
	`, orgID, input.Email); err != nil {
		h.logger.Warn("mark contact suppressed", "error", err)
	}

	if err := h.recordReputationSignal(r.Context(), orgID, input.Email, input.CampaignID, "complained", input.FeedbackType); err != nil {
		h.logger.Error("record complaint signal", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record complaint feedback.")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DeliveryHandler) recordReputationSignal(ctx context.Context, organizationID, email, campaignID, eventType, details string) error {
	status := "bounced"
	if eventType == "complained" {
		status = "complained"
	}
	rows, err := h.pool.Query(ctx, `
		SELECT dj.id, dj.campaign_id, dj.contact_id
		FROM delivery_jobs dj
		JOIN campaign_recipients cr
		  ON cr.campaign_id = dj.campaign_id AND cr.contact_id = dj.contact_id
		WHERE dj.organization_id = $1 AND lower(cr.email) = lower($2)
		  AND ($3::uuid IS NULL OR dj.campaign_id = $3)
		  AND dj.status IN ('queued', 'processing', 'deferred', 'submitted', 'accepted')
	`, organizationID, email, nullIfEmpty(campaignID))
	if err != nil {
		return fmt.Errorf("find affected campaign deliveries: %w", err)
	}
	type affectedDelivery struct{ jobID, campaignID, contactID string }
	var deliveries []affectedDelivery
	for rows.Next() {
		var item affectedDelivery
		if err := rows.Scan(&item.jobID, &item.campaignID, &item.contactID); err != nil {
			rows.Close()
			return fmt.Errorf("scan affected campaign delivery: %w", err)
		}
		deliveries = append(deliveries, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate affected campaign deliveries: %w", err)
	}
	rows.Close()

	campaigns := make(map[string]struct{})
	for _, item := range deliveries {
		if _, err := h.pool.Exec(ctx, `
			UPDATE delivery_jobs SET status = $2, last_error = $3, updated_at = now()
			WHERE id = $1
		`, item.jobID, status, details); err != nil {
			return fmt.Errorf("update affected delivery: %w", err)
		}
		if _, err := h.pool.Exec(ctx, `
			UPDATE campaign_recipients SET status = $3
			WHERE campaign_id = $1 AND contact_id = $2
		`, item.campaignID, item.contactID, status); err != nil {
			return fmt.Errorf("update campaign recipient feedback: %w", err)
		}
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO message_events
				(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
			VALUES ($1, $2, $3, $4, $5, jsonb_build_object('feedback', $6::text))
		`, organizationID, item.campaignID, item.contactID, item.jobID, eventType, details); err != nil {
			return fmt.Errorf("record campaign feedback event: %w", err)
		}
		campaigns[item.campaignID] = struct{}{}
	}

	for campaignID := range campaigns {
		var total, bounced, complained int
		if err := h.pool.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE status = 'bounced'),
			       count(*) FILTER (WHERE status = 'complained')
			FROM campaign_recipients
			WHERE organization_id = $1 AND campaign_id = $2
		`, organizationID, campaignID).Scan(&total, &bounced, &complained); err != nil {
			return fmt.Errorf("count campaign reputation events: %w", err)
		}
		if total == 0 || (float64(bounced)/float64(total) <= h.maxBounceRate &&
			float64(complained)/float64(total) <= h.maxComplaintRate) {
			continue
		}
		message := fmt.Sprintf("Campaign paused automatically: bounce rate %.2f%% (limit %.2f%%), complaint rate %.3f%% (limit %.3f%%).",
			100*float64(bounced)/float64(total), 100*h.maxBounceRate,
			100*float64(complained)/float64(total), 100*h.maxComplaintRate)
		var pausedID string
		err := h.pool.QueryRow(ctx, `
			UPDATE campaigns SET status = 'paused', paused_at = now(), updated_at = now()
			WHERE id = $1 AND organization_id = $2 AND status IN ('processing', 'sending')
			RETURNING id
		`, campaignID, organizationID).Scan(&pausedID)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("pause campaign after reputation threshold: %w", err)
		}
		if _, err := h.pool.Exec(ctx, `
			INSERT INTO campaign_alerts (organization_id, campaign_id, alert_type, message)
			VALUES ($1, $2, 'reputation_pause', $3)
		`, organizationID, campaignID, message); err != nil {
			return fmt.Errorf("create campaign reputation alert: %w", err)
		}
		h.logger.Warn("campaign paused after reputation threshold", "campaign_id", campaignID, "message", message)
	}
	return nil
}

type suppressionInput struct {
	Email string `json:"email"`
}

// createSuppression adds an email address to the organization suppression list.
func (h *DeliveryHandler) createSuppression(w http.ResponseWriter, r *http.Request) {
	var input suppressionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Invalid request body.")
		return
	}
	if input.Email == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Email is required.")
		return
	}

	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, lower($2), 'manual', 'dashboard')
		ON CONFLICT (organization_id, lower(email)) DO NOTHING
	`, h.organizationID(r), input.Email); err != nil {
		h.logger.Error("create suppression", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not suppress address.")
		return
	}

	if _, err := h.pool.Exec(r.Context(), `
		UPDATE contacts
		SET status = 'suppressed', updated_at = now()
		WHERE organization_id = $1 AND lower(email) = lower($2)
	`, h.organizationID(r), input.Email); err != nil {
		h.logger.Warn("mark contact suppressed", "error", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// organizationID returns the organization ID from the request context.
// Returns empty string when no auth context is present (e.g. webhooks).
func (h *DeliveryHandler) organizationID(r *http.Request) string {
	claims, _ := auth.FromContext(r.Context())
	if claims == nil {
		return ""
	}
	return claims.OrganizationID
}

// ------------------------------------------------------------
// Webhook security: HMAC signature verification, replay prevention,
// idempotency keys.
// ------------------------------------------------------------

// webhookSignature parses the X-Webhook-Signature header of the form:
//
//	t=<timestamp>, v1=<hmac_hex>
type webhookSignature struct {
	Timestamp int64
	HMAC      string
}

func parseWebhookSignature(header string) (webhookSignature, bool) {
	var sig webhookSignature
	parts := strings.Split(header, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "t":
			if ts, err := strconv.ParseInt(val, 10, 64); err == nil {
				sig.Timestamp = ts
			}
		case "v1":
			sig.HMAC = val
		}
	}
	return sig, sig.HMAC != "" && sig.Timestamp > 0
}

// verifyWebhookSignature returns true when the request body is authentic
// according to the shared webhook secret. The expected signature scheme is:
//
//	signature = HMAC-SHA256(secret, fmt.Sprintf("%d.%s", timestamp, body))
//
// A tolerance of 5 minutes is applied to the timestamp to prevent replay
// attacks while allowing for clock skew.
func (h *DeliveryHandler) verifyWebhookSignature(body []byte, signatureHeader string) error {
	if h.webhookSecret == "" {
		return fmt.Errorf("webhook secret not configured")
	}

	sig, ok := parseWebhookSignature(signatureHeader)
	if !ok {
		return fmt.Errorf("missing or malformed webhook signature")
	}

	// Replay prevention: reject timestamps older than 5 minutes.
	if time.Since(time.Unix(sig.Timestamp, 0)) > 5*time.Minute {
		return fmt.Errorf("webhook timestamp outside tolerance window")
	}
	if time.Unix(sig.Timestamp, 0).After(time.Now().Add(5 * time.Minute)) {
		return fmt.Errorf("webhook timestamp in the future")
	}

	// Compute expected HMAC over "timestamp.body".
	payload := fmt.Sprintf("%d.%s", sig.Timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig.HMAC), []byte(expected)) {
		return fmt.Errorf("webhook signature mismatch")
	}

	return nil
}

// idempotencyEntry records a single processed webhook idempotency key with expiry.
type idempotencyEntry struct {
	key     string
	expires time.Time
}

// idempotencyStore is a small in-memory store with TTL-based expiration.
// Suitable for single-instance deployments. Multi-instance deployments should
// replace this with Redis or a database-backed store.
type idempotencyStore struct {
	mu    sync.RWMutex
	items map[string]idempotencyEntry
	ttl   time.Duration
}

func newIdempotencyStore() *idempotencyStore {
	s := &idempotencyStore{
		items: make(map[string]idempotencyEntry),
		ttl:   1 * time.Hour,
	}
	go s.sweep()
	return s
}

func (s *idempotencyStore) sweep() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, v := range s.items {
			if now.After(v.expires) {
				delete(s.items, k)
			}
		}
		s.mu.Unlock()
	}
}

// checkAndMark returns true when the key is new (first sighting) and records it.
// Returns false when the key was already seen (duplicate request).
func (s *idempotencyStore) checkAndMark(key string) bool {
	if key == "" {
		return true // no idempotency key provided; always process
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[key]; exists {
		return false
	}
	s.items[key] = idempotencyEntry{key: key, expires: time.Now().Add(s.ttl)}
	return true
}

// requireWebhookAuth is middleware that verifies HMAC signatures and idempotency
// for inbound webhook requests. It reads the raw body, verifies the signature,
// checks the idempotency key, then hands the (unmodified) body and request to
// the inner handler.
func (h *DeliveryHandler) requireWebhookAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read raw body for signature verification.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.logger.Error("read webhook body", "error", err)
			httpx.Error(w, http.StatusBadRequest, "invalid_body", "Could not read request body.")
			return
		}
		// Restore body so downstream JSON decoding works.
		r.Body = io.NopCloser(bytes.NewReader(body))

		// Verify HMAC signature.
		sigHeader := r.Header.Get("X-Webhook-Signature")
		if err := h.verifyWebhookSignature(body, sigHeader); err != nil {
			h.logger.Warn("webhook signature verification failed", "error", err)
			httpx.Error(w, http.StatusUnauthorized, "invalid_signature", "Invalid webhook signature.")
			return
		}

		// Idempotency check.
		idemKey := r.Header.Get("Idempotency-Key")
		if !h.idempotency.checkAndMark(idemKey) {
			httpx.Error(w, http.StatusConflict, "duplicate_request", "This webhook event has already been processed.")
			return
		}

		next.ServeHTTP(w, r)
	}
}
