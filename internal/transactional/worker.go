package transactional

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/quota"
	"golang.org/x/time/rate"
)

type DeliveryWorker struct {
	cfg       config.Config
	pool      *pgxpool.Pool
	store     *Store
	transport delivery.Transport
	logger    *slog.Logger
	limiter   *rate.Limiter
}

func NewDeliveryWorker(cfg config.Config, pool *pgxpool.Pool, transport delivery.Transport, logger *slog.Logger) *DeliveryWorker {
	if cfg.TransactionalRequestsPerSecond < 1 {
		cfg.TransactionalRequestsPerSecond = 1
	}
	burst := cfg.TransactionalRequestsPerSecond
	if burst > 100 {
		burst = 100
	}
	return &DeliveryWorker{
		cfg:       cfg,
		pool:      pool,
		store:     NewStore(pool),
		transport: transport,
		logger:    logger,
		limiter:   rate.NewLimiter(rate.Limit(cfg.TransactionalRequestsPerSecond), burst),
	}
}

func (w *DeliveryWorker) Run(ctx context.Context) error {
	if !w.cfg.TransactionalEnabled {
		return nil
	}
	if w.transport == nil {
		return errors.New("transactional delivery transport is not configured")
	}
	concurrency := w.cfg.TransactionalWorkerConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for index := 0; index < concurrency; index++ {
		go func() {
			defer workers.Done()
			w.runWorker(ctx)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	return nil
}

func (w *DeliveryWorker) runWorker(ctx context.Context) {
	pollInterval := 200 * time.Millisecond
	for ctx.Err() == nil {
		if err := w.limiter.Wait(ctx); err != nil {
			return
		}
		job, err := w.store.ClaimDue(ctx, 2*time.Minute)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Error("claim transactional message", "error", err)
				waitContext(ctx, pollInterval)
			}
			continue
		}
		if job == nil {
			waitContext(ctx, pollInterval)
			continue
		}
		if err := w.process(ctx, job); err != nil && ctx.Err() == nil {
			w.logger.Error("process transactional message",
				"message_id", job.MessageUUID,
				"recipient_id", job.RecipientID,
				"error", err,
			)
		}
	}
}

func (w *DeliveryWorker) process(ctx context.Context, job *Job) error {
	if !w.cfg.TransactionalEnabled {
		return w.reject(ctx, job, "transactional product is disabled")
	}
	if err := w.validateWorkspaceAndDomain(ctx, job); err != nil {
		if errors.Is(err, ErrWorkspacePaused) {
			return w.store.TransitionRecipient(
				ctx, job.RecipientID, StatusDeferred, "system", 0, "",
				err.Error(), time.Now().Add(time.Minute), nil,
			)
		}
		return w.reject(ctx, job, err.Error())
	}
	suppressed, err := w.store.IsSuppressed(ctx, job.Organization, job.To)
	if err != nil {
		return err
	}
	if suppressed {
		return w.store.TransitionRecipient(ctx, job.RecipientID, StatusSuppressed, "system", 0, "", "recipient is suppressed", time.Time{}, nil)
	}

	dkim, err := delivery.LoadDKIMConfig(ctx, w.pool, job.Organization, job.From, w.cfg.DKIMEncryptionKey)
	if err != nil {
		return fmt.Errorf("load transactional DKIM configuration: %w", err)
	}
	if dkim == nil {
		return w.reject(ctx, job, "verified sending domain has no DKIM signing key")
	}
	token, envelopeFrom, err := NewVERPForRecipient(
		w.cfg.JWTSecret, w.cfg.BounceDomain, job.Organization, job.MessageUUID, job.To,
	)
	if err != nil {
		return err
	}
	if err := w.store.SaveVERPToken(ctx, job, token, job.To); err != nil {
		return err
	}

	headers := make(map[string]string)
	if err := json.Unmarshal(job.Headers, &headers); err != nil {
		return fmt.Errorf("decode transactional headers: %w", err)
	}
	if job.ReplyTo != "" {
		headers["Reply-To"] = job.ReplyTo
	}
	headers["X-Sendwise-Message-Id"] = job.MessageUUID
	attachments, err := decodeAttachments(job.Attachments, w.cfg.TransactionalMaxMessageBytes)
	if err != nil {
		return w.reject(ctx, job, err.Error())
	}
	message := delivery.Message{
		ID:           job.MessageID,
		WorkspaceID:  job.Organization,
		From:         job.From,
		EnvelopeFrom: envelopeFrom,
		To:           job.To,
		Subject:      job.Subject,
		HTML:         job.HTML,
		Text:         job.Text,
		Headers:      headers,
		Attachments:  attachments,
		DKIM:         dkim,
		RequestDSN:   true,
	}

	if err := w.store.ReserveRecipientQuota(ctx, job, int64(w.cfg.PlatformMaxDomainRate)); err != nil {
		var exceeded *quota.ExceededError
		if errors.As(err, &exceeded) {
			return w.store.TransitionRecipient(
				ctx, job.RecipientID, StatusDeferred, "system", 0, "4.5.3",
				err.Error(), exceeded.ResetAt, map[string]any{"quota_window": exceeded.Window},
			)
		}
		if errors.Is(err, ErrSenderDomainNotAllowed) {
			return w.reject(ctx, job, err.Error())
		}
		return err
	}

	sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result, err := w.transport.Send(sendCtx, message)
	if err == nil && (result == nil || !result.Accepted) {
		err = errors.New("Stalwart did not confirm SMTP acceptance")
	}
	if err != nil {
		return w.handleSendFailure(ctx, job, err)
	}

	// SMTP acceptance is not final delivery. A real downstream delivery or
	// DSN event must update this message from "sending".
	return w.store.MarkAccepted(ctx, job, result.MessageID)
}

func (w *DeliveryWorker) validateWorkspaceAndDomain(ctx context.Context, job *Job) error {
	address, err := mail.ParseAddress(job.From)
	if err != nil {
		return fmt.Errorf("sender address is invalid")
	}
	domain := strings.ToLower(address.Address[strings.LastIndexByte(address.Address, '@')+1:])
	var organizationStatus, settingsStatus string
	var enabled bool
	err = w.pool.QueryRow(ctx, `
		SELECT o.status, ts.enabled, ts.status
		FROM transactional_settings ts
		JOIN organizations o ON o.id = ts.organization_id
		WHERE ts.organization_id = $1
	`, job.Organization).Scan(&organizationStatus, &enabled, &settingsStatus)
	if err != nil {
		return fmt.Errorf("validate transactional workspace and sending domain: %w", err)
	}
	if organizationStatus != "active" || settingsStatus == "paused" {
		return ErrWorkspacePaused
	}
	if !enabled || settingsStatus == "disabled" {
		return ErrWorkspaceDisabled
	}
	if settingsStatus != "active" {
		return ErrWorkspacePaused
	}
	var allowed bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sending_domains sd
			WHERE sd.organization_id = $1 AND lower(sd.domain) = $2
			  AND sd.transactional_enabled AND sd.status = 'verified'
			  AND sd.spf_status = 'verified' AND sd.dkim_status = 'verified'
			  AND NOT sd.owner_conflict AND sd.admin_disabled_at IS NULL
		)
	`, job.Organization, domain).Scan(&allowed); err != nil {
		return fmt.Errorf("validate transactional sender domain: %w", err)
	}
	if !allowed {
		return ErrSenderDomainNotAllowed
	}
	return nil
}

func (w *DeliveryWorker) reject(ctx context.Context, job *Job, reason string) error {
	return w.store.TransitionRecipient(ctx, job.RecipientID, StatusRejected, "system", 0, "", reason, time.Time{}, nil)
}

func (w *DeliveryWorker) handleSendFailure(ctx context.Context, job *Job, err error) error {
	failure := ClassifyFailure(err)
	response := err.Error()
	if len(response) > 1000 {
		response = response[:1000]
	}
	if !failure.Temporary {
		if failure.EnhancedCode == "5.1.1" {
			if err := w.store.AddSuppression(ctx, job.Organization, job.To, "hard_bounce", "smtp_rejection", false); err != nil {
				return err
			}
			if err := w.store.AddSuppression(ctx, "", job.To, "invalid", "smtp_rejection", true); err != nil {
				return err
			}
		}
		return w.store.TransitionRecipient(
			ctx, job.RecipientID, StatusFailed, "smtp", failure.SMTPCode,
			failure.EnhancedCode, response, time.Time{},
			map[string]any{"permanent": true},
		)
	}
	retryLimit := time.Duration(w.cfg.TransactionalRetryHours) * time.Hour
	if retryLimit <= 0 {
		retryLimit = 24 * time.Hour
	}
	nextAttempt := time.Now().Add(RetryDelay(job.Attempts))
	if time.Since(job.CreatedAt) >= retryLimit || nextAttempt.Sub(job.CreatedAt) > retryLimit {
		return w.store.TransitionRecipient(
			ctx, job.RecipientID, StatusFailed, "retry", failure.SMTPCode,
			failure.EnhancedCode, response, time.Time{},
			map[string]any{"retry_limit_exceeded": true},
		)
	}
	return w.store.TransitionRecipient(
		ctx, job.RecipientID, StatusDeferred, "retry", failure.SMTPCode,
		failure.EnhancedCode, response, nextAttempt,
		map[string]any{"retry_at": nextAttempt.UTC().Format(time.RFC3339Nano)},
	)
}

func decodeAttachments(raw []byte, maxBytes int64) ([]delivery.Attachment, error) {
	var input []Attachment
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("decode transactional attachments: %w", err)
	}
	attachments := make([]delivery.Attachment, 0, len(input))
	var size int64
	for _, attachment := range input {
		content, err := base64.StdEncoding.DecodeString(attachment.Content)
		if err != nil {
			return nil, fmt.Errorf("decode attachment %q: invalid base64", attachment.Filename)
		}
		size += int64(len(content))
		if maxBytes > 0 && size > maxBytes {
			return nil, fmt.Errorf("decoded attachments exceed the maximum size of %d bytes", maxBytes)
		}
		attachments = append(attachments, delivery.Attachment{
			Filename: attachment.Filename, ContentType: attachment.ContentType, Content: content,
		})
	}
	return attachments, nil
}

func waitContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
