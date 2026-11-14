package transactional

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/delivery"
	xhtml "golang.org/x/net/html"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type EnqueueOptions struct {
	OrganizationID string
	APIKeyID       string
	IdempotencyKey string
	Mode           bool
	EnforceLimits  bool
}

type Job struct {
	RecipientID   string
	MessageUUID   string
	Organization  string
	APIKeyID      string
	MessageID     string
	From          string
	ReplyTo       string
	To            string
	Subject       string
	HTML          string
	Text          string
	Headers       []byte
	Attachments   []byte
	RecipientType string
	Attempts      int
	CreatedAt     time.Time
}

func (s *Store) Enqueue(ctx context.Context, options EnqueueOptions, input MessageInput) (MessageReceipt, error) {
	var receipt MessageReceipt
	if strings.TrimSpace(options.OrganizationID) == "" {
		return receipt, errors.New("organization id is required")
	}
	if options.IdempotencyKey != "" &&
		(len(options.IdempotencyKey) > 200 || strings.ContainsAny(options.IdempotencyKey, "\r\n")) {
		return receipt, errors.New("idempotency key is invalid")
	}
	if options.IdempotencyKey != "" {
		var existingID, existingMessageID string
		err := s.pool.QueryRow(ctx, `
			SELECT id::text, message_id, status
			FROM transactional_messages
			WHERE organization_id = $1 AND idempotency_key = $2
		`, options.OrganizationID, options.IdempotencyKey).Scan(
			&existingID, &existingMessageID, &receipt.Status,
		)
		if err == nil {
			receipt.ID = existingID
			receipt.MessageID = "<" + existingMessageID + ">"
			receipt.Duplicate = true
			return receipt, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return receipt, fmt.Errorf("check transactional idempotency key: %w", err)
		}
	}
	var templateID any
	var templateVersion any
	if input.TemplateID != "" {
		id, err := uuid.Parse(input.TemplateID)
		if err != nil {
			return receipt, errors.New("template_id must be a valid template ID")
		}
		if input.Subject != "" || input.HTML != "" || input.Text != "" {
			return receipt, errors.New("template messages cannot include inline subject or body content")
		}
		var version int
		var source Template
		err = s.pool.QueryRow(ctx, `
			SELECT t.current_version, v.subject, v.html, v.text_content
			FROM transactional_templates t
			JOIN transactional_template_versions v
			  ON v.template_id = t.id AND v.version = t.current_version
			WHERE t.id = $1 AND t.organization_id = $2
		`, id.String(), options.OrganizationID).Scan(
			&version, &source.Subject, &source.HTML, &source.Text,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return receipt, errors.New("transactional template was not found")
		}
		if err != nil {
			return receipt, fmt.Errorf("load transactional template: %w", err)
		}
		rendered, err := RenderTemplate(source, input.Variables)
		if err != nil {
			return receipt, fmt.Errorf("render transactional template: %w", err)
		}
		input.Subject, input.HTML, input.Text = rendered.Subject, rendered.HTML, rendered.Text
		templateID, templateVersion = id.String(), version
		input.Variables = nil
	} else if len(input.Variables) > 0 {
		return receipt, errors.New("variables require a template_id")
	}
	recipients, err := ValidateMessage(input, int(^uint(0)>>1), 0)
	if err != nil {
		return receipt, err
	}
	from, err := ValidateEmail(input.From)
	if err != nil {
		return receipt, err
	}
	if input.Text == "" {
		input.Text = PlainTextFromHTML(input.HTML)
	}
	if input.Text == "" {
		return receipt, errors.New("message must include a plain-text alternative")
	}

	headers, err := json.Marshal(input.Headers)
	if err != nil {
		return receipt, fmt.Errorf("encode transactional headers: %w", err)
	}
	if len(input.Headers) == 0 {
		headers = []byte("{}")
	}
	attachments, err := json.Marshal(input.Attachments)
	if err != nil {
		return receipt, fmt.Errorf("encode transactional attachments: %w", err)
	}
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil {
		return receipt, fmt.Errorf("encode transactional metadata: %w", err)
	}
	if len(input.Metadata) == 0 {
		metadata = []byte("{}")
	}
	if input.Attachments == nil {
		input.Attachments = []Attachment{}
	}
	messageUUID := uuid.New()
	fromAddress, _ := mail.ParseAddress(from)
	senderDomain := strings.ToLower(fromAddress.Address[strings.LastIndexByte(fromAddress.Address, '@')+1:])
	messageID := messageUUID.String() + "@" + senderDomain

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return receipt, fmt.Errorf("begin transactional enqueue: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, options.OrganizationID); err != nil {
		return receipt, fmt.Errorf("lock transactional workspace enqueue: %w", err)
	}

	if options.IdempotencyKey != "" {
		var existingID, existingMessageID string
		err := tx.QueryRow(ctx, `
			SELECT id::text, message_id, status
			FROM transactional_messages
			WHERE organization_id = $1 AND idempotency_key = $2
		`, options.OrganizationID, options.IdempotencyKey).Scan(
			&existingID, &existingMessageID, &receipt.Status,
		)
		if err == nil {
			receipt.ID = existingID
			receipt.MessageID = "<" + existingMessageID + ">"
			receipt.Duplicate = true
			if err := tx.Commit(ctx); err != nil {
				return MessageReceipt{}, fmt.Errorf("commit duplicate transactional enqueue: %w", err)
			}
			return receipt, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return receipt, fmt.Errorf("check transactional idempotency key: %w", err)
		}
	}

	if options.EnforceLimits {
		var workspaceStatus string
		if err := tx.QueryRow(ctx, `
			SELECT status FROM organizations WHERE id = $1 FOR SHARE
		`, options.OrganizationID).Scan(&workspaceStatus); err != nil {
			return receipt, fmt.Errorf("load transactional organization status: %w", err)
		}
		if workspaceStatus != "active" {
			return receipt, ErrWorkspacePaused
		}
		var enabled bool
		var status string
		var dailyLimit, monthlyLimit, maxRecipients int
		var maxMessageBytes int64
		if err := tx.QueryRow(ctx, `
			SELECT enabled, status, daily_limit, monthly_limit, max_recipients, max_message_bytes
			FROM transactional_settings
			WHERE organization_id = $1
			FOR UPDATE
		`, options.OrganizationID).Scan(
			&enabled, &status, &dailyLimit, &monthlyLimit, &maxRecipients, &maxMessageBytes,
		); err != nil {
			return receipt, fmt.Errorf("load transactional workspace limits: %w", err)
		}
		if !enabled || status == "disabled" {
			return receipt, ErrWorkspaceDisabled
		}
		if status != "active" {
			return receipt, ErrWorkspacePaused
		}
		recipients, err = ValidateMessage(input, maxRecipients, maxMessageBytes)
		if err != nil {
			return receipt, err
		}
		if !options.Mode {
			var dailyUsage, monthlyUsage int64
			if err := tx.QueryRow(ctx, `
				SELECT
					count(*) FILTER (WHERE m.created_at >= current_date),
					count(*) FILTER (WHERE m.created_at >= date_trunc('month', now()))
				FROM transactional_message_recipients r
				JOIN transactional_messages m ON m.id = r.message_id
				WHERE m.organization_id = $1 AND m.status <> 'rejected'
			`, options.OrganizationID).Scan(&dailyUsage, &monthlyUsage); err != nil {
				return receipt, fmt.Errorf("check transactional recipient usage: %w", err)
			}
			if dailyUsage+int64(len(recipients)) > int64(dailyLimit) {
				return receipt, ErrDailyLimit
			}
			if monthlyUsage+int64(len(recipients)) > int64(monthlyLimit) {
				return receipt, ErrMonthlyLimit
			}
		}
	}

	var apiKeyID any
	if options.APIKeyID != "" {
		apiKeyID = options.APIKeyID
	}
	var insertedID, insertedMessageID string
	err = tx.QueryRow(ctx, `
		INSERT INTO transactional_messages
			(id, organization_id, api_key_id, template_id, template_version,
			 idempotency_key, message_id, from_email,
			 reply_to, subject, html, text_content, headers, attachments, tags, metadata)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (organization_id, idempotency_key)
			WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING id::text, message_id, status
	`, messageUUID, options.OrganizationID, apiKeyID, templateID, templateVersion,
		options.IdempotencyKey, messageID,
		from, input.ReplyTo, input.Subject, input.HTML, input.Text, headers, attachments, tags, metadata,
	).Scan(&insertedID, &insertedMessageID, &receipt.Status)

	if errors.Is(err, pgx.ErrNoRows) && options.IdempotencyKey != "" {
		err = tx.QueryRow(ctx, `
			SELECT id::text, message_id, status
			FROM transactional_messages
			WHERE organization_id = $1 AND idempotency_key = $2
		`, options.OrganizationID, options.IdempotencyKey).Scan(&insertedID, &insertedMessageID, &receipt.Status)
		if err == nil {
			receipt.ID = insertedID
			receipt.MessageID = "<" + insertedMessageID + ">"
			receipt.Duplicate = true
			if err := tx.Commit(ctx); err != nil {
				return MessageReceipt{}, fmt.Errorf("commit duplicate transactional enqueue: %w", err)
			}
			return receipt, nil
		}
	}
	if err != nil {
		return MessageReceipt{}, fmt.Errorf("insert transactional message: %w", err)
	}

	for _, recipient := range recipients {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transactional_message_recipients (message_id, email, recipient_type)
			VALUES ($1, $2, $3)
		`, messageUUID, recipient.Email, recipient.Type); err != nil {
			return MessageReceipt{}, fmt.Errorf("insert transactional recipient: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_message_events
			(organization_id, message_id, event_type, source, metadata)
		VALUES ($1, $2, 'message.queued', 'api', $3)
	`, options.OrganizationID, messageUUID, metadata); err != nil {
		return MessageReceipt{}, fmt.Errorf("record transactional enqueue event: %w", err)
	}
	if options.Mode {
		if _, err := tx.Exec(ctx, `
			UPDATE transactional_messages
			SET status = 'rejected', updated_at = now()
			WHERE id = $1
		`, messageUUID); err != nil {
			return MessageReceipt{}, fmt.Errorf("mark test-mode message: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE transactional_message_recipients
			SET status = 'rejected', updated_at = now()
			WHERE message_id = $1
		`, messageUUID); err != nil {
			return MessageReceipt{}, fmt.Errorf("mark test-mode recipients: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO transactional_message_events
				(organization_id, message_id, event_type, source, metadata)
			VALUES ($1, $2, 'message.test_mode', 'api', '{"delivery_skipped":true}'::jsonb)
		`, options.OrganizationID, messageUUID); err != nil {
			return MessageReceipt{}, fmt.Errorf("record test-mode event: %w", err)
		}
		receipt.Status = StatusRejected
	}
	if err := tx.Commit(ctx); err != nil {
		return MessageReceipt{}, fmt.Errorf("commit transactional enqueue: %w", err)
	}

	receipt.ID = insertedID
	receipt.MessageID = "<" + insertedMessageID + ">"
	if receipt.Status == "" {
		receipt.Status = StatusQueued
	}
	return receipt, nil
}

func (s *Store) ClaimDue(ctx context.Context, lease time.Duration) (*Job, error) {
	if lease <= 0 {
		return nil, errors.New("transactional lease must be positive")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transactional claim: %w", err)
	}
	defer tx.Rollback(ctx)

	var job Job
	err = tx.QueryRow(ctx, `
		SELECT r.id::text, m.id::text, m.organization_id::text, COALESCE(m.api_key_id::text, ''),
		       m.message_id, m.from_email, m.reply_to, r.email, m.subject, m.html, m.text_content,
		       m.headers, m.attachments, r.recipient_type, r.attempts + 1, m.created_at
		FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE (
			(r.status IN ('queued', 'deferred') AND r.next_attempt_at <= now())
			OR (r.status = 'sending' AND r.accepted_at IS NULL AND r.lease_expires_at < now())
		)
		ORDER BY r.next_attempt_at, r.created_at, r.id
		FOR UPDATE OF r SKIP LOCKED
		LIMIT 1
	`).Scan(
		&job.RecipientID, &job.MessageUUID, &job.Organization, &job.APIKeyID,
		&job.MessageID, &job.From, &job.ReplyTo, &job.To, &job.Subject, &job.HTML, &job.Text,
		&job.Headers, &job.Attachments, &job.RecipientType, &job.Attempts, &job.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select transactional recipient: %w", err)
	}
	leaseText := fmt.Sprintf("%.6f seconds", lease.Seconds())
	if _, err := tx.Exec(ctx, `
		UPDATE transactional_message_recipients
		SET status = 'sending', attempts = attempts + 1,
		    lease_expires_at = now() + $2::interval, updated_at = now()
		WHERE id = $1
	`, job.RecipientID, leaseText); err != nil {
		return nil, fmt.Errorf("lease transactional recipient: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transactional_messages SET status = 'sending', updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'deferred', 'sending')
	`, job.MessageUUID); err != nil {
		return nil, fmt.Errorf("mark transactional message sending: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_message_events
			(organization_id, message_id, recipient_id, event_type, source, metadata)
		VALUES ($1, $2, $3, 'message.sending', 'system', jsonb_build_object('attempt', $4::integer))
	`, job.Organization, job.MessageUUID, job.RecipientID, job.Attempts); err != nil {
		return nil, fmt.Errorf("record transactional claim event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transactional claim: %w", err)
	}
	return &job, nil
}

func (s *Store) SaveVERPToken(ctx context.Context, job *Job, token, email string) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO transactional_verp_tokens
			(token, organization_id, message_id, recipient_id, email)
		VALUES ($1, $2, $3, $4, lower($5))
		ON CONFLICT (token) DO NOTHING
	`, token, job.Organization, job.MessageUUID, job.RecipientID, email)
	if err != nil {
		return fmt.Errorf("store transactional VERP token: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var same bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM transactional_verp_tokens
			WHERE token = $1 AND organization_id = $2 AND message_id = $3
			  AND recipient_id = $4 AND lower(email) = lower($5)
		)
	`, token, job.Organization, job.MessageUUID, job.RecipientID, email).Scan(&same); err != nil {
		return fmt.Errorf("verify transactional VERP token: %w", err)
	}
	if !same {
		return errors.New("transactional VERP token conflicts with a different recipient")
	}
	return nil
}

func (s *Store) MarkAccepted(ctx context.Context, job *Job, smtpMessageID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transactional acceptance: %w", err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE transactional_message_recipients
		SET status = 'sending', accepted_at = COALESCE(accepted_at, now()),
		    lease_expires_at = NULL, smtp_response = $2, updated_at = now()
		WHERE id = $1 AND status = 'sending' AND accepted_at IS NULL
	`, job.RecipientID, smtpMessageID)
	if err != nil {
		return fmt.Errorf("mark transactional SMTP acceptance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transactional_messages
		SET accepted_at = COALESCE(accepted_at, now()), status = 'sending', updated_at = now()
		WHERE id = $1
	`, job.MessageUUID); err != nil {
		return fmt.Errorf("mark transactional message accepted by MTA: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_message_events
			(organization_id, message_id, recipient_id, event_type, source, smtp_response)
		VALUES ($1, $2, $3, 'message.sent', 'smtp', $4)
	`, job.Organization, job.MessageUUID, job.RecipientID, smtpMessageID); err != nil {
		return fmt.Errorf("record transactional SMTP acceptance event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_usage_daily (organization_id, usage_date, accepted_count)
		VALUES ($1, current_date, 1)
		ON CONFLICT (organization_id, usage_date)
		DO UPDATE SET accepted_count = transactional_usage_daily.accepted_count + 1,
		              updated_at = now()
	`, job.Organization); err != nil {
		return fmt.Errorf("increment transactional accepted usage: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transactional SMTP acceptance: %w", err)
	}
	return nil
}

func (s *Store) ApplyFeedback(
	ctx context.Context,
	eventHash string,
	claims delivery.VERPClaims,
	feedback delivery.Feedback,
) error {
	messageID, ok := strings.CutPrefix(claims.CampaignID, "transactional:")
	if !ok || messageID == "" || eventHash == "" {
		return errors.New("transactional feedback scope is invalid")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transactional feedback: %w", err)
	}
	defer tx.Rollback(ctx)

	var organizationID, recipientID, recipientEmail string
	var current Status
	err = tx.QueryRow(ctx, `
		SELECT m.organization_id::text, r.id::text, r.email, r.status
		FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE m.id = $1 AND m.organization_id = $2 AND lower(r.email) = lower($3)
		FOR UPDATE OF r
	`, messageID, claims.WorkspaceID, claims.Recipient).Scan(
		&organizationID, &recipientID, &recipientEmail, &current,
	)
	if err != nil {
		return fmt.Errorf("find transactional feedback recipient: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO transactional_feedback_ingest
			(event_hash, organization_id, message_id, recipient_id, event_type, enhanced_code, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (event_hash) DO NOTHING
	`, eventHash, organizationID, messageID, recipientID, feedback.Type, feedback.EnhancedCode, feedback.Diagnostic)
	if err != nil {
		return fmt.Errorf("persist transactional feedback: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	status := Status("")
	eventType := "message.delivery_delayed"
	switch feedback.Type {
	case delivery.FeedbackDelivered:
		status, eventType = StatusDelivered, "message.delivered"
	case delivery.FeedbackHardBounce:
		status, eventType = StatusBounced, "message.bounced"
	case delivery.FeedbackComplaint:
		status, eventType = StatusComplained, "message.complained"
	case delivery.FeedbackSoftBounce:
	default:
		return fmt.Errorf("unsupported transactional feedback type %q", feedback.Type)
	}

	if status != "" && current != status {
		allowComplaintAfterDelivery := current == StatusDelivered && status == StatusComplained
		if !CanTransition(current, status) && !allowComplaintAfterDelivery {
			return fmt.Errorf("transactional feedback cannot transition recipient %q to %q", current, status)
		}
		tag, err = tx.Exec(ctx, `
			UPDATE transactional_message_recipients
			SET status = $2, enhanced_code = $3, smtp_response = $4,
			    lease_expires_at = NULL, updated_at = now()
			WHERE id = $1
		`, recipientID, status, feedback.EnhancedCode, feedback.Diagnostic)
		if err != nil {
			return fmt.Errorf("update recipient from transactional feedback: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return errors.New("transactional feedback recipient disappeared")
		}
	}

	metadata, err := json.Marshal(map[string]string{
		"action":        feedback.Action,
		"feedback_type": string(feedback.Type),
	})
	if err != nil {
		return fmt.Errorf("encode transactional feedback metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_message_events
			(organization_id, message_id, recipient_id, event_type, source, enhanced_code,
			 smtp_response, metadata)
		VALUES ($1, $2, $3, $4, 'stalwart', $5, $6, $7)
	`, organizationID, messageID, recipientID, eventType,
		feedback.EnhancedCode, feedback.Diagnostic, metadata); err != nil {
		return fmt.Errorf("record transactional feedback event: %w", err)
	}

	suppressionReason := ""
	switch feedback.Type {
	case delivery.FeedbackHardBounce:
		suppressionReason = "hard_bounce"
	case delivery.FeedbackComplaint:
		suppressionReason = "complaint"
	}
	if suppressionReason != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transactional_suppressions (organization_id, email, reason, source, confirmed)
			VALUES ($1, lower($2), $3, 'stalwart_feedback', true)
			ON CONFLICT (organization_id, lower(email))
				WHERE organization_id IS NOT NULL DO NOTHING
		`, organizationID, recipientEmail, suppressionReason); err != nil {
			return fmt.Errorf("suppress transactional feedback recipient: %w", err)
		}
		if feedback.Type == delivery.FeedbackHardBounce && feedback.EnhancedCode == "5.1.1" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO transactional_suppressions (organization_id, email, reason, source, confirmed)
				VALUES (NULL, lower($1), 'invalid', 'stalwart_feedback', true)
				ON CONFLICT (lower(email)) WHERE organization_id IS NULL DO NOTHING
			`, recipientEmail); err != nil {
				return fmt.Errorf("globally suppress invalid transactional recipient: %w", err)
			}
		}
	}

	if status != "" && current != status {
		if err := updateAggregateStatus(ctx, tx, messageID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO transactional_usage_daily (organization_id, usage_date, delivered_count, bounced_count, complained_count)
			VALUES ($1, current_date, $2, $3, $4)
			ON CONFLICT (organization_id, usage_date)
			DO UPDATE SET
				delivered_count = transactional_usage_daily.delivered_count + EXCLUDED.delivered_count,
				bounced_count = transactional_usage_daily.bounced_count + EXCLUDED.bounced_count,
				complained_count = transactional_usage_daily.complained_count + EXCLUDED.complained_count,
				updated_at = now()
		`, organizationID,
			boolCount(status == StatusDelivered),
			boolCount(status == StatusBounced),
			boolCount(status == StatusComplained),
		); err != nil {
			return fmt.Errorf("update transactional feedback usage: %w", err)
		}
		if feedback.Type == delivery.FeedbackHardBounce || feedback.Type == delivery.FeedbackComplaint {
			if err := maybePauseForReputation(ctx, tx, organizationID); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transactional feedback: %w", err)
	}
	return nil
}

func maybePauseForReputation(ctx context.Context, tx pgx.Tx, organizationID string) error {
	var bounceRateLimit, complaintRateLimit float64
	var minimumSampleSize int
	if err := tx.QueryRow(ctx, `
		SELECT max_bounce_rate, max_complaint_rate, minimum_sample_size
		FROM transactional_settings WHERE organization_id = $1
	`, organizationID).Scan(&bounceRateLimit, &complaintRateLimit, &minimumSampleSize); err != nil {
		return fmt.Errorf("load transactional reputation limits: %w", err)
	}

	var bounced, complained, total int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE r.status = 'bounced'),
		       count(*) FILTER (WHERE r.status = 'complained'),
		       count(*) FILTER (WHERE r.status IN ('delivered', 'bounced', 'complained'))
		FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE m.organization_id = $1 AND m.created_at >= now() - interval '30 days'
	`, organizationID).Scan(&bounced, &complained, &total); err != nil {
		return fmt.Errorf("calculate transactional reputation rates: %w", err)
	}
	if total < minimumSampleSize {
		return nil
	}

	reason := ""
	if float64(bounced)/float64(total) > bounceRateLimit {
		reason = "automatically paused: transactional bounce rate exceeded its limit"
	} else if float64(complained)/float64(total) > complaintRateLimit {
		reason = "automatically paused: transactional complaint rate exceeded its limit"
	}
	if reason == "" {
		return nil
	}
	tag, err := tx.Exec(ctx, `
		UPDATE transactional_settings
		SET status = 'paused', pause_reason = $2, updated_at = now()
		WHERE organization_id = $1 AND enabled AND status = 'active'
	`, organizationID, reason)
	if err != nil {
		return fmt.Errorf("pause transactional workspace for reputation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_audit_events (organization_id, event_type, metadata)
		VALUES ($1, 'transactional.automatically_paused', jsonb_build_object(
			'reason', $2::text, 'sample_size', $3::integer,
			'bounce_rate', $4::double precision, 'complaint_rate', $5::double precision))
	`, organizationID, reason, total, float64(bounced)/float64(total), float64(complained)/float64(total)); err != nil {
		return fmt.Errorf("record transactional auto-pause audit event: %w", err)
	}
	return nil
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) TransitionRecipient(
	ctx context.Context,
	recipientID string,
	to Status,
	source string,
	smtpCode int,
	enhancedCode, response string,
	nextAttempt time.Time,
	metadata map[string]any,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transactional state transition: %w", err)
	}
	defer tx.Rollback(ctx)

	var organizationID, messageID string
	var current Status
	err = tx.QueryRow(ctx, `
		SELECT m.organization_id::text, m.id::text, r.status
		FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE r.id = $1
		FOR UPDATE OF r
	`, recipientID).Scan(&organizationID, &messageID, &current)
	if err != nil {
		return fmt.Errorf("load transactional recipient state: %w", err)
	}
	if !CanTransition(current, to) {
		return fmt.Errorf("invalid transactional message transition %q -> %q", current, to)
	}
	if current == to {
		return tx.Commit(ctx)
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode transactional event metadata: %w", err)
	}
	var next any
	if !nextAttempt.IsZero() {
		next = nextAttempt
	}
	tag, err := tx.Exec(ctx, `
		UPDATE transactional_message_recipients
		SET status = $2, smtp_code = NULLIF($3, 0), enhanced_code = $4,
		    smtp_response = $5, next_attempt_at = COALESCE($6, next_attempt_at),
		    lease_expires_at = NULL, updated_at = now()
		WHERE id = $1
	`, recipientID, to, smtpCode, enhancedCode, response, next)
	if err != nil {
		return fmt.Errorf("update transactional recipient state: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("transactional recipient disappeared during state transition")
	}
	eventType := "message." + string(to)
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_message_events
			(organization_id, message_id, recipient_id, event_type, source, smtp_code,
			 enhanced_code, smtp_response, metadata)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, 0), $7, $8, $9)
	`, organizationID, messageID, recipientID, eventType, source, smtpCode, enhancedCode, response, encodedMetadata); err != nil {
		return fmt.Errorf("record transactional state event: %w", err)
	}
	if err := updateAggregateStatus(ctx, tx, messageID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transactional state transition: %w", err)
	}
	return nil
}

func (s *Store) IsSuppressed(ctx context.Context, organizationID, email string) (bool, error) {
	var suppressed bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM transactional_suppressions
			WHERE lower(email) = lower($2)
			  AND (organization_id = $1 OR organization_id IS NULL)
		)
	`, organizationID, email).Scan(&suppressed)
	if err != nil {
		return false, fmt.Errorf("check transactional suppression: %w", err)
	}
	return suppressed, nil
}

func (s *Store) AddSuppression(ctx context.Context, organizationID, email, reason, source string, confirmed bool) error {
	normalized, err := ValidateEmail(email)
	if err != nil {
		return err
	}
	if reason != "hard_bounce" && reason != "complaint" && reason != "manual" && reason != "invalid" {
		return fmt.Errorf("unsupported transactional suppression reason %q", reason)
	}
	var organization any
	if organizationID != "" {
		organization = organizationID
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO transactional_suppressions (organization_id, email, reason, source, confirmed)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING
	`, organization, normalized, reason, source, confirmed)
	if err != nil {
		return fmt.Errorf("insert transactional suppression: %w", err)
	}
	return nil
}

func updateAggregateStatus(ctx context.Context, tx pgx.Tx, messageID string) error {
	var status Status
	err := tx.QueryRow(ctx, `
		SELECT CASE
			WHEN count(*) FILTER (WHERE status IN ('queued', 'sending')) > 0 THEN
				CASE WHEN count(*) FILTER (WHERE status = 'sending') > 0 THEN 'sending' ELSE 'queued' END
			WHEN count(*) FILTER (WHERE status = 'deferred') > 0 THEN 'deferred'
			WHEN count(*) FILTER (WHERE status = 'complained') > 0 THEN 'complained'
			WHEN count(*) FILTER (WHERE status = 'bounced') > 0 THEN 'bounced'
			WHEN count(*) FILTER (WHERE status = 'failed') > 0 THEN 'failed'
			WHEN count(*) FILTER (WHERE status = 'rejected') > 0 THEN 'rejected'
			WHEN count(*) FILTER (WHERE status = 'suppressed') > 0 THEN 'suppressed'
			ELSE 'delivered'
		END
		FROM transactional_message_recipients
		WHERE message_id = $1
	`, messageID).Scan(&status)
	if err != nil {
		return fmt.Errorf("aggregate transactional recipient states: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transactional_messages
		SET status = $2,
		    delivered_at = CASE WHEN $2 = 'delivered' THEN COALESCE(delivered_at, now()) ELSE delivered_at END,
		    updated_at = now()
		WHERE id = $1
	`, messageID, status); err != nil {
		return fmt.Errorf("update aggregate transactional message state: %w", err)
	}
	return nil
}

func PlainTextFromHTML(source string) string {
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}
	var text strings.Builder
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		if node.Type == xhtml.TextNode {
			text.WriteString(node.Data)
			text.WriteByte(' ')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return strings.Join(strings.Fields(text.String()), " ")
}

func NewVERPForRecipient(secret, bounceDomain, organizationID, messageID, email string) (string, string, error) {
	token, err := delivery.NewVERPToken(secret, delivery.VERPClaims{
		WorkspaceID: organizationID,
		CampaignID:  "transactional:" + messageID,
		Recipient:   email,
	})
	if err != nil {
		return "", "", fmt.Errorf("create transactional VERP token: %w", err)
	}
	address, err := delivery.FormatVERPReturnPath(bounceDomain, token)
	if err != nil {
		return "", "", fmt.Errorf("format transactional VERP address: %w", err)
	}
	return token, address, nil
}
