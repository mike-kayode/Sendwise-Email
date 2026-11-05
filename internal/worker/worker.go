package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/messaging"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/nats-io/nats.go/jetstream"
	"golang.org/x/time/rate"
)

type Worker struct {
	cfg       config.Config
	pool      *pgxpool.Pool
	bus       *messaging.Bus
	transport delivery.Transport
	renderer  *delivery.Renderer
	tokens    *auth.Manager
	campaigns *campaigns.Service
	logger    *slog.Logger
	limiter   *rate.Limiter

	domainMu       sync.Mutex
	domainInFlight map[string]int
}

type jobRow struct {
	ID             string
	OrganizationID string
	CampaignID     string
	ContactID      string
	MessageID      string
	Status         string
	Attempts       int
	MaxAttempts    int
	Email          string
	FirstName      string
	LastName       string
	ContactStatus  string
	CustomFields   []byte
	Subject        string
	PreviewText    string
	SenderName     string
	SenderEmail    string
	ReplyTo        string
	Content        []byte
	CampaignStatus string
	TrackOpens     bool
	TrackClicks    bool
	Organization   string
	CreatedAt      time.Time
}

func New(
	cfg config.Config,
	pool *pgxpool.Pool,
	bus *messaging.Bus,
	transport delivery.Transport,
	tokens *auth.Manager,
	campaignService *campaigns.Service,
	logger *slog.Logger,
) *Worker {
	burst := cfg.SendingRate / 4
	if burst < 1 {
		burst = 1
	}
	return &Worker{
		cfg:            cfg,
		pool:           pool,
		bus:            bus,
		transport:      transport,
		renderer:       delivery.NewRenderer(),
		tokens:         tokens,
		campaigns:      campaignService,
		logger:         logger,
		limiter:        rate.NewLimiter(rate.Limit(cfg.SendingRate), burst),
		domainInFlight: make(map[string]int),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	if _, err := w.bus.EnsureStream(ctx, "DELIVERY", []string{messaging.SubjectDeliveryJobs}); err != nil {
		return err
	}

	if n, err := w.campaigns.RepublishPending(ctx, 500); err != nil {
		w.logger.Warn("republish pending jobs", "error", err)
	} else if n > 0 {
		w.logger.Info("republished pending delivery jobs", "count", n)
	}

	consumeCtx, err := w.bus.Consume(ctx, "DELIVERY", "delivery-workers", w.handleMessage)
	if err != nil {
		return err
	}
	defer consumeCtx.Stop()

	go w.schedulerLoop(ctx)
	go w.contactImportLoop(ctx)

	w.logger.Info("delivery worker started",
		"sending_rate_per_second", w.cfg.SendingRate,
		"transport", fmt.Sprintf("%T", w.transport),
	)

	<-ctx.Done()
	return nil
}

func (w *Worker) handleMessage(ctx context.Context, msg jetstream.Msg) {
	var job campaigns.JobPayload
	if err := json.Unmarshal(msg.Data(), &job); err != nil {
		w.logger.Error("decode delivery job", "error", err)
		_ = msg.Term()
		return
	}

	retryAfter, err := w.process(ctx, job)
	if err != nil {
		w.logger.Error("process delivery job", "job_id", job.JobID, "error", err)
		if retryAfter <= 0 {
			retryAfter = 30 * time.Second
		}
	}
	if retryAfter > 0 {
		if err := msg.NakWithDelay(retryAfter); err != nil {
			w.logger.Warn("nack delivery job", "job_id", job.JobID, "error", err)
		}
		return
	}
	if err := msg.Ack(); err != nil {
		w.logger.Warn("ack delivery job", "job_id", job.JobID, "error", err)
	}
}

func (w *Worker) process(ctx context.Context, job campaigns.JobPayload) (time.Duration, error) {
	row, err := w.loadJob(ctx, job.JobID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}

	if isTerminalJobStatus(row.Status) {
		return 0, nil
	}

	// Maximum message age: do not retry jobs older than the configured age.
	if w.cfg.MaxMessageAge > 0 && time.Since(row.CreatedAt) > w.cfg.MaxMessageAge {
		return 0, w.deadLetterJob(ctx, row, "message_expired", fmt.Errorf("message exceeded max age %s", w.cfg.MaxMessageAge))
	}

	if row.CampaignStatus == "cancelled" || row.CampaignStatus == "failed" {
		return 0, w.cancelPendingJob(ctx, row, "campaign_"+row.CampaignStatus)
	}
	if row.CampaignStatus == "paused" {
		return time.Minute, nil
	}

	if row.ContactStatus != "subscribed" {
		return 0, w.cancelPendingJob(ctx, row, "contact_"+row.ContactStatus)
	}

	var suppressed bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM suppressions
			WHERE organization_id = $1 AND lower(email) = lower($2)
		)
	`, row.OrganizationID, row.Email).Scan(&suppressed); err != nil {
		return 0, fmt.Errorf("check suppression: %w", err)
	}
	if suppressed {
		return 0, w.cancelPendingJob(ctx, row, "suppressed")
	}

	if w.cfg.RequireVerifiedDomain {
		senderDomain := domainOf(row.SenderEmail)
		var verified bool
		if err := w.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM sending_domains
				WHERE organization_id = $1 AND lower(domain) = lower($2) AND status = 'verified'
			)
		`, row.OrganizationID, senderDomain).Scan(&verified); err != nil {
			return 0, fmt.Errorf("check sending domain: %w", err)
		}
		if !verified {
			return 0, w.failJob(ctx, row, fmt.Errorf("sending domain %q is not verified", senderDomain))
		}
	}

	// Tenant quota and abuse pause: reject sends for paused organizations or
	// organizations that have exceeded their daily email quota.
	if err := w.checkTenantQuota(ctx, row.OrganizationID); err != nil {
		return 0, w.failJob(ctx, row, err)
	}

	if err := w.limiter.Wait(ctx); err != nil {
		return 30 * time.Second, err
	}

	data := personalizationData(row)
	htmlBody, textBody, err := w.renderer.RenderDesign(json.RawMessage(row.Content), data)
	if err != nil {
		return 0, w.failJob(ctx, row, err)
	}

	unsubscribeToken, err := w.tokens.IssueUnsubscribe(row.OrganizationID, row.ContactID)
	if err != nil {
		return 30 * time.Second, fmt.Errorf("issue unsubscribe token: %w", err)
	}
	unsubscribeURL := strings.TrimRight(w.cfg.APIBaseURL, "/") + "/api/v1/public/unsubscribe?token=" + url.QueryEscape(unsubscribeToken)

	if row.TrackClicks {
		htmlBody = delivery.RewriteLinks(htmlBody, w.cfg.TrackingBaseURL, func(target string) (string, error) {
			return w.tokens.IssueClick(row.ID, row.OrganizationID, row.CampaignID, row.ContactID, target)
		})
	}

	footer := fmt.Sprintf(
		`<div style="margin-top:32px;padding-top:24px;border-top:1px solid #e2e8f0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:12px;line-height:1.6;color:#94a3b8;">
<p style="margin:0 0 8px;">You are receiving this email because you subscribed to updates from %s.</p>
<p style="margin:0;"><a href="%s" style="color:#64748b;text-decoration:underline;">Unsubscribe</a></p>
</div>`,
		html.EscapeString(row.Organization),
		html.EscapeString(unsubscribeURL),
	)
	htmlBody = appendBeforeBodyClose(htmlBody, footer)

	if row.TrackOpens {
		trackingToken, err := w.tokens.IssueTracking(row.ID, row.OrganizationID, row.CampaignID, row.ContactID)
		if err == nil {
			pixel := fmt.Sprintf(`<img src="%s/t/open/%s.gif" width="1" height="1" alt="" style="display:block;width:1px;height:1px;border:0;">`,
				strings.TrimRight(w.cfg.TrackingBaseURL, "/"), trackingToken)
			htmlBody = appendBeforeBodyClose(htmlBody, pixel)
		}
	}

	sender := row.SenderEmail
	if row.SenderName != "" {
		sender = fmt.Sprintf("%s <%s>", row.SenderName, row.SenderEmail)
	}

	headers := map[string]string{
		"List-Unsubscribe":      "<" + unsubscribeURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		"X-Campaign-ID":         row.CampaignID,
	}
	if row.ReplyTo != "" {
		headers["Reply-To"] = row.ReplyTo
	}

	message := delivery.Message{
		ID:      row.MessageID,
		From:    sender,
		To:      row.Email,
		Subject: row.Subject,
		HTML:    htmlBody,
		Text:    textBody,
		Headers: headers,
	}

	// Per-domain concurrency: limit simultaneous outbound connections per
	// recipient domain to protect sender reputation.
	recipientDomain := domainOf(row.Email)
	if !w.acquireDomainSlot(recipientDomain) {
		return 30 * time.Second, fmt.Errorf("domain %s concurrency limit reached", recipientDomain)
	}
	defer w.releaseDomainSlot(recipientDomain)

	result, err := w.transport.Send(ctx, message)
	if err != nil {
		return w.handleSendFailure(ctx, row, err)
	}

	if err := w.recordSubmission(ctx, row, result); err != nil {
		return 30 * time.Second, err
	}

	observability.DeliveryJobs.WithLabelValues("submitted").Inc()
	w.maybeCompleteCampaign(ctx, row.CampaignID, row.OrganizationID)
	return 0, nil
}

func (w *Worker) loadJob(ctx context.Context, jobID string) (*jobRow, error) {
	var row jobRow
	err := w.pool.QueryRow(ctx, `
		SELECT dj.id, dj.organization_id, dj.campaign_id, dj.contact_id, dj.message_id,
		       dj.status, dj.attempts, dj.max_attempts,
		       COALESCE(c.email, cr.email, '') AS email,
		       COALESCE(c.first_name, ''), COALESCE(c.last_name, ''),
		       COALESCE(c.status, 'missing'), COALESCE(c.custom_fields, '{}'::jsonb),
		       cp.subject, cp.preview_text, cp.sender_name, cp.sender_email,
		       COALESCE(cp.reply_to, ''), cp.content, cp.status,
		       cp.track_opens, cp.track_clicks, o.name,
		       dj.created_at
		FROM delivery_jobs dj
		JOIN campaigns cp ON cp.id = dj.campaign_id
		JOIN organizations o ON o.id = dj.organization_id
		LEFT JOIN contacts c ON c.id = dj.contact_id
		LEFT JOIN campaign_recipients cr
		       ON cr.campaign_id = dj.campaign_id AND cr.contact_id = dj.contact_id
		WHERE dj.id = $1
	`, jobID).Scan(
		&row.ID, &row.OrganizationID, &row.CampaignID, &row.ContactID, &row.MessageID,
		&row.Status, &row.Attempts, &row.MaxAttempts,
		&row.Email, &row.FirstName, &row.LastName,
		&row.ContactStatus, &row.CustomFields,
		&row.Subject, &row.PreviewText, &row.SenderName, &row.SenderEmail,
		&row.ReplyTo, &row.Content, &row.CampaignStatus,
		&row.TrackOpens, &row.TrackClicks, &row.Organization,
		&row.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (w *Worker) recordSubmission(ctx context.Context, row *jobRow, result *delivery.Result) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin submission transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	transportMessageID := result.MessageID
	if transportMessageID == "" {
		transportMessageID = row.MessageID
	}

	tag, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'submitted',
		    attempts = attempts + 1,
		    accepted_at = now(),
		    transport_message_id = $2,
		    last_error = NULL,
		    updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'processing', 'deferred')
	`, row.ID, transportMessageID)
	if err != nil {
		return fmt.Errorf("mark job submitted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipients
		SET status = 'submitted', message_id = $3
		WHERE campaign_id = $1 AND contact_id = $2
	`, row.CampaignID, row.ContactID, transportMessageID); err != nil {
		return fmt.Errorf("mark recipient submitted: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, 'submitted', '{}'::jsonb)
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID); err != nil {
		return fmt.Errorf("record submitted event: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (organization_id, metric, quantity)
		VALUES ($1, 'email_submitted', 1)
	`, row.OrganizationID); err != nil {
		return fmt.Errorf("record usage: %w", err)
	}

	return tx.Commit(ctx)
}

func (w *Worker) handleSendFailure(ctx context.Context, row *jobRow, sendErr error) (time.Duration, error) {
	attempts := row.Attempts + 1
	smtpCode := delivery.ExtractSMTPCode(sendErr)
	bounceType, permanent := delivery.ClassifyBounce(sendErr, smtpCode)

	if permanent {
		if bounceType == delivery.BounceHard {
			if err := w.suppressContact(ctx, row.OrganizationID, row.Email, "hard_bounce", "smtp"); err != nil {
				w.logger.Warn("suppress contact after hard bounce", "job_id", row.ID, "email", row.Email, "error", err)
			}
		}
		return 0, w.bounceJob(ctx, row, sendErr, bounceType, smtpCode)
	}

	if attempts >= row.MaxAttempts {
		return 0, w.deadLetterJob(ctx, row, "max_attempts_exceeded", sendErr)
	}

	delay := backoff(attempts)
	_, err := w.pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'deferred',
		    attempts = $2,
		    next_attempt_at = now() + $3::interval,
		    last_error = $4,
		    updated_at = now()
		WHERE id = $1
	`, row.ID, attempts, fmt.Sprintf("%d seconds", int(delay.Seconds())), truncateError(sendErr))
	if err != nil {
		return 30 * time.Second, fmt.Errorf("record deferred job: %w", err)
	}

	_, _ = w.pool.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, 'deferred', jsonb_build_object('error', $5::text, 'attempt', $6::int))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, truncateError(sendErr), attempts)

	observability.DeliveryJobs.WithLabelValues("deferred").Inc()
	w.logger.Warn("delivery deferred",
		"job_id", row.ID,
		"attempt", attempts,
		"retry_in", delay.String(),
		"error", sendErr,
	)
	return delay, nil
}

func (w *Worker) failJob(ctx context.Context, row *jobRow, cause error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failure transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	message := truncateError(cause)
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'failed',
		    attempts = attempts + 1,
		    last_error = $2,
		    updated_at = now()
		WHERE id = $1
	`, row.ID, message); err != nil {
		return fmt.Errorf("mark job failed: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipients
		SET status = 'failed'
		WHERE campaign_id = $1 AND contact_id = $2
	`, row.CampaignID, row.ContactID); err != nil {
		return fmt.Errorf("mark recipient failed: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, 'failed', jsonb_build_object('error', $5::text))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message); err != nil {
		return fmt.Errorf("record failed event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failure: %w", err)
	}

	observability.DeliveryJobs.WithLabelValues("failed").Inc()
	w.logger.Error("delivery failed", "job_id", row.ID, "error", cause)
	w.maybeCompleteCampaign(ctx, row.CampaignID, row.OrganizationID)
	return nil
}

// bounceJob records a permanent delivery failure as a hard bounce.
func (w *Worker) bounceJob(ctx context.Context, row *jobRow, cause error, bounceType delivery.BounceType, smtpCode string) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin bounce transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	message := truncateError(cause)
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'bounced',
		    attempts = attempts + 1,
		    bounce_type = $2,
		    smtp_code = $3,
		    last_error = $4,
		    updated_at = now()
		WHERE id = $1
	`, row.ID, bounceType, smtpCode, message); err != nil {
		return fmt.Errorf("mark job bounced: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipients
		SET status = 'bounced'
		WHERE campaign_id = $1 AND contact_id = $2
	`, row.CampaignID, row.ContactID); err != nil {
		return fmt.Errorf("mark recipient bounced: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, 'bounced', jsonb_build_object('error', $5::text, 'bounce_type', $6, 'smtp_code', $7))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message, bounceType, smtpCode); err != nil {
		return fmt.Errorf("record bounced event: %w", err)
	}

	observability.DeliveryJobs.WithLabelValues("bounced").Inc()
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bounce: %w", err)
	}

	w.logger.Error("delivery bounced", "job_id", row.ID, "email", row.Email, "bounce_type", bounceType, "error", cause)
	w.maybeCompleteCampaign(ctx, row.CampaignID, row.OrganizationID)
	return nil
}

// deadLetterJob moves an exhausted or aged-out delivery job to the dead-letter
// queue so it can be inspected and manually replayed later.
func (w *Worker) deadLetterJob(ctx context.Context, row *jobRow, reason string, cause error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin dead-letter transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	message := truncateError(cause)
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'failed',
		    attempts = attempts + 1,
		    dead_lettered_at = now(),
		    last_error = $2,
		    updated_at = now()
		WHERE id = $1
	`, row.ID, message); err != nil {
		return fmt.Errorf("mark job dead-lettered: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO dead_letter_queue
			(organization_id, campaign_id, contact_id, delivery_job_id, reason, attempts, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, reason, row.Attempts+1, message); err != nil {
		return fmt.Errorf("insert dead-letter record: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipients
		SET status = 'failed'
		WHERE campaign_id = $1 AND contact_id = $2
	`, row.CampaignID, row.ContactID); err != nil {
		return fmt.Errorf("mark recipient failed: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1, $2, $3, $4, 'failed', jsonb_build_object('error', $5::text, 'dead_lettered', true, 'reason', $6))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message, reason); err != nil {
		return fmt.Errorf("record dead-lettered event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit dead-letter: %w", err)
	}

	observability.DeliveryJobs.WithLabelValues("failed").Inc()
	w.logger.Error("delivery dead-lettered", "job_id", row.ID, "email", row.Email, "reason", reason, "error", cause)
	w.maybeCompleteCampaign(ctx, row.CampaignID, row.OrganizationID)
	return nil
}

// suppressContact adds an email address to the organization suppression list.
func (w *Worker) suppressContact(ctx context.Context, organizationID, email, reason, source string) error {
	_, err := w.pool.Exec(ctx, `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, lower($2), $3, $4)
		ON CONFLICT (organization_id, lower(email)) DO NOTHING
	`, organizationID, email, reason, source)
	if err != nil {
		return fmt.Errorf("insert suppression: %w", err)
	}

	if _, err := w.pool.Exec(ctx, `
		UPDATE contacts
		SET status = 'suppressed', updated_at = now()
		WHERE organization_id = $1 AND lower(email) = lower($2)
	`, organizationID, email); err != nil {
		return fmt.Errorf("mark contact suppressed: %w", err)
	}

	observability.DeliveryJobs.WithLabelValues("suppressed").Inc()
	w.logger.Info("contact suppressed", "organization_id", organizationID, "email", email, "reason", reason, "source", source)
	return nil
}

// checkTenantQuota enforces daily email quotas and abuse pauses.
func (w *Worker) checkTenantQuota(ctx context.Context, organizationID string) error {
	var settings struct {
		MaxDailyEmails int
		PausedUntil    *time.Time
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(max_daily_emails, 10000), paused_until
		FROM organization_settings
		WHERE organization_id = $1
	`, organizationID).Scan(&settings.MaxDailyEmails, &settings.PausedUntil); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return fmt.Errorf("load organization settings: %w", err)
	}

	if settings.PausedUntil != nil && settings.PausedUntil.After(time.Now()) {
		return fmt.Errorf("organization sending paused until %s", settings.PausedUntil.Format(time.RFC3339))
	}

	var dailyCount int
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)
		FROM usage_records
		WHERE organization_id = $1
		  AND metric = 'email_submitted'
		  AND occurred_at >= now() - interval '1 day'
	`, organizationID).Scan(&dailyCount); err != nil {
		return fmt.Errorf("count daily emails: %w", err)
	}

	if dailyCount >= settings.MaxDailyEmails {
		return fmt.Errorf("daily email quota of %d exceeded", settings.MaxDailyEmails)
	}
	return nil
}

// acquireDomainSlot reserves a concurrency slot for the given domain.
func (w *Worker) acquireDomainSlot(domain string) bool {
	if domain == "" {
		return true
	}
	w.domainMu.Lock()
	defer w.domainMu.Unlock()
	limit := w.cfg.DomainConcurrencyLimit
	if w.domainInFlight[domain] >= limit {
		return false
	}
	w.domainInFlight[domain]++
	return true
}

// releaseDomainSlot frees a concurrency slot for the given domain.
func (w *Worker) releaseDomainSlot(domain string) {
	if domain == "" {
		return
	}
	w.domainMu.Lock()
	defer w.domainMu.Unlock()
	if w.domainInFlight[domain] > 0 {
		w.domainInFlight[domain]--
	}
}

func (w *Worker) cancelPendingJob(ctx context.Context, row *jobRow, reason string) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'cancelled', last_error = $2, updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'processing', 'deferred')
	`, row.ID, reason); err != nil {
		return fmt.Errorf("cancel job: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipients
		SET status = 'skipped'
		WHERE campaign_id = $1 AND contact_id = $2 AND status = 'pending'
	`, row.CampaignID, row.ContactID); err != nil {
		return fmt.Errorf("skip recipient: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancellation: %w", err)
	}

	observability.DeliveryJobs.WithLabelValues("cancelled").Inc()
	w.maybeCompleteCampaign(ctx, row.CampaignID, row.OrganizationID)
	return nil
}

func (w *Worker) maybeCompleteCampaign(ctx context.Context, campaignID, organizationID string) {
	if _, err := w.pool.Exec(ctx, `
		UPDATE campaigns c
		SET status = 'completed', completed_at = now(), updated_at = now()
		WHERE c.id = $1 AND c.organization_id = $2 AND c.status = 'sending'
		  AND NOT EXISTS (
			  SELECT 1 FROM delivery_jobs dj
			  WHERE dj.campaign_id = c.id
			    AND dj.status IN ('queued', 'processing', 'deferred')
		  )
	`, campaignID, organizationID); err != nil {
		w.logger.Warn("complete campaign", "campaign_id", campaignID, "error", err)
	}
}

func (w *Worker) schedulerLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.SchedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			observability.QueuePolls.Inc()
			refs, err := w.campaigns.DueCampaigns(ctx, 25)
			if err != nil {
				w.logger.Error("claim due campaigns", "error", err)
				continue
			}
			for _, ref := range refs {
				count, err := w.campaigns.Queue(ctx, ref.ID, ref.OrganizationID)
				if err != nil {
					w.logger.Error("queue campaign",
						"campaign_id", ref.ID,
						"organization_id", ref.OrganizationID,
						"error", err,
					)
					continue
				}
				w.logger.Info("campaign queued", "campaign_id", ref.ID, "jobs", count)
			}
		}
	}
}

func personalizationData(row *jobRow) map[string]string {
	data := map[string]string{
		"first_name":   row.FirstName,
		"last_name":    row.LastName,
		"email":        row.Email,
		"organization": row.Organization,
	}

	var custom map[string]any
	if err := json.Unmarshal(row.CustomFields, &custom); err == nil {
		for key, value := range custom {
			text := fmt.Sprint(value)
			data["custom."+key] = text
			if _, exists := data[key]; !exists {
				data[key] = text
			}
		}
	}
	return data
}

func appendBeforeBodyClose(document, fragment string) string {
	if idx := strings.LastIndex(strings.ToLower(document), "</body>"); idx >= 0 {
		return document[:idx] + fragment + document[idx:]
	}
	return document + fragment
}

func isTerminalJobStatus(status string) bool {
	switch status {
	case "submitted", "accepted", "bounced", "complained", "unsubscribed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt >= 10 {
		return 6 * time.Hour
	}
	delay := time.Duration(1<<uint(attempt-1)) * time.Minute
	if delay > 6*time.Hour {
		return 6 * time.Hour
	}
	return delay
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}

func domainOf(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return ""
}
