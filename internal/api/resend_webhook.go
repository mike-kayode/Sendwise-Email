package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

const resendWebhookBodyLimit = 1 << 20

type resendWebhookPayload struct {
	Type string `json:"type"`
	Data struct {
		EmailID string          `json:"email_id"`
		To      json.RawMessage `json:"to"`
		Tags    json.RawMessage `json:"tags"`
		Bounce  struct {
			Type string `json:"type"`
		} `json:"bounce"`
		Reason string `json:"reason"`
	} `json:"data"`
}

func (h *DeliveryHandler) resendWebhook(w http.ResponseWriter, r *http.Request) {
	if h.resendSecret == "" {
		httpx.Error(w, http.StatusServiceUnavailable, "webhook_unconfigured", "Resend webhook is not configured.")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, resendWebhookBodyLimit+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Could not read webhook payload.")
		return
	}
	if len(body) > resendWebhookBodyLimit {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Webhook payload is too large.")
		return
	}

	svixID := r.Header.Get("svix-id")
	svixTimestamp := r.Header.Get("svix-timestamp")
	if !verifyResendWebhookSignature(h.resendSecret, svixID, svixTimestamp, r.Header.Get("svix-signature"), body, time.Now()) {
		httpx.Error(w, http.StatusUnauthorized, "invalid_signature", "Invalid webhook signature.")
		return
	}

	var payload resendWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Invalid webhook payload.")
		return
	}

	tags := parseResendTags(payload.Data.Tags)
	workspaceID := parseWorkspaceID(tags["workspace_id"])
	eventType, ok := resendEventTypes[payload.Type]

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		h.logger.Error("begin Resend webhook transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process webhook.")
		return
	}
	defer tx.Rollback(r.Context())

	payloadHash := sha256.Sum256(body)
	var insertedID string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO resend_webhook_events (svix_id, workspace_id, event_type, payload_sha256)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (svix_id) DO NOTHING
		RETURNING svix_id
	`, svixID, workspaceID, payload.Type, hex.EncodeToString(payloadHash[:])).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		h.logger.Error("record Resend webhook", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process webhook.")
		return
	}

	if ok && workspaceID != nil {
		campaignID, campaignOK := parseUUID(tags["campaign_id"])
		messageID := strings.TrimSpace(tags["message_id"])
		recipient := resendRecipient(payload.Data.To)
		if campaignOK && messageID != "" && recipient != "" {
			if err := applyResendEvent(r.Context(), tx, *workspaceID, campaignID, messageID, recipient, payload, eventType, svixID); err != nil {
				h.logger.Error("apply Resend webhook event", "svix_id", svixID, "event_type", payload.Type, "error", err)
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process webhook.")
				return
			}
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		h.logger.Error("commit Resend webhook transaction", "svix_id", svixID, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process webhook.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func verifyResendWebhookSignature(secret, id, timestamp, signature string, body []byte, now time.Time) bool {
	if id == "" || timestamp == "" || signature == "" || !strings.HasPrefix(secret, "whsec_") {
		return false
	}
	secretBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(secretBytes) == 0 {
		return false
	}
	unixTime, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	eventTime := time.Unix(unixTime, 0)
	if eventTime.Before(now.Add(-5*time.Minute)) || eventTime.After(now.Add(5*time.Minute)) {
		return false
	}

	mac := hmac.New(sha256.New, secretBytes)
	_, _ = mac.Write([]byte(id + "." + timestamp + "."))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	for _, value := range strings.Fields(signature) {
		version, encoded, found := strings.Cut(strings.TrimSpace(value), ",")
		if found && version == "v1" {
			if supplied, decodeErr := base64.StdEncoding.DecodeString(encoded); decodeErr == nil && hmac.Equal(expected, supplied) {
				return true
			}
		}
	}
	return false
}

func parseResendTags(raw json.RawMessage) map[string]string {
	tags := make(map[string]string)
	if len(raw) == 0 {
		return tags
	}
	if err := json.Unmarshal(raw, &tags); err == nil {
		return tags
	}
	var list []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, tag := range list {
			tags[tag.Name] = tag.Value
		}
	}
	return tags
}

func parseWorkspaceID(value string) *string {
	id, ok := parseUUID(value)
	if !ok {
		return nil
	}
	return &id
}

func parseUUID(value string) (string, bool) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return id.String(), true
}

func resendRecipient(raw json.RawMessage) string {
	var recipients []string
	if err := json.Unmarshal(raw, &recipients); err == nil && len(recipients) > 0 {
		return strings.ToLower(strings.TrimSpace(recipients[0]))
	}
	var recipient string
	if err := json.Unmarshal(raw, &recipient); err == nil {
		return strings.ToLower(strings.TrimSpace(recipient))
	}
	return ""
}

var resendEventTypes = map[string]string{
	"email.sent":             "accepted",
	"email.delivered":        "delivered",
	"email.delivery_delayed": "deferred",
	"email.bounced":          "bounced",
	"email.complained":       "complained",
	"email.opened":           "opened",
	"email.clicked":          "clicked",
	"email.failed":           "failed",
}

func applyResendEvent(ctx context.Context, tx pgx.Tx, workspaceID, campaignID, messageID, recipient string, payload resendWebhookPayload, eventType, svixID string) error {
	var jobID, contactID string
	err := tx.QueryRow(ctx, `
		SELECT dj.id, dj.contact_id
		FROM delivery_jobs dj
		JOIN contacts c ON c.id = dj.contact_id AND c.organization_id = dj.organization_id
		WHERE dj.organization_id = $1 AND dj.campaign_id = $2 AND dj.message_id = $3
		  AND lower(c.email) = $4
		FOR UPDATE OF dj
	`, workspaceID, campaignID, messageID, recipient).Scan(&jobID, &contactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	status := eventType
	switch eventType {
	case "accepted", "delivered", "deferred", "opened", "clicked":
		status = "accepted"
	case "bounced", "complained", "failed":
		_, err = tx.Exec(ctx, `
			UPDATE delivery_jobs SET status = $2, updated_at = now()
			WHERE id = $1
		`, jobID, status)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE campaign_recipients SET status = $3
			WHERE organization_id = $1 AND campaign_id = $2 AND contact_id = $4
		`, workspaceID, campaignID, status, contactID)
		if err != nil {
			return err
		}
	}
	if status == "accepted" {
		if _, err := tx.Exec(ctx, `
			UPDATE delivery_jobs SET status = 'accepted', updated_at = now()
			WHERE id = $1 AND status IN ('queued', 'processing', 'submitted', 'deferred')
		`, jobID); err != nil {
			return err
		}
	}

	metadata, err := json.Marshal(map[string]string{
		"provider": "resend",
		"svix_id":  svixID,
		"email_id": payload.Data.EmailID,
		"reason":   payload.Data.Reason,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, workspaceID, campaignID, contactID, jobID, eventType, metadata); err != nil {
		return err
	}

	suppressReason := ""
	switch eventType {
	case "complained":
		suppressReason = "complaint"
	case "bounced":
		if isHardResendBounce(payload.Data.Bounce.Type) {
			suppressReason = "hard_bounce"
		}
	}
	if suppressReason != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO suppressions (organization_id, email, reason, source)
			VALUES ($1, $2, $3, 'resend_webhook')
			ON CONFLICT (organization_id, lower(email)) DO NOTHING
		`, workspaceID, recipient, suppressReason); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE contacts SET status = 'suppressed', updated_at = now()
			WHERE organization_id = $1 AND lower(email) = $2
		`, workspaceID, recipient); err != nil {
			return err
		}
	}
	if eventType == "bounced" || eventType == "complained" {
		if eventType == "bounced" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO sending_domain_daily_stats
					(organization_id, domain, sending_day, reserved_count, successful_count, bounce_count)
				SELECT $1, lower(split_part(c.sender_email, '@', 2)),
				       COALESCE(dj.accepted_at::date, CURRENT_DATE), 1, 1, 1
				FROM delivery_jobs dj
				JOIN campaigns c ON c.id = dj.campaign_id AND c.organization_id = dj.organization_id
				WHERE dj.id = $2 AND dj.organization_id = $1
				ON CONFLICT (organization_id, domain, sending_day)
				DO UPDATE SET bounce_count = sending_domain_daily_stats.bounce_count + 1,
				              updated_at = now()
			`, workspaceID, jobID); err != nil {
				return err
			}
		}
		return pauseResendCampaignIfNeeded(ctx, tx, workspaceID, campaignID)
	}
	return nil
}

func isHardResendBounce(bounceType string) bool {
	bounceType = strings.ToLower(strings.TrimSpace(bounceType))
	return strings.Contains(bounceType, "hard") || strings.Contains(bounceType, "permanent")
}

func pauseResendCampaignIfNeeded(ctx context.Context, tx pgx.Tx, workspaceID, campaignID string) error {
	var total, bounced, complained int
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT delivery_job_id),
		       count(DISTINCT delivery_job_id) FILTER (WHERE event_type = 'bounced'),
		       count(DISTINCT delivery_job_id) FILTER (WHERE event_type = 'complained')
		FROM message_events
		WHERE organization_id = $1 AND campaign_id = $2
		  AND delivery_job_id IS NOT NULL
		  AND occurred_at >= now() - interval '24 hours'
		  AND event_type IN ('accepted', 'delivered', 'deferred', 'bounced', 'complained', 'failed')
	`, workspaceID, campaignID).Scan(&total, &bounced, &complained); err != nil {
		return err
	}
	if total == 0 || (float64(bounced)/float64(total) <= 0.02 && float64(complained)/float64(total) <= 0.0005) {
		return nil
	}
	message := "Campaign paused: Resend bounce or complaint rate exceeded the safety threshold."
	var pausedID string
	err := tx.QueryRow(ctx, `
		UPDATE campaigns SET status = 'paused', paused_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status IN ('processing', 'sending')
		RETURNING id
	`, campaignID, workspaceID).Scan(&pausedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO campaign_alerts (organization_id, campaign_id, alert_type, message)
		VALUES ($1, $2, 'reputation_pause', $3)
	`, workspaceID, campaignID, message)
	return err
}
