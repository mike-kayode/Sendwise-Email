package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/domainwarmup"
	"github.com/khayode/email-platform/internal/messaging"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/quota"
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
	recipientRates map[string]*rate.Limiter
	smtpBreaker    smtpCircuitBreaker
}

func (w *Worker) releaseProcessingLease(ctx context.Context, jobID string) error {
	_, err := w.pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'queued', processing_started_at = NULL,
		    nats_published_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'processing'
	`, jobID)
	if err != nil {
		return fmt.Errorf("release delivery job processing lease: %w", err)
	}
	return nil
}

func (w *Worker) closeSMTPBreaker() {
	if w.smtpBreaker.ProbeSucceeded(time.Now()) {
		observability.SMTPBreakerOpen.Set(0)
		w.logger.Info("SMTP circuit breaker closed; probe succeeded")
	}
}

func (w *Worker) claimForProcessing(ctx context.Context, row *jobRow) bool {
	tag, err := w.pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'processing', processing_started_at = now(),
		    nats_publish_claimed_at = NULL, updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'deferred')
		  AND next_attempt_at <= now()
		  AND processing_started_at IS NULL
	`, row.ID)
	if err != nil {
		w.logger.Error("claim delivery job for processing", "job_id", row.ID, "error", err)
		return false
	}
	return tag.RowsAffected() == 1
}

func (w *Worker) processingHeartbeat(ctx context.Context, jobID string) {
	interval := w.cfg.NATSStalePublishTimeout / 3
	if interval <= 0 || interval > 30*time.Second {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.pool.Exec(ctx, `
				UPDATE delivery_jobs
				SET processing_started_at = now(), updated_at = now()
				WHERE id = $1 AND status = 'processing'
			`, jobID); err != nil && ctx.Err() == nil {
				w.logger.Warn("refresh delivery processing lease", "job_id", jobID, "error", err)
			}
		}
	}
}

func (w *Worker) deferWithoutAge(ctx context.Context, row *jobRow, delay time.Duration, reason string) error {
	if delay < time.Second {
		delay = time.Second
	}
	_, err := w.pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'deferred', next_attempt_at = now() + $2::interval,
		    failure_started_at = NULL, processing_started_at = NULL,
		    nats_publish_claimed_at = NULL, nats_published_at = NULL,
		    last_error = $3, updated_at = now()
		WHERE id = $1 AND status = 'processing'
	`, row.ID, fmt.Sprintf("%f seconds", delay.Seconds()), reason)
	if err != nil {
		return fmt.Errorf("defer delivery job without age limit: %w", err)
	}
	row.Status = "deferred"
	return nil
}

func (w *Worker) deferInfrastructureFailure(ctx context.Context, row *jobRow, sendErr error, until time.Time) error {
	if err := w.releaseCampaignQuota(ctx, row); err != nil {
		return fmt.Errorf("release campaign quota after SMTP infrastructure failure: %w", err)
	}
	if until.Before(time.Now()) {
		until = time.Now().Add(30 * time.Second)
	}
	_, err := w.pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'deferred', next_attempt_at = $2,
		    failure_started_at = NULL, processing_started_at = NULL,
		    nats_publish_claimed_at = NULL, nats_published_at = NULL,
		    last_error = $3, updated_at = now()
		WHERE id = $1 AND status = 'processing'
	`, row.ID, until, truncateError(sendErr))
	if err != nil {
		return fmt.Errorf("defer delivery after SMTP infrastructure failure: %w", err)
	}
	row.Status = "deferred"
	return nil
}

var errWorkspaceSuspended = errors.New("workspace is suspended")

type jobRow struct {
	ID                  string
	OrganizationID      string
	CampaignID          string
	ContactID           string
	MessageID           string
	Status              string
	Attempts            int
	MaxAttempts         int
	Email               string
	FirstName           string
	LastName            string
	ContactStatus       string
	CustomFields        []byte
	Subject             string
	PreviewText         string
	SenderName          string
	SenderEmail         string
	ReplyTo             string
	Content             []byte
	CampaignStatus      string
	TrackOpens          bool
	TrackClicks         bool
	Organization        string
	SenderPostalAddress string
	SenderCompany       string
	SenderStreet        string
	SenderCity          string
	SenderRegion        string
	SenderPostalCode    string
	SenderCountry       string
	SenderWebsite       string
	SenderSupportEmail  string
	SenderFooterText    string
	SenderSocialLinks   []byte
	WarmupReservedOn    *time.Time
	QuotaReservationID  string
	QuotaDeferred       bool
	CreatedAt           time.Time
	FailureStartedAt    pgtype.Timestamptz
	TrackingID          string
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
	if cfg.WarmupStartPerDay < 1 {
		cfg.WarmupStartPerDay = 50
	}
	if cfg.WarmupGrowthFactor < 1 {
		cfg.WarmupGrowthFactor = 1.5
	}
	if cfg.MaxDailyEmails < 1 {
		cfg.MaxDailyEmails = 10000
	}
	if cfg.NATSStalePublishTimeout <= 0 {
		cfg.NATSStalePublishTimeout = 10 * time.Minute
	}
	if cfg.PlatformMaxDomainRate < 1 {
		cfg.PlatformMaxDomainRate = 1000
	}
	if cfg.RecipientDomainDefault < 1 {
		cfg.RecipientDomainDefault = cfg.SendingRate
		if cfg.RecipientDomainDefault < 1 {
			cfg.RecipientDomainDefault = 10
		}
	}
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
		recipientRates: make(map[string]*rate.Limiter),
	}
}

func (w *Worker) Run(ctx context.Context) error {
	if _, err := w.bus.EnsureStream(ctx, "DELIVERY", []string{messaging.SubjectDeliveryJobs}); err != nil {
		return err
	}

	if n, err := w.campaigns.RepublishPendingWithStaleTimeout(ctx, 500, w.cfg.NATSStalePublishTimeout); err != nil {
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
	if err != nil || retryAfter > 0 {
		if releaseErr := w.releaseProcessingLease(ctx, job.JobID); releaseErr != nil {
			w.logger.Error("release delivery processing lease", "job_id", job.JobID, "error", releaseErr)
		}
	}
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

	if row.Status == "processing" || !w.claimForProcessing(ctx, row) {
		return 0, nil
	}
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go w.processingHeartbeat(heartbeatCtx, row.ID)
	row.Status = "processing"

	if row.CampaignStatus == "cancelled" || row.CampaignStatus == "failed" {
		return 0, w.cancelPendingJob(ctx, row, "campaign_"+row.CampaignStatus)
	}
	if row.CampaignStatus == "paused" {
		return 0, w.deferWithoutAge(ctx, row, time.Minute, "campaign paused")
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

	var ownerConflict bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sending_domains
			WHERE organization_id = $1 AND lower(domain) = lower($2) AND owner_conflict
		)
	`, row.OrganizationID, domainOf(row.SenderEmail)).Scan(&ownerConflict); err != nil {
		return 0, fmt.Errorf("check sending-domain ownership: %w", err)
	}
	if ownerConflict {
		return 0, w.failJob(ctx, row, fmt.Errorf("sending domain %q is already owned by another workspace", domainOf(row.SenderEmail)))
	}
	var domainDisabled bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sending_domains
			WHERE organization_id = $1 AND lower(domain) = lower($2)
			  AND admin_disabled_at IS NOT NULL
		)
	`, row.OrganizationID, domainOf(row.SenderEmail)).Scan(&domainDisabled); err != nil {
		return 0, fmt.Errorf("check platform sending-domain disable: %w", err)
	}
	if domainDisabled {
		return 0, w.failJob(ctx, row, fmt.Errorf("sending domain %q has been disabled by a platform administrator", domainOf(row.SenderEmail)))
	}

	var authorizedSender bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM campaigns cp
			JOIN sender_identities si
			  ON si.id = cp.sender_identity_id AND si.organization_id = cp.organization_id
			JOIN sending_domains sd
			  ON sd.organization_id = si.organization_id AND lower(sd.domain) = lower(si.domain)
			WHERE cp.id = $1 AND cp.organization_id = $2
			  AND lower(si.local_part || '@' || si.domain) = lower(cp.sender_email)
			  AND sd.status = 'verified' AND NOT sd.owner_conflict
			  AND sd.admin_disabled_at IS NULL
		)
	`, row.CampaignID, row.OrganizationID).Scan(&authorizedSender); err != nil {
		return 0, fmt.Errorf("check campaign sender identity: %w", err)
	}
	if !authorizedSender {
		return 0, w.failJob(ctx, row, errors.New("campaign From address is not a verified sender identity owned by this workspace"))
	}
	if !completeSenderProfile(row) {
		return 0, w.failJob(ctx, row, errors.New("complete the sender profile before sending campaigns"))
	}

	if w.cfg.RequireVerifiedDomain {
		senderDomain := domainOf(row.SenderEmail)
		var verified bool
		if err := w.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM sending_domains
				WHERE organization_id = $1 AND lower(domain) = lower($2)
				  AND status = 'verified' AND admin_disabled_at IS NULL
			)
		`, row.OrganizationID, senderDomain).Scan(&verified); err != nil {
			return 0, fmt.Errorf("check sending domain: %w", err)
		}
		if !verified {
			return 0, w.failJob(ctx, row, fmt.Errorf("sending domain %q is not verified", senderDomain))
		}
	}

	// Workspace suspension and abuse pauses remain independent of domain quotas.
	if err := w.checkTenantQuota(ctx, row.OrganizationID); err != nil {
		if errors.Is(err, errWorkspaceSuspended) {
			return 0, w.deferWithoutAge(ctx, row, time.Minute, "workspace sending suspended")
		}
		return 0, w.failJob(ctx, row, err)
	}

	deferred, err := w.reserveWarmupSlot(ctx, row)
	if err != nil {
		return 30 * time.Second, err
	}
	if deferred {
		return 0, nil
	}

	if err := w.limiter.Wait(ctx); err != nil {
		return 30 * time.Second, err
	}
	recipientDomain := domainOf(row.Email)
	if err := w.recipientLimiter(recipientDomain).Wait(ctx); err != nil {
		return 30 * time.Second, err
	}
	if row.TrackOpens || row.TrackClicks {
		trackingID, err := w.ensureRecipientTracking(ctx, row)
		if err != nil {
			w.logger.Warn("recipient tracking unavailable; rendering untracked email",
				"job_id", row.ID, "error", err)
			row.TrackOpens = false
			row.TrackClicks = false
		} else {
			row.TrackingID = trackingID
		}
	}

	unsubscribeToken, err := w.tokens.IssueUnsubscribe(row.OrganizationID, row.ContactID)
	if err != nil {
		return 30 * time.Second, fmt.Errorf("issue unsubscribe token: %w", err)
	}
	unsubscribeURL := strings.TrimRight(w.cfg.APIBaseURL, "/") + "/api/v1/public/unsubscribe?token=" + url.QueryEscape(unsubscribeToken)

	data := personalizationData(row, unsubscribeURL)
	data["app_base_url"] = w.cfg.AppBaseURL
	data["sender_company"] = row.SenderCompany
	data["sender_postal_address"] = senderPostalAddress(row)
	data["sender_website"] = row.SenderWebsite
	data["sender_support_email"] = row.SenderSupportEmail
	data["sender_footer_text"] = row.SenderFooterText
	data["sender_social_links"] = string(row.SenderSocialLinks)
	htmlBody, textBody, err := w.renderer.RenderDesign(json.RawMessage(row.Content), data)
	if err != nil {
		return 0, w.failJob(ctx, row, err)
	}

	if row.TrackClicks {
		htmlBody = delivery.RewriteLinks(htmlBody, w.cfg.TrackingBaseURL, func(target string) (string, error) {
			linkToken, err := w.tokens.IssueClick(row.ID, row.OrganizationID, row.CampaignID, row.ContactID, row.TrackingID, target)
			if err == nil {
				if _, insertErr := w.pool.Exec(ctx, `
					INSERT INTO email_tracking_links
						(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
						 tracking_id, link_token, destination_url)
					SELECT crt.organization_id, crt.campaign_id, crt.recipient_id, crt.contact_id,
					       crt.delivery_job_id, crt.tracking_id, $1, $2
					FROM campaign_recipient_tracking crt
					WHERE crt.delivery_job_id = $3
					ON CONFLICT (delivery_job_id, destination_url) DO UPDATE
					SET link_token = EXCLUDED.link_token,
					    tracking_id = EXCLUDED.tracking_id
				`, linkToken, target, row.ID); insertErr != nil {
					w.logger.Warn("create recipient tracking link", "job_id", row.ID, "error", insertErr)
				}
			}
			return linkToken, err
		})
	}

	if row.TrackOpens {
		trackingToken, err := w.tokens.IssueTracking(row.ID, row.OrganizationID, row.CampaignID, row.ContactID, row.TrackingID)
		if err == nil {
			pixel := fmt.Sprintf(`<img src="%s/t/open/%s.gif" width="1" height="1" alt="" style="display:block;width:1px;height:1px;border:0;">`,
				strings.TrimRight(w.cfg.TrackingBaseURL, "/"), trackingToken)
			htmlBody = appendBeforeBodyClose(htmlBody, pixel)
		}
	}

	address := senderPostalAddress(row)
	textBody += "\n\nYou are receiving this email because you subscribed to updates from " + row.SenderCompany + ".\n"
	if address != "" {
		textBody += address + "\n"
	}
	if strings.TrimSpace(row.SenderFooterText) != "" {
		textBody += strings.TrimSpace(row.SenderFooterText) + "\n"
	}
	if strings.TrimSpace(row.SenderWebsite) != "" {
		textBody += "Website: " + strings.TrimSpace(row.SenderWebsite) + "\n"
	}
	if strings.TrimSpace(row.SenderSupportEmail) != "" {
		textBody += "Support: " + strings.TrimSpace(row.SenderSupportEmail) + "\n"
	}
	textBody += "Unsubscribe: " + unsubscribeURL + "\n"

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
		ID:          row.MessageID,
		WorkspaceID: row.OrganizationID,
		CampaignID:  row.CampaignID,
		From:        sender,
		To:          row.Email,
		Subject:     row.Subject,
		HTML:        htmlBody,
		Text:        textBody,
		Headers:     headers,
	}
	verpClaims := delivery.VERPClaims{
		WorkspaceID: row.OrganizationID,
		CampaignID:  row.CampaignID,
		Recipient:   strings.ToLower(row.Email),
	}
	verpToken, err := delivery.NewVERPToken(w.cfg.JWTSecret, verpClaims)
	if err != nil {
		return 0, w.failJob(ctx, row, fmt.Errorf("create signed VERP token: %w", err))
	}
	message.EnvelopeFrom, err = delivery.FormatVERPReturnPath(w.cfg.BounceDomain, verpToken)
	if err != nil {
		return 0, w.failJob(ctx, row, fmt.Errorf("format VERP return path: %w", err))
	}
	tag, err := w.pool.Exec(ctx, `
		INSERT INTO delivery_verp_tokens
			(token, organization_id, campaign_id, delivery_job_id, contact_id, email)
		VALUES ($1, $2, $3, $4, $5, lower($6))
		ON CONFLICT (token) DO UPDATE SET token = EXCLUDED.token
		WHERE delivery_verp_tokens.organization_id = EXCLUDED.organization_id
		  AND delivery_verp_tokens.campaign_id = EXCLUDED.campaign_id
		  AND delivery_verp_tokens.delivery_job_id = EXCLUDED.delivery_job_id
		  AND lower(delivery_verp_tokens.email) = lower(EXCLUDED.email)
	`, verpToken, row.OrganizationID, row.CampaignID, row.ID, row.ContactID, row.Email)
	if err != nil {
		return 0, w.failJob(ctx, row, fmt.Errorf("persist signed VERP token mapping: %w", err))
	}
	if tag.RowsAffected() == 0 {
		return 0, w.failJob(ctx, row, errors.New("signed VERP token conflicts with a different delivery"))
	}
	message.DKIM, err = delivery.LoadDKIMConfig(ctx, w.pool, row.OrganizationID, row.SenderEmail, w.cfg.DKIMEncryptionKey)
	if err != nil {
		return w.handleSendFailure(ctx, row, fmt.Errorf("load dkim signing key: %w", err))
	}

	// Per-domain concurrency: limit simultaneous outbound connections per
	// recipient domain to protect sender reputation.
	if !w.acquireDomainSlot(recipientDomain) {
		return 30 * time.Second, fmt.Errorf("domain %s concurrency limit reached", recipientDomain)
	}
	defer w.releaseDomainSlot(recipientDomain)

	deferred, _, err = w.reserveCampaignQuota(ctx, row)
	if err != nil {
		return 30 * time.Second, err
	}
	if deferred {
		return 0, nil
	}

	// Cap- and quota-deferred jobs clear this clock before they are resumed.
	if row.FailureStartedAt.Valid && w.cfg.MaxMessageAge > 0 &&
		time.Since(row.FailureStartedAt.Time) > w.cfg.MaxMessageAge {
		if err := w.releaseWarmupReservation(ctx, row); err != nil {
			return 30 * time.Second, err
		}
		return 0, w.deadLetterJob(ctx, row, "message_expired", fmt.Errorf("message exceeded max age %s", w.cfg.MaxMessageAge))
	}

	if allowed, until := w.smtpBreaker.Allow(time.Now()); !allowed {
		return 0, w.deferWithoutAge(ctx, row, time.Until(until), "smtp circuit breaker open")
	}
	result, err := w.transport.Send(ctx, message)
	if err != nil {
		if delivery.IsInfrastructureError(err) {
			until, opened := w.smtpBreaker.Failure(time.Now())
			if opened {
				observability.SMTPBreakerOpen.Set(1)
				w.logger.Warn("SMTP circuit breaker opened",
					"failure_threshold", smtpBreakerFailureThreshold,
					"failure_window", smtpBreakerFailureWindow.String(),
					"cooldown", time.Until(until).Round(time.Second).String(),
				)
			}
			return 0, w.deferInfrastructureFailure(ctx, row, err, until)
		}
		w.closeSMTPBreaker()
		return w.handleSendFailure(ctx, row, err)
	}
	w.closeSMTPBreaker()

	if err := w.recordSubmission(ctx, row, result); err != nil {
		return 30 * time.Second, err
	}

	observability.DeliveryJobs.WithLabelValues("submitted").Inc()
	w.maybeCompleteCampaign(ctx, row.CampaignID, row.OrganizationID)
	return 0, nil
}

func (w *Worker) ensureRecipientTracking(ctx context.Context, row *jobRow) (string, error) {
	var trackingID string
	err := w.pool.QueryRow(ctx, `
		INSERT INTO campaign_recipient_tracking
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 tracking_id, email, delivery_status, queued_at)
		SELECT r.organization_id, r.campaign_id, r.id, r.contact_id, dj.id,
		       'trk_' || encode(gen_random_bytes(24), 'hex'), r.email, dj.status, dj.created_at
		FROM campaign_recipients r
		JOIN delivery_jobs dj
		  ON dj.campaign_id = r.campaign_id AND dj.contact_id = r.contact_id
		WHERE dj.id = $1
		ON CONFLICT (campaign_id, contact_id) DO UPDATE
		SET delivery_job_id = EXCLUDED.delivery_job_id,
		    email = EXCLUDED.email,
		    updated_at = now()
		RETURNING campaign_recipient_tracking.tracking_id
	`, row.ID).Scan(&trackingID)
	if err != nil {
		return "", fmt.Errorf("ensure campaign recipient tracking: %w", err)
	}
	return trackingID, nil
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
		       COALESCE(cp.sender_postal_address, ''),
		       COALESCE(sp.company_name, ''), COALESCE(sp.street_address, ''),
		       COALESCE(sp.city, ''), COALESCE(sp.region, ''), COALESCE(sp.postal_code, ''),
		       COALESCE(sp.country, ''), COALESCE(sp.website, ''), COALESCE(sp.support_email, ''),
		       COALESCE(sp.footer_text, ''), COALESCE(sp.social_links, '[]'::jsonb),
		       dj.warmup_reserved_on, COALESCE(dj.quota_reservation_id::text, ''),
		       dj.quota_deferred, dj.created_at, dj.failure_started_at,
		       COALESCE(crt.tracking_id, '')
		FROM delivery_jobs dj
		JOIN campaigns cp ON cp.id = dj.campaign_id
		JOIN organizations o ON o.id = dj.organization_id
		LEFT JOIN sender_profiles sp ON sp.organization_id = dj.organization_id
		LEFT JOIN contacts c ON c.id = dj.contact_id
		LEFT JOIN campaign_recipients cr
		       ON cr.campaign_id = dj.campaign_id AND cr.contact_id = dj.contact_id
		LEFT JOIN campaign_recipient_tracking crt
		       ON crt.delivery_job_id = dj.id
		WHERE dj.id = $1
	`, jobID).Scan(
		&row.ID, &row.OrganizationID, &row.CampaignID, &row.ContactID, &row.MessageID,
		&row.Status, &row.Attempts, &row.MaxAttempts,
		&row.Email, &row.FirstName, &row.LastName,
		&row.ContactStatus, &row.CustomFields,
		&row.Subject, &row.PreviewText, &row.SenderName, &row.SenderEmail,
		&row.ReplyTo, &row.Content, &row.CampaignStatus,
		&row.TrackOpens, &row.TrackClicks, &row.Organization,
		&row.SenderPostalAddress,
		&row.SenderCompany, &row.SenderStreet, &row.SenderCity, &row.SenderRegion,
		&row.SenderPostalCode, &row.SenderCountry, &row.SenderWebsite, &row.SenderSupportEmail,
		&row.SenderFooterText, &row.SenderSocialLinks,
		&row.WarmupReservedOn,
		&row.QuotaReservationID, &row.QuotaDeferred,
		&row.CreatedAt, &row.FailureStartedAt, &row.TrackingID,
	)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (w *Worker) recordSubmission(ctx context.Context, row *jobRow, result *delivery.Result) error {
	transportMessageID := result.MessageID
	if transportMessageID == "" {
		transportMessageID = row.MessageID
	}

	retryCtx := context.WithoutCancel(ctx)
	retryDelay := time.Second
	for {
		attemptCtx, cancel := context.WithTimeout(retryCtx, 5*time.Second)
		tag, err := w.pool.Exec(attemptCtx, `
		UPDATE delivery_jobs
		SET status = 'submitted',
		    attempts = attempts + CASE WHEN status = 'submitted' THEN 0 ELSE 1 END,
		    accepted_at = COALESCE(accepted_at, now()),
		    transport_message_id = COALESCE(transport_message_id, $2::text),
		    quota_deferred = false,
		    failure_started_at = NULL,
		    processing_started_at = NULL,
		    last_error = NULL,
		    updated_at = now()
		WHERE id = $1::uuid
	`, row.ID, transportMessageID)
		cancel()
		if err == nil && tag.RowsAffected() == 1 {
			break
		}
		if err == nil {
			err = errors.New("delivery job row was not updated")
		}
		w.logger.Error("SMTP accepted but delivery submission is not durable; retrying",
			"job_id", row.ID, "error", err, "retry_in", retryDelay.String())
		time.Sleep(retryDelay)
		if retryDelay < 30*time.Second {
			retryDelay *= 2
			if retryDelay > 30*time.Second {
				retryDelay = 30 * time.Second
			}
		}
	}

	if err := w.recordSubmissionTracking(ctx, row, transportMessageID); err != nil {
		w.logger.Warn("record post-submission delivery bookkeeping", "job_id", row.ID, "error", err)
	}
	return nil
}

func (w *Worker) recordSubmissionTracking(ctx context.Context, row *jobRow, transportMessageID string) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin post-submission bookkeeping: %w", err)
	}
	defer tx.Rollback(ctx)

	if row.QuotaReservationID != "" {
		if err := quota.Retain(ctx, tx, quota.StreamMarketing, quota.SourceCampaignDelivery, row.QuotaReservationID); err != nil {
			return fmt.Errorf("retain submitted campaign quota: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE delivery_jobs
			SET quota_reservation_id = NULL, updated_at = now()
			WHERE id = $1::uuid
			  AND status = 'submitted'
			  AND quota_reservation_id = $2::uuid
		`, row.ID, row.QuotaReservationID); err != nil {
			return fmt.Errorf("clear retained campaign quota reservation: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET delivery_status = 'submitted',
		    accepted_at = COALESCE(accepted_at, now()),
		    updated_at = now()
		WHERE delivery_job_id = $1::uuid
		  AND organization_id = $2::uuid
	`, row.ID, row.OrganizationID); err != nil {
		return fmt.Errorf("update recipient tracking submission: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO email_tracking_events
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 tracking_id, event_type, event_metadata, event_hash)
		SELECT $1::uuid, $2::uuid, recipient_id, $3::uuid, $4::uuid,
		       tracking_id, 'accepted'::text, '{}'::jsonb,
		       md5($4::uuid::text || ':accepted')
		FROM campaign_recipient_tracking
		WHERE delivery_job_id = $4::uuid
		ON CONFLICT DO NOTHING
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID); err != nil {
		return fmt.Errorf("record recipient tracking submission: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO sending_domain_daily_stats
			(organization_id, domain, sending_day, reserved_count, successful_count)
		VALUES ($1::uuid, lower($2::text), CURRENT_DATE, 0, 1)
		ON CONFLICT (organization_id, domain, sending_day)
		DO UPDATE SET successful_count = sending_domain_daily_stats.successful_count + 1,
		              updated_at = now()
	`, row.OrganizationID, domainOf(row.SenderEmail)); err != nil {
		return fmt.Errorf("record successful sending-domain delivery: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipients
		SET status = 'submitted', message_id = $3::text
		WHERE campaign_id = $1::uuid AND contact_id = $2::uuid
	`, row.CampaignID, row.ContactID, transportMessageID); err != nil {
		return fmt.Errorf("mark recipient submitted: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'submitted'::text, '{}'::jsonb)
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID); err != nil {
		return fmt.Errorf("record submitted event: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (organization_id, metric, quantity)
		VALUES ($1::uuid, 'email_submitted'::text, 1)
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

	if err := w.releaseCampaignQuota(ctx, row); err != nil {
		return 30 * time.Second, err
	}
	delay := backoff(attempts)
	_, err := w.pool.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'deferred',
		    attempts = $2,
		    next_attempt_at = now() + $3::interval,
		    failure_started_at = COALESCE(failure_started_at, now()),
		    processing_started_at = NULL,
		    nats_publish_claimed_at = NULL,
		    nats_published_at = NULL,
		    last_error = $4,
		    updated_at = now()
		WHERE id = $1
	`, row.ID, attempts, fmt.Sprintf("%d seconds", int(delay.Seconds())), truncateError(sendErr))
	if err != nil {
		return 30 * time.Second, fmt.Errorf("record deferred job: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET delivery_status = 'deferred', deferred_at = now(), updated_at = now()
		WHERE delivery_job_id = $1
	`, row.ID); err != nil {
		w.logger.Warn("update recipient tracking deferral", "job_id", row.ID, "error", err)
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO email_tracking_events
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id, tracking_id,
			 event_type, event_metadata, event_hash)
		SELECT $1::uuid, $2::uuid, recipient_id, $3::uuid, $4::uuid,
		       tracking_id, 'deferred'::text,
		       jsonb_build_object('error', $5::text, 'attempt', $6::integer),
		       md5($4::uuid::text || ':deferred:' || $6::integer::text)
		FROM campaign_recipient_tracking
		WHERE delivery_job_id = $4::uuid
		ON CONFLICT DO NOTHING
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, truncateError(sendErr), attempts); err != nil {
		w.logger.Warn("record recipient tracking deferral", "job_id", row.ID, "error", err)
	}

	_, _ = w.pool.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, metadata)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'deferred'::text,
		        jsonb_build_object('error', $5::text, 'attempt', $6::integer))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, truncateError(sendErr), attempts)

	observability.DeliveryJobs.WithLabelValues("deferred").Inc()
	w.logger.Warn("delivery deferred",
		"job_id", row.ID,
		"attempt", attempts,
		"retry_in", delay.String(),
		"error", sendErr,
	)
	return 0, nil
}

func (w *Worker) failJob(ctx context.Context, row *jobRow, cause error) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failure transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	message := truncateError(cause)
	if err := w.releaseCampaignQuotaTx(ctx, tx, row); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'failed',
		    attempts = attempts + 1,
		    processing_started_at = NULL,
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
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'failed'::text,
		        jsonb_build_object('error', $5::text))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message); err != nil {
		return fmt.Errorf("record failed event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET delivery_status = 'failed', failed_at = now(), updated_at = now()
		WHERE delivery_job_id = $1
	`, row.ID); err != nil {
		return fmt.Errorf("update recipient tracking failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_tracking_events
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 tracking_id, event_type, event_metadata, event_hash)
		SELECT $1::uuid, $2::uuid, recipient_id, $3::uuid, $4::uuid,
		       tracking_id, 'failed'::text,
		       jsonb_build_object('error', $5::text),
		       md5($4::uuid::text || ':failed')
		FROM campaign_recipient_tracking
		WHERE delivery_job_id = $4::uuid
		ON CONFLICT DO NOTHING
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message); err != nil {
		return fmt.Errorf("record recipient tracking failed event: %w", err)
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
	if err := w.releaseCampaignQuotaTx(ctx, tx, row); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'bounced',
		    attempts = attempts + 1,
		    processing_started_at = NULL,
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
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'bounced'::text,
		        jsonb_build_object('error', $5::text, 'bounce_type', $6::text, 'smtp_code', $7::text))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message, bounceType, smtpCode); err != nil {
		return fmt.Errorf("record bounced event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET delivery_status = 'bounced', bounced_at = now(), updated_at = now()
		WHERE delivery_job_id = $1
	`, row.ID); err != nil {
		return fmt.Errorf("update recipient tracking bounced: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_tracking_events
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 tracking_id, event_type, event_metadata, event_hash)
		SELECT $1::uuid, $2::uuid, recipient_id, $3::uuid, $4::uuid,
		       tracking_id, 'bounced'::text,
		       jsonb_build_object('error', $5::text, 'bounce_type', $6::text, 'smtp_code', $7::text),
		       md5($4::uuid::text || ':bounced')
		FROM campaign_recipient_tracking
		WHERE delivery_job_id = $4::uuid
		ON CONFLICT DO NOTHING
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message, bounceType, smtpCode); err != nil {
		return fmt.Errorf("record recipient tracking bounced event: %w", err)
	}

	observability.DeliveryJobs.WithLabelValues("bounced").Inc()
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit bounce: %w", err)
	}

	if err := w.checkCampaignReputation(ctx, row.CampaignID, row.OrganizationID); err != nil {
		w.logger.Error("evaluate campaign reputation after bounce", "campaign_id", row.CampaignID, "error", err)
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
	if err := w.releaseCampaignQuotaTx(ctx, tx, row); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'failed',
		    attempts = attempts + 1,
		    dead_lettered_at = now(),
		    processing_started_at = NULL,
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
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'failed'::text,
		        jsonb_build_object('error', $5::text, 'dead_lettered', true, 'reason', $6::text))
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message, reason); err != nil {
		return fmt.Errorf("record dead-lettered event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET delivery_status = 'failed', failed_at = now(), updated_at = now()
		WHERE delivery_job_id = $1
	`, row.ID); err != nil {
		return fmt.Errorf("update recipient tracking dead-lettered: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO email_tracking_events
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 tracking_id, event_type, event_metadata, event_hash)
		SELECT $1::uuid, $2::uuid, recipient_id, $3::uuid, $4::uuid,
		       tracking_id, 'failed'::text,
		       jsonb_build_object('error', $5::text, 'dead_lettered', true, 'reason', $6::text),
		       md5($4::uuid::text || ':dead-lettered')
		FROM campaign_recipient_tracking
		WHERE delivery_job_id = $4::uuid
		ON CONFLICT DO NOTHING
	`, row.OrganizationID, row.CampaignID, row.ContactID, row.ID, message, reason); err != nil {
		return fmt.Errorf("record recipient tracking dead-lettered event: %w", err)
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
	var workspaceStatus string
	if err := w.pool.QueryRow(ctx, `
		SELECT status FROM organizations WHERE id = $1
	`, organizationID).Scan(&workspaceStatus); err != nil {
		if err == pgx.ErrNoRows {
			return errors.New("workspace is unavailable")
		}
		return fmt.Errorf("load workspace status: %w", err)
	}
	if workspaceStatus == "suspended" {
		return fmt.Errorf("%w: %s", errWorkspaceSuspended, workspaceStatus)
	}
	if workspaceStatus != "active" {
		return fmt.Errorf("workspace is %s", workspaceStatus)
	}
	var pausedUntil *time.Time
	if err := w.pool.QueryRow(ctx, `
		SELECT paused_until
		FROM organization_settings
		WHERE organization_id = $1
	`, organizationID).Scan(&pausedUntil); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return fmt.Errorf("load organization settings: %w", err)
	}

	if pausedUntil != nil && pausedUntil.After(time.Now()) {
		return fmt.Errorf("organization sending paused until %s", pausedUntil.Format(time.RFC3339))
	}
	return nil
}

func (w *Worker) reserveCampaignQuota(ctx context.Context, row *jobRow) (bool, time.Time, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("begin campaign quota reservation: %w", err)
	}
	defer tx.Rollback(ctx)

	var domainID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM sending_domains
		WHERE organization_id = $1 AND lower(domain) = lower($2)
		  AND status = 'verified' AND NOT owner_conflict
		  AND admin_disabled_at IS NULL
		ORDER BY created_at
		LIMIT 1
		FOR UPDATE
	`, row.OrganizationID, domainOf(row.SenderEmail)).Scan(&domainID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, time.Time{}, quota.ErrDomainNotFound
		}
		return false, time.Time{}, fmt.Errorf("find verified campaign sending domain: %w", err)
	}

	reservationID := row.QuotaReservationID
	if reservationID == "" {
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&reservationID); err != nil {
			return false, time.Time{}, fmt.Errorf("create campaign quota reservation id: %w", err)
		}
	}
	_, err = quota.Reserve(
		ctx, tx, domainID, quota.StreamMarketing, quota.SourceCampaignDelivery,
		reservationID, 1, int64(w.cfg.PlatformMaxDomainRate),
	)
	if err != nil {
		var exceeded *quota.ExceededError
		if !errors.As(err, &exceeded) {
			return false, time.Time{}, err
		}
		warmupTag, warmupErr := tx.Exec(ctx, `
			UPDATE delivery_jobs SET warmup_reserved_on = NULL
			WHERE id = $1 AND warmup_reserved_on = CURRENT_DATE
		`, row.ID)
		if warmupErr != nil {
			return false, time.Time{}, fmt.Errorf("release warm-up slot after quota deferral: %w", warmupErr)
		}
		if warmupTag.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE sending_domain_daily_stats
				SET reserved_count = GREATEST(reserved_count - 1, 0), updated_at = now()
				WHERE organization_id = $1 AND lower(domain) = lower($2)
				  AND sending_day = CURRENT_DATE
			`, row.OrganizationID, domainOf(row.SenderEmail)); err != nil {
				return false, time.Time{}, fmt.Errorf("release warm-up usage after quota deferral: %w", err)
			}
		}
		if _, updateErr := tx.Exec(ctx, `
			UPDATE delivery_jobs
			SET status = 'deferred', next_attempt_at = $2, quota_deferred = true,
			    failure_started_at = NULL, processing_started_at = NULL,
			    nats_publish_claimed_at = NULL, nats_published_at = NULL,
			    quota_reservation_id = NULL, last_error = $3, updated_at = now()
			WHERE id = $1 AND status IN ('queued', 'processing', 'deferred')
		`, row.ID, exceeded.ResetAt, exceeded.Error()); updateErr != nil {
			return false, time.Time{}, fmt.Errorf("defer campaign delivery for quota: %w", updateErr)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, time.Time{}, fmt.Errorf("commit campaign quota deferral: %w", err)
		}
		row.QuotaDeferred = true
		row.QuotaReservationID = ""
		return true, exceeded.ResetAt, nil
	}

	tag, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET quota_reservation_id = $2, quota_deferred = false, updated_at = now()
		WHERE id = $1 AND status IN ('queued', 'processing', 'deferred')
	`, row.ID, reservationID)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("save campaign quota reservation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := quota.Release(ctx, tx, quota.StreamMarketing, quota.SourceCampaignDelivery, reservationID); err != nil {
			return false, time.Time{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, time.Time{}, fmt.Errorf("commit unused campaign quota reservation: %w", err)
		}
		return true, time.Now(), nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, time.Time{}, fmt.Errorf("commit campaign quota reservation: %w", err)
	}
	row.QuotaReservationID = reservationID
	row.QuotaDeferred = false
	return false, time.Time{}, nil
}

func (w *Worker) releaseCampaignQuota(ctx context.Context, row *jobRow) error {
	if row.QuotaReservationID == "" {
		return nil
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin campaign quota release: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := w.releaseCampaignQuotaTx(ctx, tx, row); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit campaign quota release: %w", err)
	}
	row.QuotaReservationID = ""
	return nil
}

func (w *Worker) releaseCampaignQuotaTx(ctx context.Context, tx pgx.Tx, row *jobRow) error {
	if row.QuotaReservationID == "" {
		return nil
	}
	if err := quota.Release(ctx, tx, quota.StreamMarketing, quota.SourceCampaignDelivery, row.QuotaReservationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs SET quota_reservation_id = NULL
		WHERE id = $1 AND quota_reservation_id = $2
	`, row.ID, row.QuotaReservationID); err != nil {
		return fmt.Errorf("clear campaign quota reservation: %w", err)
	}
	return nil
}

func (w *Worker) releaseWarmupReservation(ctx context.Context, row *jobRow) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin warm-up reservation release: %w", err)
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET warmup_reserved_on = NULL, updated_at = now()
		WHERE id = $1 AND warmup_reserved_on = CURRENT_DATE
	`, row.ID)
	if err != nil {
		return fmt.Errorf("clear expired job warm-up reservation: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE sending_domain_daily_stats
			SET reserved_count = GREATEST(reserved_count - 1, 0), updated_at = now()
			WHERE organization_id = $1 AND lower(domain) = lower($2)
			  AND sending_day = CURRENT_DATE
		`, row.OrganizationID, domainOf(row.SenderEmail)); err != nil {
			return fmt.Errorf("release expired job warm-up usage: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit warm-up reservation release: %w", err)
	}
	return nil
}

func (w *Worker) reserveWarmupSlot(ctx context.Context, row *jobRow) (bool, error) {
	domain := strings.ToLower(domainOf(row.SenderEmail))
	if domain == "" {
		return false, fmt.Errorf("campaign sender email has no domain")
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin warm-up reservation: %w", err)
	}
	defer tx.Rollback(ctx)

	var alreadyReserved bool
	var currentDate time.Time
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(warmup_reserved_on = CURRENT_DATE, false), CURRENT_DATE
		FROM delivery_jobs
		WHERE id = $1
		FOR UPDATE
	`, row.ID).Scan(&alreadyReserved, &currentDate); err != nil {
		return false, fmt.Errorf("read warm-up reservation: %w", err)
	}
	if alreadyReserved {
		return false, tx.Commit(ctx)
	}

	var warmupProfile string
	var overrideValue pgtype.Int4
	var overrideExpiresAt pgtype.Timestamptz
	var graduatedAt pgtype.Timestamptz
	var warmupDomainID string
	var dailyDefault, dailyOverride pgtype.Int8
	var unlimited bool
	var now time.Time
	if err := tx.QueryRow(ctx, `
		SELECT sd.id::text, sd.warmup_profile, sd.warmup_daily_cap_override,
		       sd.warmup_override_expires_at, sd.warmup_graduated_at,
		       pd.daily_limit, dq.daily_limit,
		       COALESCE(dq.unlimited, false), now()
		FROM sending_domains sd
		LEFT JOIN platform_quota_defaults pd ON pd.stream = 'marketing'
		LEFT JOIN domain_quotas dq ON dq.domain_id = sd.id AND dq.stream = 'marketing'
		WHERE sd.organization_id = $1 AND lower(sd.domain) = $2
		ORDER BY sd.created_at
		LIMIT 1
	`, row.OrganizationID, domain).Scan(
		&warmupDomainID, &warmupProfile, &overrideValue, &overrideExpiresAt,
		&graduatedAt,
		&dailyDefault, &dailyOverride, &unlimited, &now,
	); err != nil && err != pgx.ErrNoRows {
		return false, fmt.Errorf("load sending-domain warm-up profile: %w", err)
	}
	if warmupProfile == "" {
		warmupProfile = "standard"
	}
	var revoked bool
	var reputation domainwarmup.Reputation
	if warmupDomainID != "" {
		revoked, reputation, err = domainwarmup.RevokeIfUnsafe(ctx, tx, warmupDomainID)
		if err != nil {
			return false, fmt.Errorf("check warm-up graduation reputation: %w", err)
		}
	}
	if revoked {
		graduatedAt.Valid = false
		w.logger.Warn("warm-up graduation automatically revoked",
			"domain", domain,
			"bounce_rate_7_days", reputation.BounceRate,
			"complaint_rate_7_days", reputation.ComplaintRate,
		)
	}
	var successfulDays int
	qualifiedDayFilter := ""
	if warmupProfile == "accelerated" {
		qualifiedDayFilter = " AND bounce_count::numeric / NULLIF(successful_count, 0) < 0.02"
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND domain = $2
		  AND sending_day < CURRENT_DATE AND successful_count > 0
	`+qualifiedDayFilter, row.OrganizationID, domain).Scan(&successfulDays); err != nil {
		return false, fmt.Errorf("count successful warm-up days: %w", err)
	}
	curveCap := delivery.WarmupDailyCap(warmupProfile, w.cfg.WarmupStartPerDay, w.cfg.WarmupGrowthFactor, w.cfg.MaxDailyEmails, successfulDays)
	var dailyQuota *int64
	if !unlimited {
		effectiveDailyQuota := dailyOverride
		if !effectiveDailyQuota.Valid {
			effectiveDailyQuota = dailyDefault
		}
		if effectiveDailyQuota.Valid {
			dailyQuota = &effectiveDailyQuota.Int64
		}
	}
	if graduatedAt.Valid {
		curveCap = delivery.GraduatedWarmupDailyCap(dailyQuota, unlimited)
	}
	var override *int
	if overrideValue.Valid {
		value := int(overrideValue.Int32)
		override = &value
	}
	var expiresAt *time.Time
	if overrideExpiresAt.Valid {
		expiresAt = &overrideExpiresAt.Time
	}
	cap := delivery.EffectiveWarmupDailyCap(
		curveCap, override, expiresAt, now, dailyQuota, unlimited,
	)

	if _, err := tx.Exec(ctx, `
		INSERT INTO sending_domain_daily_stats (organization_id, domain, sending_day)
		VALUES ($1, $2, CURRENT_DATE)
		ON CONFLICT (organization_id, domain, sending_day) DO NOTHING
	`, row.OrganizationID, domain); err != nil {
		return false, fmt.Errorf("create warm-up usage row: %w", err)
	}

	var reserved int
	if err := tx.QueryRow(ctx, `
		SELECT reserved_count
		FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND domain = $2 AND sending_day = CURRENT_DATE
		FOR UPDATE
	`, row.OrganizationID, domain).Scan(&reserved); err != nil {
		return false, fmt.Errorf("load warm-up usage: %w", err)
	}
	if reserved >= cap {
		if _, err := tx.Exec(ctx, `
			UPDATE delivery_jobs
			SET status = 'deferred',
			    next_attempt_at = date_trunc('day', now()) + interval '1 day',
			    failure_started_at = NULL, processing_started_at = NULL,
			    nats_publish_claimed_at = NULL,
			    nats_published_at = NULL,
			    last_error = 'sending domain warm-up daily cap reached',
			    updated_at = now()
			WHERE id = $1 AND status IN ('queued', 'processing', 'deferred')
		`, row.ID); err != nil {
			return false, fmt.Errorf("defer job beyond warm-up cap: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit warm-up deferral: %w", err)
		}
		return true, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE sending_domain_daily_stats
		SET reserved_count = reserved_count + 1, updated_at = now()
		WHERE organization_id = $1 AND domain = $2 AND sending_day = CURRENT_DATE
	`, row.OrganizationID, domain); err != nil {
		return false, fmt.Errorf("reserve warm-up slot: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET warmup_reserved_on = CURRENT_DATE, updated_at = now()
		WHERE id = $1
	`, row.ID); err != nil {
		return false, fmt.Errorf("mark warm-up reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit warm-up reservation: %w", err)
	}
	return false, nil
}

func warmupDailyCap(start int, growth float64, maximum, successfulDays int) int {
	return delivery.WarmupDailyCap("standard", start, growth, maximum, successfulDays)
}

func (w *Worker) recipientLimiter(domain string) *rate.Limiter {
	domain = strings.ToLower(domain)
	w.domainMu.Lock()
	defer w.domainMu.Unlock()
	if limiter := w.recipientRates[domain]; limiter != nil {
		return limiter
	}

	perSecond := w.cfg.RecipientDomainDefault
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts); i++ {
		if configured := w.cfg.RecipientDomainRates[strings.Join(parts[i:], ".")]; configured > 0 {
			perSecond = configured
			break
		}
	}
	limiter := rate.NewLimiter(rate.Limit(perSecond), 1)
	w.recipientRates[domain] = limiter
	return limiter
}

func completeSenderProfile(row *jobRow) bool {
	return strings.TrimSpace(row.SenderCompany) != "" &&
		strings.TrimSpace(row.SenderWebsite) != "" &&
		strings.TrimSpace(row.SenderSupportEmail) != ""
}

func senderPostalAddress(row *jobRow) string {
	parts := []string{strings.TrimSpace(row.SenderStreet), strings.TrimSpace(row.SenderCity)}
	locality := strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(row.SenderRegion),
		strings.TrimSpace(row.SenderPostalCode),
	}, " "))
	if locality != "" {
		parts = append(parts, locality)
	}
	parts = append(parts, strings.TrimSpace(row.SenderCountry))
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			values = append(values, part)
		}
	}
	return strings.Join(values, ", ")
}

func (w *Worker) checkCampaignReputation(ctx context.Context, campaignID, organizationID string) error {
	var total, bounced, complained int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status = 'bounced'),
		       count(*) FILTER (WHERE status = 'complained')
		FROM campaign_recipients
		WHERE organization_id = $1 AND campaign_id = $2
	`, organizationID, campaignID).Scan(&total, &bounced, &complained); err != nil {
		return fmt.Errorf("count campaign reputation events: %w", err)
	}
	if total == 0 || (float64(bounced)/float64(total) <= w.cfg.MaxBounceRate &&
		float64(complained)/float64(total) <= w.cfg.MaxComplaintRate) {
		return nil
	}

	message := fmt.Sprintf("Campaign paused automatically: bounce rate %.2f%% (limit %.2f%%), complaint rate %.3f%% (limit %.3f%%).",
		100*float64(bounced)/float64(total), 100*w.cfg.MaxBounceRate,
		100*float64(complained)/float64(total), 100*w.cfg.MaxComplaintRate)
	var pausedID string
	err := w.pool.QueryRow(ctx, `
		UPDATE campaigns SET status = 'paused', paused_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status IN ('processing', 'sending')
		RETURNING id
	`, campaignID, organizationID).Scan(&pausedID)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pause campaign after reputation threshold: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO campaign_alerts (organization_id, campaign_id, alert_type, message)
		VALUES ($1, $2, 'reputation_pause', $3)
	`, organizationID, campaignID, message); err != nil {
		return fmt.Errorf("create campaign reputation alert: %w", err)
	}
	w.logger.Warn("campaign paused after reputation threshold", "campaign_id", campaignID, "message", message)
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
	if err := w.releaseCampaignQuotaTx(ctx, tx, row); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs
		SET status = 'cancelled', last_error = $2,
		    failure_started_at = NULL, processing_started_at = NULL,
		    nats_publish_claimed_at = NULL, nats_published_at = NULL,
		    updated_at = now()
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
			resumed, err := w.campaigns.RepublishPendingWithStaleTimeout(ctx, 1000, w.cfg.NATSStalePublishTimeout)
			if err != nil {
				w.logger.Error("resume deferred delivery jobs", "error", err)
			} else if resumed > 0 {
				w.logger.Info("resumed due delivery jobs", "jobs", resumed)
			}
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

func personalizationData(row *jobRow, unsubscribeURL string) map[string]string {
	data := map[string]string{
		"first_name":      row.FirstName,
		"last_name":       row.LastName,
		"email":           row.Email,
		"organization":    row.Organization,
		"unsubscribe_url": unsubscribeURL,
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
	if marker := strings.Index(document, `data-sendwise-compliance-footer="true"`); marker >= 0 {
		if start := strings.LastIndex(document[:marker], "<"); start >= 0 {
			return document[:start] + fragment + document[start:]
		}
	}
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
