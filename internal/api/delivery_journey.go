package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/deliveryjourney"
	"github.com/khayode/email-platform/internal/httpx"
)

const maxStalwartTelemetryBody = 2 << 20

type stalwartTelemetryPayload struct {
	Events []stalwartTelemetryEvent `json:"events"`
}

type stalwartTelemetryEvent struct {
	ID        string                     `json:"id"`
	CreatedAt time.Time                  `json:"createdAt"`
	Type      string                     `json:"type"`
	SpanID    string                     `json:"spanId"`
	Data      map[string]json.RawMessage `json:"data"`
}

type journeyJobMatch struct {
	OrganizationID string
	CampaignID     string
	JobID          string
	Email          string
}

func (h *DeliveryHandler) stalwartEventsWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxStalwartTelemetryBody+1))
	if err != nil || len(raw) > maxStalwartTelemetryBody {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Telemetry request body is too large or could not be read.")
		return
	}
	if !verifyStalwartTelemetrySignature(h.webhookSecret, raw, r.Header.Get("X-Signature")) {
		httpx.Error(w, http.StatusUnauthorized, "invalid_signature", "Invalid telemetry signature.")
		return
	}
	var payload stalwartTelemetryPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Invalid Stalwart telemetry payload.")
		return
	}
	if len(payload.Events) > 1000 {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_many_events", "A telemetry request may contain at most 1000 events.")
		return
	}
	for _, event := range payload.Events {
		if err := h.applyStalwartTelemetryEvent(r, event); err != nil {
			h.logger.Error("apply Stalwart delivery event", "event_id", event.ID, "event_type", event.Type, "error", err)
			httpx.Error(w, http.StatusServiceUnavailable, "event_processing_failed", "Telemetry could not be processed; retry the signed request.")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) recordTrackingJourneyEvent(
	ctx context.Context,
	organizationID, jobID, eventType, sourceEventID, classification string,
) {
	metadata, err := json.Marshal(map[string]string{"engagement_classification": classification})
	if err != nil {
		s.logger.Warn("encode tracking journey event", "delivery_job_id", jobID, "error", err)
		return
	}
	if _, err := deliveryjourney.NewStore(s.pool).Record(ctx, deliveryjourney.Event{
		OrganizationID: organizationID,
		JobID:          jobID,
		Type:           eventType,
		Source:         "tracking",
		SourceEventID:  sourceEventID,
		OccurredAt:     time.Now().UTC(),
		Metadata:       metadata,
	}); err != nil {
		s.logger.Warn("record tracking journey event", "delivery_job_id", jobID, "event_type", eventType, "error", err)
	}
}

func (s *Server) getCampaignRecipientJourney(w http.ResponseWriter, r *http.Request) {
	organizationID := s.organizationID(r)
	campaignID := r.PathValue("id")
	recipientID := r.PathValue("recipientId")
	var status string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT journey_status
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid
		  AND campaign_id = $2::uuid
		  AND recipient_id = $3::uuid
	`, organizationID, campaignID, recipientID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "recipient_not_found", "Recipient journey not found.")
			return
		}
		s.logger.Error("load recipient journey status", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient journey.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT event_type, source, source_event_id, event_timestamp,
		       remote_mx_host, smtp_code, enhanced_code, reason,
		       attempt_number, next_retry_at, event_metadata
		FROM delivery_journey_events
		WHERE organization_id = $1::uuid
		  AND campaign_id = $2::uuid
		  AND recipient_id = $3::uuid
		ORDER BY event_timestamp, id
	`, organizationID, campaignID, recipientID)
	if err != nil {
		s.logger.Error("query recipient journey events", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient journey.")
		return
	}
	defer rows.Close()
	type journeyEvent struct {
		Stage         string          `json:"stage"`
		Source        string          `json:"source"`
		SourceEventID string          `json:"source_event_id,omitempty"`
		Timestamp     time.Time       `json:"timestamp"`
		RemoteMXHost  string          `json:"remote_mx_host,omitempty"`
		SMTPCode      *int            `json:"smtp_code,omitempty"`
		EnhancedCode  string          `json:"enhanced_code,omitempty"`
		Reason        string          `json:"reason,omitempty"`
		AttemptNumber *int            `json:"attempt_number,omitempty"`
		NextRetryAt   *time.Time      `json:"next_retry_at,omitempty"`
		Metadata      json.RawMessage `json:"metadata,omitempty"`
	}
	events := make([]journeyEvent, 0)
	for rows.Next() {
		var item journeyEvent
		var smtpCode, attempt pgtype.Int4
		var retryAt pgtype.Timestamptz
		if err := rows.Scan(
			&item.Stage, &item.Source, &item.SourceEventID, &item.Timestamp,
			&item.RemoteMXHost, &smtpCode, &item.EnhancedCode, &item.Reason,
			&attempt, &retryAt, &item.Metadata,
		); err != nil {
			s.logger.Error("scan recipient journey event", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient journey.")
			return
		}
		if smtpCode.Valid {
			value := int(smtpCode.Int32)
			item.SMTPCode = &value
		}
		if attempt.Valid {
			value := int(attempt.Int32)
			item.AttemptNumber = &value
		}
		if retryAt.Valid {
			value := retryAt.Time
			item.NextRetryAt = &value
		}
		events = append(events, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate recipient journey events", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient journey.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": status, "events": events})
}

func verifyStalwartTelemetrySignature(secret string, body []byte, signature string) bool {
	if secret == "" || signature == "" {
		return false
	}
	received, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(received, mac.Sum(nil))
}

func (h *DeliveryHandler) applyStalwartTelemetryEvent(r *http.Request, event stalwartTelemetryEvent) error {
	eventType, ok := mapStalwartJourneyType(event.Type, event.Data, event.CreatedAt)
	if !ok {
		return nil
	}
	if event.CreatedAt.IsZero() {
		return fmt.Errorf("Stalwart event %q has no creation timestamp", event.ID)
	}
	recipients := telemetryRecipients(event.Data)
	returnPath := telemetryString(event.Data, "from", "returnPath", "return_path")
	matches, err := h.resolveStalwartEventMatches(r, returnPath, recipients, event.SpanID, event.Data)
	if err != nil {
		return err
	}
	for _, match := range matches {
		attempt, hasAttempt := telemetryInt(event.Data, "attempt", "attemptNumber", "retryCount")
		journeyEvent := deliveryjourney.Event{
			OrganizationID: match.OrganizationID,
			JobID:          match.JobID,
			Type:           eventType,
			Source:         "stalwart",
			SourceEventID:  event.ID,
			OccurredAt:     event.CreatedAt,
			RemoteHost:     telemetryString(event.Data, "hostname", "remoteHost", "remote_mx_host"),
			SMTPCode:       telemetrySMTPCode(event.Data),
			EnhancedCode:   telemetryString(event.Data, "enhancedCode", "enhancedStatusCode", "enhanced_code"),
			Reason:         telemetryString(event.Data, "details", "reason", "response", "message"),
			NextRetryAt:    telemetryTime(event.Data, "retry", "nextRetry", "next_retry_at"),
			Metadata:       rawTelemetryMetadata(event.Data),
		}
		if hasAttempt {
			journeyEvent.Attempt = &attempt
		} else if event.Type == "delivery.attempt-start" {
			journeyEvent.AttemptStarted = true
		}
		if event.ID != "" {
			journeyEvent.EventHash = telemetryHash(event.ID, match.OrganizationID, match.JobID)
		}
		if _, err := deliveryjourney.NewStore(h.pool).Record(r.Context(), journeyEvent); err != nil {
			return err
		}
		if event.SpanID != "" {
			if _, err := h.pool.Exec(r.Context(), `
				INSERT INTO delivery_journey_stalwart_spans
					(organization_id, span_id, delivery_job_id, recipient_id, expires_at)
				SELECT $1::uuid, $2::text, crt.delivery_job_id, crt.recipient_id,
				       now() + interval '30 days'
				FROM campaign_recipient_tracking crt
				WHERE crt.organization_id = $1::uuid AND crt.delivery_job_id = $3::uuid
				  AND lower(crt.email) = lower($4::text)
				ON CONFLICT (organization_id, span_id, delivery_job_id, recipient_id)
				DO UPDATE SET expires_at = EXCLUDED.expires_at
			`, match.OrganizationID, event.SpanID, match.JobID, match.Email); err != nil {
				return fmt.Errorf("persist Stalwart span mapping: %w", err)
			}
		}
	}
	return nil
}

func mapStalwartJourneyType(eventType string, data map[string]json.RawMessage, occurredAt time.Time) (string, bool) {
	switch eventType {
	case "delivery.attempt-start", "delivery.attempt-end", "delivery.connect-error",
		"delivery.greeting-failed", "delivery.ehlo-rejected", "delivery.mail-from-rejected",
		"delivery.rcpt-to-rejected", "delivery.rcpt-to-failed", "delivery.message-rejected",
		"delivery.start-tls-error", "delivery.implicit-tls-error", "delivery.completed":
		return "relaying", true
	case "delivery.delivered", "delivery.dsn-success":
		return "accepted_by_recipient_server", true
	case "delivery.dsn-temp-fail", "queue.rescheduled":
		return "deferred_by_recipient", true
	case "delivery.dsn-perm-fail":
		return "bounced", true
	case "delivery.failed":
		if expired := telemetryString(data, "expired", "isExpired"); expired == "true" ||
			(func() bool {
				expires := telemetryTime(data, "expires", "expiresAt")
				return expires != nil && !expires.After(occurredAt)
			})() {
			return "expired", true
		}
		return "failed", true
	default:
		return "", false
	}
}

func (h *DeliveryHandler) resolveStalwartEventMatches(
	r *http.Request,
	returnPath string,
	recipients []string,
	spanID string,
	data map[string]json.RawMessage,
) ([]journeyJobMatch, error) {
	recipientSet := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		recipientSet[strings.ToLower(recipient)] = struct{}{}
	}
	matches := make([]journeyJobMatch, 0)
	if returnPath != "" {
		token, err := delivery.ParseVERPTokenAddress(h.bounceDomain, returnPath)
		if err == nil {
			var organizationID, campaignID, jobID, email string
			err = h.pool.QueryRow(r.Context(), `
				SELECT organization_id::text, campaign_id::text, delivery_job_id::text, email
				FROM delivery_verp_tokens
				WHERE token = $1::text
			`, token).Scan(&organizationID, &campaignID, &jobID, &email)
			if err == nil {
				claims := delivery.VERPClaims{WorkspaceID: organizationID, CampaignID: campaignID, Recipient: email}
				if delivery.VerifyVERPToken(h.bounceTokenSecret, token, claims) && recipientMatches(email, recipientSet) {
					matches = append(matches, journeyJobMatch{organizationID, campaignID, jobID, email})
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("resolve Stalwart VERP event: %w", err)
			}
		}
	}
	if len(matches) == 0 && spanID != "" {
		rows, err := h.pool.Query(r.Context(), `
			SELECT s.organization_id::text, crt.campaign_id::text,
			       s.delivery_job_id::text, crt.email
			FROM delivery_journey_stalwart_spans s
			JOIN campaign_recipient_tracking crt
			  ON crt.organization_id = s.organization_id
			 AND crt.delivery_job_id = s.delivery_job_id
			 AND crt.recipient_id = s.recipient_id
			WHERE s.span_id = $1::text AND s.expires_at > now()
			  AND ($2::text = '' OR lower(crt.email) = lower($2::text))
			ORDER BY s.organization_id, s.delivery_job_id
			LIMIT 100
		`, spanID, firstRecipient(recipients))
		if err != nil {
			return nil, fmt.Errorf("resolve Stalwart span event: %w", err)
		}
		for rows.Next() {
			var match journeyJobMatch
			if err := rows.Scan(&match.OrganizationID, &match.CampaignID, &match.JobID, &match.Email); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan Stalwart span event: %w", err)
			}
			matches = append(matches, match)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate Stalwart span event: %w", err)
		}
		rows.Close()
	}
	if len(matches) == 0 {
		messageID := telemetryString(data, "messageId", "messageID", "message_id", "transportMessageId")
		if messageID != "" {
			rows, err := h.pool.Query(r.Context(), `
				SELECT dj.organization_id::text, dj.campaign_id::text, dj.id::text, crt.email
				FROM delivery_jobs dj
				JOIN campaign_recipient_tracking crt
				  ON crt.organization_id = dj.organization_id
				 AND crt.delivery_job_id = dj.id
				WHERE (dj.message_id = $1::text OR dj.transport_message_id = $1::text)
				  AND ($2::text = '' OR lower(crt.email) = lower($2::text))
				ORDER BY CASE WHEN dj.transport_message_id = $1::text THEN 0 ELSE 1 END
				LIMIT 2
			`, messageID, firstRecipient(recipients))
			if err != nil {
				return nil, fmt.Errorf("resolve Stalwart Message-ID event: %w", err)
			}
			for rows.Next() {
				var match journeyJobMatch
				if err := rows.Scan(&match.OrganizationID, &match.CampaignID, &match.JobID, &match.Email); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan Stalwart Message-ID event: %w", err)
				}
				matches = append(matches, match)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("iterate Stalwart Message-ID event: %w", err)
			}
			rows.Close()
			if len(matches) > 1 {
				return nil, fmt.Errorf("Stalwart Message-ID is ambiguous")
			}
		}
	}
	return matches, nil
}

func recipientMatches(email string, recipients map[string]struct{}) bool {
	if len(recipients) == 0 {
		return true
	}
	_, ok := recipients[strings.ToLower(email)]
	return ok
}

func firstRecipient(recipients []string) string {
	if len(recipients) == 0 {
		return ""
	}
	return recipients[0]
}

func telemetryRecipients(data map[string]json.RawMessage) []string {
	raw := telemetryRaw(data, "to", "recipient", "recipients")
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		result := make([]string, 0, len(values))
		for _, value := range values {
			if address := parseMailAddress(value); address != "" {
				result = append(result, address)
			}
		}
		return result
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if address := parseMailAddress(value); address != "" {
			return []string{address}
		}
	}
	var object struct {
		Address string `json:"address"`
		Email   string `json:"email"`
	}
	if json.Unmarshal(raw, &object) == nil {
		if address := parseMailAddress(firstNonEmpty(object.Address, object.Email)); address != "" {
			return []string{address}
		}
	}
	var objects []struct {
		Address string `json:"address"`
		Email   string `json:"email"`
	}
	if json.Unmarshal(raw, &objects) == nil {
		result := make([]string, 0, len(objects))
		for _, item := range objects {
			if address := parseMailAddress(firstNonEmpty(item.Address, item.Email)); address != "" {
				result = append(result, address)
			}
		}
		return result
	}
	return nil
}

func parseMailAddress(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := mail.ParseAddress(value); err == nil {
		return parsed.Address
	}
	return ""
}

func telemetryRaw(data map[string]json.RawMessage, keys ...string) json.RawMessage {
	for key, value := range data {
		for _, wanted := range keys {
			if strings.EqualFold(key, wanted) {
				return value
			}
		}
	}
	return nil
}

func telemetryString(data map[string]json.RawMessage, keys ...string) string {
	raw := telemetryRaw(data, keys...)
	if len(raw) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	var boolean bool
	if json.Unmarshal(raw, &boolean) == nil {
		return strconv.FormatBool(boolean)
	}
	return ""
}

func telemetryInt(data map[string]json.RawMessage, keys ...string) (int, bool) {
	value, err := strconv.Atoi(telemetryString(data, keys...))
	return value, err == nil
}

func telemetrySMTPCode(data map[string]json.RawMessage) *int {
	value, ok := telemetryInt(data, "code", "smtpCode", "smtp_code", "responseCode")
	if !ok {
		return nil
	}
	return &value
}

func telemetryTime(data map[string]json.RawMessage, keys ...string) *time.Time {
	value := telemetryString(data, keys...)
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func rawTelemetryMetadata(data map[string]json.RawMessage) json.RawMessage {
	raw, err := json.Marshal(data)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func telemetryHash(eventID, organizationID, jobID string) string {
	mac := sha256.Sum256([]byte("stalwart:" + eventID + ":" + organizationID + ":" + jobID))
	return fmt.Sprintf("%x", mac[:])
}

func validJourneyStage(stage string) bool {
	switch stage {
	case "queued", "processing", "deferred_by_sendwise", "submitted_to_mail_server",
		"relaying", "deferred_by_recipient", "accepted_by_recipient_server",
		"bounced", "expired", "failed", "complained", "unsubscribed",
		"accepted_final_state_unknown":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
