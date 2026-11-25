package deliveryjourney

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMissingRecipientTracking = errors.New("delivery journey event has no tenant-scoped recipient tracking row")

type Event struct {
	OrganizationID string
	JobID          string
	Type           string
	Source         string
	SourceEventID  string
	EventHash      string
	OccurredAt     time.Time
	RemoteHost     string
	SMTPCode       *int
	EnhancedCode   string
	Reason         string
	Attempt        *int
	AttemptStarted bool
	NextRetryAt    *time.Time
	Metadata       json.RawMessage
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Record(ctx context.Context, event Event) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin delivery journey event: %w", err)
	}
	defer tx.Rollback(ctx)

	inserted, err := RecordTx(ctx, tx, event)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit delivery journey event: %w", err)
	}
	return inserted, nil
}

func RecordTx(ctx context.Context, tx pgx.Tx, event Event) (bool, error) {
	if !validType(event.Type) || !validSource(event.Source) {
		return false, fmt.Errorf("unsupported delivery journey event type or source")
	}
	if event.OrganizationID == "" || event.JobID == "" {
		return false, errors.New("delivery journey event requires organization and job IDs")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if len(event.Metadata) == 0 {
		event.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Metadata) {
		return false, errors.New("delivery journey event metadata must be valid JSON")
	}
	if event.EventHash == "" {
		event.EventHash = hashEvent(event)
	}

	var campaignID, recipientID, contactID, currentStatus string
	var currentAt *time.Time
	var currentAttempt int
	err := tx.QueryRow(ctx, `
		SELECT campaign_id::text, recipient_id::text, contact_id::text,
		       journey_status, journey_last_event_at, journey_attempts
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
		FOR UPDATE
	`, event.OrganizationID, event.JobID).Scan(
		&campaignID, &recipientID, &contactID, &currentStatus, &currentAt, &currentAttempt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrMissingRecipientTracking
		}
		return false, fmt.Errorf("load delivery journey recipient: %w", err)
	}
	if event.AttemptStarted {
		nextAttempt := currentAttempt + 1
		event.Attempt = &nextAttempt
	}

	var insertedID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO delivery_journey_events
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 event_type, source, source_event_id, event_timestamp, remote_mx_host,
			 smtp_code, enhanced_code, reason, attempt_number, next_retry_at,
			 event_metadata, event_hash)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
		        $6::text, $7::text, $8::text, $9::timestamptz, $10::text,
		        $11::integer, $12::text, $13::text, $14::integer,
		        $15::timestamptz, $16::jsonb, $17::text)
		ON CONFLICT (organization_id, event_hash) DO NOTHING
		RETURNING id
	`, event.OrganizationID, campaignID, recipientID, contactID, event.JobID,
		event.Type, event.Source, event.SourceEventID, event.OccurredAt,
		event.RemoteHost, event.SMTPCode, event.EnhancedCode, event.Reason,
		event.Attempt, event.NextRetryAt, []byte(event.Metadata), event.EventHash,
	).Scan(&insertedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("insert delivery journey event: %w", err)
	}

	attempt := currentAttempt
	if event.Attempt != nil && *event.Attempt > attempt {
		attempt = *event.Attempt
	}
	if event.Type == "opened" || event.Type == "clicked" {
		return true, nil
	}
	// Timeline-only events: append to history and refresh metadata, but do not
	// change the recipient's current journey_status to an event-only type.
	if event.Type == "picked_up" || event.Type == "smtp_attempt" {
		if _, err := tx.Exec(ctx, `
			UPDATE campaign_recipient_tracking
			SET journey_last_event_at = CASE
			        WHEN journey_last_event_at IS NULL OR $3::timestamptz >= journey_last_event_at
			        THEN $3::timestamptz ELSE journey_last_event_at END,
			    journey_next_retry_at = COALESCE($4::timestamptz, journey_next_retry_at),
			    journey_remote_host = CASE WHEN $5::text = '' THEN journey_remote_host ELSE $5::text END,
			    journey_smtp_code = COALESCE($6::integer, journey_smtp_code),
			    journey_enhanced_code = CASE WHEN $7::text = '' THEN journey_enhanced_code ELSE $7::text END,
			    journey_reason = CASE WHEN $8::text = '' THEN journey_reason ELSE $8::text END,
			    journey_attempts = GREATEST(journey_attempts, $9::integer),
			    journey_last_stalwart_event_at = CASE
			        WHEN $10::boolean THEN GREATEST(
			            COALESCE(journey_last_stalwart_event_at, $3::timestamptz),
			            $3::timestamptz
			        )
			        ELSE journey_last_stalwart_event_at
			    END,
			    updated_at = now()
			WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
		`, event.OrganizationID, event.JobID, event.OccurredAt, event.NextRetryAt,
			event.RemoteHost, event.SMTPCode, event.EnhancedCode, event.Reason,
			attempt, event.Source == "stalwart" || event.Source == "stalwart_webhook"); err != nil {
			return false, fmt.Errorf("update delivery journey timeline metadata: %w", err)
		}
		return true, nil
	}
	if shouldAdvance(currentStatus, currentAt, currentAttempt, event) {
		if _, err := tx.Exec(ctx, `
			UPDATE campaign_recipient_tracking
			SET journey_status = $3::text,
			    journey_last_event_at = $4::timestamptz,
			    journey_next_retry_at = $5::timestamptz,
			    journey_remote_host = CASE WHEN $6::text = '' THEN journey_remote_host ELSE $6::text END,
			    journey_smtp_code = COALESCE($7::integer, journey_smtp_code),
			    journey_enhanced_code = CASE WHEN $8::text = '' THEN journey_enhanced_code ELSE $8::text END,
			    journey_reason = CASE WHEN $9::text = '' THEN journey_reason ELSE $9::text END,
			    journey_attempts = $10::integer,
			    journey_last_stalwart_event_at = CASE
			        WHEN $11::boolean THEN GREATEST(
			            COALESCE(journey_last_stalwart_event_at, $4::timestamptz),
			            $4::timestamptz
			        )
			        ELSE journey_last_stalwart_event_at
			    END,
			    updated_at = now()
			WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
		`, event.OrganizationID, event.JobID, event.Type, event.OccurredAt,
			event.NextRetryAt, event.RemoteHost, event.SMTPCode, event.EnhancedCode,
			event.Reason, attempt, event.Source == "stalwart" || event.Source == "stalwart_webhook"); err != nil {
			return false, fmt.Errorf("advance delivery journey status: %w", err)
		}
	}
	return true, nil
}

func hashEvent(event Event) string {
	value := strings.Join([]string{
		event.OrganizationID, event.JobID, event.Source, event.SourceEventID,
		event.Type, event.OccurredAt.UTC().Format(time.RFC3339Nano),
	}, ":")
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func shouldAdvance(current string, currentAt *time.Time, currentAttempt int, next Event) bool {
	if next.Type == "bounced" && current == "accepted_by_recipient_server" {
		return currentAt == nil || !next.OccurredAt.Before(*currentAt)
	}
	if currentAt != nil && next.OccurredAt.Before(*currentAt) {
		return false
	}
	if next.Type == "accepted_by_recipient_server" && current == "accepted_final_state_unknown" {
		return true
	}
	if next.Type == "accepted_final_state_unknown" && current != "submitted_to_mail_server" &&
		current != "relaying" {
		return false
	}
	if next.Attempt != nil && *next.Attempt > currentAttempt {
		return rank(next.Type) > rank(current) ||
			(next.Type == "relaying" && (current == "deferred_by_recipient" || current == "submitted_to_mail_server" || current == "relaying"))
	}
	return rank(next.Type) > rank(current) || next.Type == current
}

func rank(status string) int {
	switch status {
	case "queued":
		return 1
	case "picked_up", "processing":
		return 2
	case "deferred_by_sendwise":
		return 3
	case "submitted_to_mail_server":
		return 4
	case "smtp_attempt", "relaying":
		return 5
	case "deferred_by_recipient":
		return 6
	case "accepted_by_recipient_server", "accepted_final_state_unknown":
		return 7
	case "bounced", "expired", "failed", "complained", "unsubscribed":
		return 8
	default:
		return 0
	}
}

func validType(eventType string) bool {
	switch eventType {
	case "queued", "picked_up", "processing", "deferred_by_sendwise", "submitted_to_mail_server",
		"smtp_attempt", "relaying", "deferred_by_recipient", "accepted_by_recipient_server",
		"bounced", "expired", "failed", "complained", "unsubscribed",
		"accepted_final_state_unknown", "opened", "clicked":
		return true
	default:
		return false
	}
}

func validSource(source string) bool {
	switch source {
	case "sendwise", "stalwart", "stalwart_webhook", "dsn", "reconciliation", "backfill", "tracking":
		return true
	default:
		return false
	}
}

func (s *Store) UpdateReconciled(ctx context.Context, organizationID, jobID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET journey_last_reconciled_at = now(),
		    updated_at = now()
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, organizationID, jobID)
	if err != nil {
		return fmt.Errorf("mark delivery journey reconciled: %w", err)
	}
	return nil
}
