package deliveryjourney

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/delivery"
	"golang.org/x/time/rate"
)

type QueueRecipient struct {
	Status       string
	RemoteHost   string
	SMTPCode     *int
	EnhancedCode string
	Reason       string
	Attempt      *int
	NextRetryAt  *time.Time
	RawType      string
}

type QueueMessage struct {
	ID         string
	ReturnPath string
	NextRetry  string
	Flags      QueueFlags
	Recipients map[string]QueueRecipient
}

type QueueFlags struct {
	DSN bool
}

type QueueSnapshot struct {
	Messages   []QueueMessage
	Complete   bool
	DSNInQueue int
}

type QueueReader interface {
	Snapshot(ctx context.Context) (QueueSnapshot, error)
}

type Result struct {
	OrganizationID string
	CampaignID     string
	RecipientID    string
	Status         string
}

type TickSummary struct {
	Candidates int
	QueueSize  int
	DSNInQueue int
	Found      int
	NotFound   int
	Errors     int
	Stages     map[string]int
	Duration   time.Duration
}

type journeyStore interface {
	Record(ctx context.Context, event Event) (bool, error)
	UpdateReconciled(ctx context.Context, organizationID, jobID string) error
}

type Reconciler struct {
	pool           *pgxpool.Pool
	store          journeyStore
	queue          QueueReader
	bounceDomains  []string
	logger         *slog.Logger
	infraFailLimit int
}

type ReconcilerOption func(*Reconciler)

func WithLogger(logger *slog.Logger) ReconcilerOption {
	return func(r *Reconciler) { r.logger = logger }
}

func WithLegacyBounceDomains(domains ...string) ReconcilerOption {
	return func(r *Reconciler) {
		for _, domain := range domains {
			domain = strings.ToLower(strings.TrimSpace(domain))
			if domain == "" {
				continue
			}
			r.bounceDomains = appendUniqueDomain(r.bounceDomains, domain)
		}
	}
}

func WithStore(store journeyStore) ReconcilerOption {
	return func(r *Reconciler) {
		if store != nil {
			r.store = store
		}
	}
}

func NewReconciler(pool *pgxpool.Pool, stalwartURL, username, password, bounceDomain string, opts ...ReconcilerOption) *Reconciler {
	return NewReconcilerWithReader(pool, newJMAPQueueReader(stalwartURL, username, password), bounceDomain, opts...)
}

func NewReconcilerWithReader(pool *pgxpool.Pool, queue QueueReader, bounceDomain string, opts ...ReconcilerOption) *Reconciler {
	r := &Reconciler{
		pool:           pool,
		store:          NewStore(pool),
		queue:          queue,
		bounceDomains:  normalizedDomains(bounceDomain),
		logger:         slog.Default(),
		infraFailLimit: 5,
	}
	for _, opt := range opts {
		opt(r)
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	if len(r.bounceDomains) == 0 {
		r.bounceDomains = normalizedDomains(bounceDomain)
	}
	return r
}

func (r *Reconciler) ReconcileBatch(ctx context.Context, limit int, stuckAfter time.Duration) ([]Result, error) {
	summary, results, err := r.reconcileBatch(ctx, limit, stuckAfter)
	r.logTick(summary)
	return results, err
}

func (r *Reconciler) ReconcileBatchWithSummary(ctx context.Context, limit int, stuckAfter time.Duration) (TickSummary, []Result, error) {
	summary, results, err := r.reconcileBatch(ctx, limit, stuckAfter)
	r.logTick(summary)
	return summary, results, err
}

func (r *Reconciler) reconcileBatch(ctx context.Context, limit int, stuckAfter time.Duration) (TickSummary, []Result, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	if stuckAfter <= 0 {
		stuckAfter = 30 * time.Minute
	}
	start := time.Now()
	summary := TickSummary{Stages: map[string]int{}}

	candidates, err := r.listCandidates(ctx, limit, stuckAfter)
	if err != nil {
		return summary, nil, fmt.Errorf("list stale delivery journeys: %w", err)
	}
	summary.Candidates = len(candidates)
	if len(candidates) == 0 {
		summary.Duration = time.Since(start)
		return summary, nil, nil
	}

	snapshot, err := r.queue.Snapshot(ctx)
	if err != nil {
		summary.Duration = time.Since(start)
		return summary, nil, fmt.Errorf("snapshot Stalwart queue: %w", err)
	}
	summary.QueueSize = len(snapshot.Messages)
	summary.DSNInQueue = snapshot.DSNInQueue
	index := indexSnapshot(snapshot)

	results := make([]Result, 0, len(candidates))
	consecutiveInfraFailures := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			summary.Duration = time.Since(start)
			r.logTick(summary)
			return summary, results, err
		}
		result, found, jobErr := r.reconcileCandidate(ctx, candidate, index, snapshot.Complete)
		if jobErr != nil {
			summary.Errors++
			if isInfraError(jobErr) {
				consecutiveInfraFailures++
			} else {
				consecutiveInfraFailures = 0
			}
			r.logger.Warn("delivery journey reconciliation failed for job",
				"job_id", candidate.jobID,
				"organization_id", candidate.organizationID,
				"recipient_id", candidate.recipientID,
				"reason", shortReason(jobErr),
			)
			if updateErr := r.markCandidateReconcileFailure(ctx, candidate); updateErr != nil {
				r.logger.Warn("delivery journey reconciliation failure could not be recorded",
					"job_id", candidate.jobID,
					"organization_id", candidate.organizationID,
					"reason", shortReason(updateErr),
				)
				if isInfraError(updateErr) {
					consecutiveInfraFailures++
				}
			}
			if consecutiveInfraFailures >= r.infraFailLimit {
				summary.Duration = time.Since(start)
				r.logTick(summary)
				return summary, results, fmt.Errorf("aborting delivery journey reconciliation after %d consecutive infrastructure failures: %w", consecutiveInfraFailures, jobErr)
			}
			continue
		}
		consecutiveInfraFailures = 0
		if result != nil {
			summary.Stages[result.Status]++
			results = append(results, *result)
		}
		if found {
			summary.Found++
		} else if result != nil {
			summary.NotFound++
		}
	}
	summary.Duration = time.Since(start)
	return summary, results, nil
}

func (r *Reconciler) logTick(summary TickSummary) {
	if r.logger == nil {
		return
	}
	r.logger.Info("delivery journey reconciliation tick",
		"candidates", summary.Candidates,
		"queue_size", summary.QueueSize,
		"dsn_in_queue", summary.DSNInQueue,
		"found", summary.Found,
		"not_found", summary.NotFound,
		"errors", summary.Errors,
		"stages", summary.Stages,
		"duration", summary.Duration.String(),
	)
}

type BackfillResult struct {
	Processed int
	HasMore   bool
	NextAfter string
	Counts    map[string]int
}

func (r *Reconciler) BackfillBatch(ctx context.Context, afterJobID string, limit int, dryRun bool) (BackfillResult, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
		SELECT dj.organization_id::text, dj.campaign_id::text, crt.recipient_id::text,
		       crt.contact_id::text, dj.id::text, COALESCE(crt.email, c.email, ''),
		       dj.status, COALESCE(dj.last_error, ''), dj.attempts,
		       COALESCE(v.token, '')
		FROM delivery_jobs dj
		JOIN campaign_recipient_tracking crt
		  ON crt.organization_id = dj.organization_id
		 AND crt.delivery_job_id = dj.id
		LEFT JOIN contacts c
		  ON c.organization_id = dj.organization_id
		 AND c.id = dj.contact_id
		LEFT JOIN delivery_verp_tokens v
		  ON v.organization_id = dj.organization_id
		 AND v.delivery_job_id = dj.id
		WHERE dj.status IN ('submitted', 'accepted')
		  AND crt.journey_status IN ('queued', 'processing', 'deferred_by_sendwise',
		                             'submitted_to_mail_server', 'relaying')
		  AND ($1::text = '' OR dj.id::text > $1::text)
		ORDER BY dj.id::text ASC
		LIMIT $2::integer
	`, afterJobID, limit+1)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("list delivery journey backfill candidates: %w", err)
	}
	defer rows.Close()

	type backfillCandidate struct {
		candidate
	}
	candidates := make([]backfillCandidate, 0, limit+1)
	for rows.Next() {
		var c backfillCandidate
		if err := rows.Scan(&c.organizationID, &c.campaignID, &c.recipientID, &c.contactID,
			&c.jobID, &c.email, &c.jobStatus, &c.lastError, &c.attempts, &c.token); err != nil {
			return BackfillResult{}, fmt.Errorf("scan delivery journey backfill candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return BackfillResult{}, fmt.Errorf("iterate delivery journey backfill candidates: %w", err)
	}

	result := BackfillResult{Counts: map[string]int{}}
	if len(candidates) > limit {
		result.HasMore = true
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		return result, nil
	}

	snapshot, err := r.queue.Snapshot(ctx)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("snapshot Stalwart queue for backfill: %w", err)
	}
	index := indexSnapshot(snapshot)

	for _, c := range candidates {
		result.Processed++
		result.NextAfter = c.jobID
		event, found, resolveErr := r.resolveCandidate(ctx, c.candidate, index, snapshot.Complete)
		if resolveErr != nil {
			return result, fmt.Errorf("resolve backfill candidate %s: %w", c.jobID, resolveErr)
		}
		if event == nil {
			if found {
				result.Counts["in_queue"]++
			} else {
				result.Counts["not_in_queue"]++
			}
			continue
		}
		result.Counts[event.Type]++
		if dryRun {
			continue
		}
		if _, err := r.store.Record(ctx, *event); err != nil {
			return result, fmt.Errorf("record backfilled delivery journey event for %s: %w", c.jobID, err)
		}
		if err := r.store.UpdateReconciled(ctx, c.organizationID, c.jobID); err != nil {
			return result, fmt.Errorf("mark backfilled delivery journey reconciled for %s: %w", c.jobID, err)
		}
	}
	return result, nil
}

type candidate struct {
	organizationID string
	campaignID     string
	recipientID    string
	contactID      string
	jobID          string
	email          string
	jobStatus      string
	lastError      string
	attempts       int
	token          string
}

func (r *Reconciler) listCandidates(ctx context.Context, limit int, stuckAfter time.Duration) ([]candidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT dj.organization_id::text, dj.campaign_id::text, crt.recipient_id::text,
		       crt.contact_id::text, dj.id::text, COALESCE(crt.email, c.email, ''),
		       dj.status, COALESCE(dj.last_error, ''), dj.attempts,
		       COALESCE(v.token, '')
		FROM delivery_jobs dj
		JOIN campaign_recipient_tracking crt
		  ON crt.organization_id = dj.organization_id
		 AND crt.delivery_job_id = dj.id
		LEFT JOIN contacts c
		  ON c.organization_id = dj.organization_id
		 AND c.id = dj.contact_id
		LEFT JOIN delivery_verp_tokens v
		  ON v.organization_id = dj.organization_id
		 AND v.delivery_job_id = dj.id
		WHERE dj.status IN ('submitted', 'accepted')
		  AND crt.journey_status IN ('submitted_to_mail_server', 'relaying')
		  AND COALESCE(crt.journey_last_stalwart_event_at, crt.accepted_at, dj.updated_at)
		      <= now() - $1::interval
		ORDER BY crt.journey_last_reconciled_at NULLS FIRST, crt.updated_at ASC
		LIMIT $2::integer
	`, fmt.Sprintf("%f seconds", stuckAfter.Seconds()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.organizationID, &c.campaignID, &c.recipientID, &c.contactID,
			&c.jobID, &c.email, &c.jobStatus, &c.lastError, &c.attempts, &c.token); err != nil {
			return nil, fmt.Errorf("scan stale delivery journey: %w", err)
		}
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

func (r *Reconciler) reconcileCandidate(ctx context.Context, c candidate, index snapshotIndex, snapshotComplete bool) (*Result, bool, error) {
	event, found, err := r.resolveCandidate(ctx, c, index, snapshotComplete)
	if err != nil {
		return nil, false, err
	}
	if event == nil {
		if found {
			if err := r.store.UpdateReconciled(ctx, c.organizationID, c.jobID); err != nil {
				return nil, true, fmt.Errorf("mark delivery journey reconciled: %w", err)
			}
			return nil, true, nil
		}
		// Not found and snapshot incomplete: do nothing.
		return nil, false, nil
	}
	if _, err := r.store.Record(ctx, *event); err != nil {
		return nil, found, fmt.Errorf("record reconciled delivery journey event: %w", err)
	}
	if err := r.store.UpdateReconciled(ctx, c.organizationID, c.jobID); err != nil {
		return nil, found, fmt.Errorf("mark delivery journey reconciled: %w", err)
	}
	return &Result{
		OrganizationID: c.organizationID,
		CampaignID:     c.campaignID,
		RecipientID:    c.recipientID,
		Status:         event.Type,
	}, found, nil
}

func (r *Reconciler) resolveCandidate(ctx context.Context, c candidate, index snapshotIndex, snapshotComplete bool) (*Event, bool, error) {
	recipient, found, err := r.lookupRecipientInSnapshot(ctx, c, index)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	if !found {
		if !snapshotComplete {
			return nil, false, nil
		}
		return &Event{
			OrganizationID: c.organizationID,
			JobID:          c.jobID,
			Type:           "accepted_final_state_unknown",
			Source:         "reconciliation",
			SourceEventID:  reconciliationSourceID("not_in_queue", c.jobID, now),
			OccurredAt:     now,
			Reason:         "left Stalwart queue; final outcome not reported",
			Metadata:       json.RawMessage(`{"reconciliation":"not_in_queue"}`),
		}, false, nil
	}

	stage := queueJourneyStage(recipient)
	if stage == "" {
		if err := r.store.UpdateReconciled(ctx, c.organizationID, c.jobID); err != nil {
			return nil, true, fmt.Errorf("mark delivery journey reconciled: %w", err)
		}
		return nil, true, nil
	}
	event := &Event{
		OrganizationID: c.organizationID,
		JobID:          c.jobID,
		Type:           stage,
		Source:         "reconciliation",
		SourceEventID:  reconciliationSourceID(stage, c.jobID, now),
		OccurredAt:     now,
		RemoteHost:     recipient.RemoteHost,
		SMTPCode:       recipient.SMTPCode,
		EnhancedCode:   recipient.EnhancedCode,
		Reason:         recipient.Reason,
		Attempt:        recipient.Attempt,
		NextRetryAt:    recipient.NextRetryAt,
		Metadata:       mustJSON(map[string]any{"reconciliation": "stalwart_jmap_queue", "queue_status": recipient.Status, "queue_type": recipient.RawType}),
	}
	return event, true, nil
}

func (r *Reconciler) lookupRecipientInSnapshot(_ context.Context, c candidate, index snapshotIndex) (QueueRecipient, bool, error) {
	if c.email == "" || c.token == "" {
		return QueueRecipient{}, false, nil
	}
	for _, domain := range r.bounceDomains {
		returnPath, err := delivery.FormatVERPReturnPath(domain, c.token)
		if err != nil {
			return QueueRecipient{}, false, fmt.Errorf("format VERP return path for %s: %w", c.jobID, err)
		}
		if recipient, ok := index.lookup(returnPath, c.email); ok {
			return recipient, true, nil
		}
	}
	return QueueRecipient{}, false, nil
}

func (r *Reconciler) markCandidateReconcileFailure(ctx context.Context, c candidate) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE campaign_recipient_tracking
		SET journey_last_reconciled_at = now(),
		    journey_attempts = GREATEST(COALESCE(journey_attempts, 0), $3::integer) + 1,
		    updated_at = now()
		WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
	`, c.organizationID, c.jobID, c.attempts)
	if err != nil {
		return fmt.Errorf("mark delivery journey reconcile failure: %w", err)
	}
	return nil
}

func reconciliationSourceID(stage, jobID string, now time.Time) string {
	return stage + ":" + jobID + ":" + now.UTC().Format(time.RFC3339Nano)
}


type snapshotIndex struct {
	byReturnPath map[string]map[string]QueueRecipient
}

func indexSnapshot(snapshot QueueSnapshot) snapshotIndex {
	idx := snapshotIndex{byReturnPath: make(map[string]map[string]QueueRecipient, len(snapshot.Messages))}
	for _, msg := range snapshot.Messages {
		if msg.Flags.DSN {
			continue
		}
		rp := strings.ToLower(strings.TrimSpace(msg.ReturnPath))
		if rp == "" {
			continue
		}
		recipients := idx.byReturnPath[rp]
		if recipients == nil {
			recipients = make(map[string]QueueRecipient, len(msg.Recipients))
			idx.byReturnPath[rp] = recipients
		}
		for addr, recipient := range msg.Recipients {
			recipients[strings.ToLower(strings.TrimSpace(addr))] = recipient
		}
	}
	return idx
}

func (idx snapshotIndex) lookup(returnPath, recipientEmail string) (QueueRecipient, bool) {
	recipients, ok := idx.byReturnPath[strings.ToLower(strings.TrimSpace(returnPath))]
	if !ok {
		return QueueRecipient{}, false
	}
	recipient, ok := recipients[strings.ToLower(strings.TrimSpace(recipientEmail))]
	return recipient, ok
}

func normalizedDomains(domains ...string) []string {
	out := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		out = appendUniqueDomain(out, domain)
	}
	return out
}

func appendUniqueDomain(domains []string, domain string) []string {
	for _, existing := range domains {
		if existing == domain {
			return domains
		}
	}
	return append(domains, domain)
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func shortReason(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.Join(strings.Fields(msg), " ")
	const max = 240
	if len(msg) > max {
		return msg[:max]
	}
	return msg
}

func isInfraError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(net.Error); ok {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"i/o timeout",
		"no such host",
		"network is unreachable",
		"tls handshake",
		"eof",
		"unauthorized",
		"forbidden",
		"http 401",
		"http 403",
		"authentication",
		"stalwart jmap returned http 401",
		"stalwart jmap returned http 403",
		"snapshot stalwart queue",
		"load stalwart jmap session",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func queueJourneyStage(recipient QueueRecipient) string {
	switch strings.ToLower(strings.TrimSpace(recipient.Status)) {
	case "scheduled", "relaying":
		return "relaying"
	case "temporaryfailure", "deferred_by_recipient":
		return "deferred_by_recipient"
	case "permanentfailure", "bounced":
		return "bounced"
	case "completed", "accepted_by_recipient_server":
		if recipient.SMTPCode != nil && *recipient.SMTPCode >= 400 && *recipient.SMTPCode < 500 {
			return "deferred_by_recipient"
		}
		if recipient.SMTPCode != nil && *recipient.SMTPCode >= 500 {
			return "bounced"
		}
		return "accepted_by_recipient_server"
	case "accepted_final_state_unknown":
		return "accepted_final_state_unknown"
	default:
		if recipient.SMTPCode != nil && *recipient.SMTPCode >= 400 && *recipient.SMTPCode < 500 {
			return "deferred_by_recipient"
		}
		if recipient.SMTPCode != nil && *recipient.SMTPCode >= 500 {
			return "bounced"
		}
		if strings.TrimSpace(recipient.Status) == "" {
			return ""
		}
		return "accepted_final_state_unknown"
	}
}

type rawQueueMessage struct {
	ID         string                     `json:"id"`
	ReturnPath string                     `json:"returnPath"`
	NextRetry  string                     `json:"nextRetry"`
	Flags      rawQueueFlags              `json:"flags"`
	Recipients map[string]rawQueueRecipient `json:"recipients"`
}

type rawQueueFlags struct {
	DSN bool `json:"dsn"`
}

type rawQueueRecipient struct {
	RetryCount int             `json:"retryCount"`
	RetryDue   string          `json:"retryDue"`
	NotifyCount int            `json:"notifyCount"`
	NotifyDue  string          `json:"notifyDue"`
	QueueName  string          `json:"queueName"`
	Status     json.RawMessage `json:"status"`
}

type queueStatusObject struct {
	Type             string `json:"@type"`
	ErrorType        string `json:"errorType"`
	ErrorMessage     string `json:"errorMessage"`
	ErrorCommand     string `json:"errorCommand"`
	ResponseHostname string `json:"responseHostname"`
	ResponseCode     *int   `json:"responseCode"`
	ResponseEnhanced string `json:"responseEnhanced"`
	ResponseMessage  string `json:"responseMessage"`
}

func parseQueueMessage(raw rawQueueMessage) QueueMessage {
	msg := QueueMessage{
		ID:         raw.ID,
		ReturnPath: raw.ReturnPath,
		NextRetry:  raw.NextRetry,
		Flags:      QueueFlags{DSN: raw.Flags.DSN},
		Recipients: make(map[string]QueueRecipient, len(raw.Recipients)),
	}
	for addr, r := range raw.Recipients {
		recipient := parseQueueRecipient(r, raw.NextRetry)
		msg.Recipients[addr] = recipient
	}
	return msg
}

func parseQueueRecipient(raw rawQueueRecipient, messageNextRetry string) QueueRecipient {
	status, rawType, remoteHost, smtpCode, enhanced, reason := parseRecipientStatus(raw.Status)
	attempt := raw.RetryCount + 1
	recipient := QueueRecipient{
		Status:       status,
		RemoteHost:   remoteHost,
		SMTPCode:     smtpCode,
		EnhancedCode: enhanced,
		Reason:       reason,
		Attempt:      &attempt,
		RawType:      rawType,
	}
	retry := firstNonEmpty(raw.RetryDue, messageNextRetry)
	if parsed, err := parseQueueTime(retry); err == nil {
		recipient.NextRetryAt = &parsed
	}
	return recipient
}

func parseRecipientStatus(raw json.RawMessage) (status string, rawType string, remoteHost string, smtpCode *int, enhanced string, reason string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "accepted_final_state_unknown", "", "", nil, "", "missing Stalwart queue recipient status"
	}

	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		status = strings.ToLower(strings.TrimSpace(str))
		rawType = strings.TrimSpace(str)
		return status, rawType, "", nil, "", ""
	}

	var obj queueStatusObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Unknown shape: never fail decode.
		return "accepted_final_state_unknown", "", "", nil, "", "unparseable Stalwart queue recipient status"
	}

	rawType = strings.TrimSpace(obj.Type)
	status = mapQueueStatusType(rawType)
	remoteHost = strings.TrimSpace(obj.ResponseHostname)
	smtpCode = obj.ResponseCode
	enhanced = strings.TrimSpace(obj.ResponseEnhanced)
	reason = strings.TrimSpace(obj.ResponseMessage)
	if reason == "" {
		reason = strings.TrimSpace(obj.ErrorMessage)
	}
	if reason == "" {
		reason = strings.TrimSpace(obj.ErrorType)
	}
	if status == "accepted_final_state_unknown" && rawType != "" && !strings.Contains(strings.ToLower(reason), strings.ToLower(rawType)) {
		if reason == "" {
			reason = "unknown Stalwart queue status @type: " + rawType
		} else {
			reason = reason + " (unknown @type: " + rawType + ")"
		}
	}
	return status, rawType, remoteHost, smtpCode, enhanced, reason
}

func mapQueueStatusType(rawType string) string {
	// Stalwart JMAP queue recipient status @type values observed on the pinned
	// deployment (Scheduled/TemporaryFailure/PermanentFailure/Completed). Unknown
	// @types are mapped safely to accepted_final_state_unknown with the raw value.
	switch strings.ToLower(strings.TrimSpace(rawType)) {
	case "scheduled":
		return "relaying"
	case "temporaryfailure":
		return "deferred_by_recipient"
	case "permanentfailure":
		return "bounced"
	case "completed":
		return "accepted_by_recipient_server"
	default:
		return "accepted_final_state_unknown"
	}
}

func parseQueueTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	return time.Parse(time.RFC3339, value)
}


func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type jmapQueueReader struct {
	endpoint  string
	username  string
	password  string
	client    *http.Client
	limiter   *rate.Limiter
	sessionURL string
}

func newJMAPQueueReader(baseURL, username, password string) *jmapQueueReader {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &jmapQueueReader{
		endpoint:   base + "/jmap/",
		sessionURL: base + "/jmap/session",
		username:   username,
		password:   password,
		client:     &http.Client{Timeout: 10 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(2), 2),
	}
}

func (c *jmapQueueReader) Snapshot(ctx context.Context) (QueueSnapshot, error) {
	maxObjectsInGet, err := c.maxObjectsInGet(ctx)
	if err != nil {
		return QueueSnapshot{}, err
	}
	if maxObjectsInGet < 1 {
		maxObjectsInGet = 50
	}

	snapshot := QueueSnapshot{Complete: true}
	position := 0
	for {
		if err := ctx.Err(); err != nil {
			return QueueSnapshot{}, err
		}
		queryRaw, err := c.call(ctx, []any{[]any{
			"x:QueuedMessage/query",
			map[string]any{
				"position": position,
				"limit":    maxObjectsInGet,
			},
			"query",
		}})
		if err != nil {
			return QueueSnapshot{}, fmt.Errorf("query Stalwart queued messages: %w", err)
		}
		queryResult, err := methodResult(queryRaw, "query")
		if err != nil {
			return QueueSnapshot{}, fmt.Errorf("query Stalwart queued messages: %w", err)
		}
		var queryData struct {
			IDs      []string `json:"ids"`
			Total    int      `json:"total"`
			Position int      `json:"position"`
		}
		if err := json.Unmarshal(queryResult, &queryData); err != nil {
			return QueueSnapshot{}, fmt.Errorf("decode Stalwart queued-message query: %w", err)
		}
		if len(queryData.IDs) == 0 {
			break
		}

		getRaw, err := c.call(ctx, []any{[]any{
			"x:QueuedMessage/get",
			map[string]any{
				"ids": queryData.IDs,
				"properties": []string{
					"id", "createdAt", "nextRetry", "nextNotify", "blobId", "returnPath",
					"recipients", "flags", "size",
				},
			},
			"get",
		}})
		if err != nil {
			return QueueSnapshot{}, fmt.Errorf("get Stalwart queued messages: %w", err)
		}
		getResult, err := methodResult(getRaw, "get")
		if err != nil {
			return QueueSnapshot{}, fmt.Errorf("get Stalwart queued messages: %w", err)
		}
		var getData struct {
			List []rawQueueMessage `json:"list"`
			NotFound []string       `json:"notFound"`
		}
		if err := json.Unmarshal(getResult, &getData); err != nil {
			return QueueSnapshot{}, fmt.Errorf("decode Stalwart queued-message details: %w", err)
		}

		for _, raw := range getData.List {
			msg := parseQueueMessage(raw)
			if msg.Flags.DSN {
				snapshot.DSNInQueue++
			}
			snapshot.Messages = append(snapshot.Messages, msg)
		}

		position += len(queryData.IDs)
		if len(queryData.IDs) < maxObjectsInGet {
			break
		}
		if queryData.Total > 0 && position >= queryData.Total {
			break
		}
	}

	return snapshot, nil
}

func (c *jmapQueueReader) maxObjectsInGet(ctx context.Context) (int, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return 0, fmt.Errorf("wait for Stalwart JMAP rate limit: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.sessionURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build Stalwart JMAP session request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("load Stalwart JMAP session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, fmt.Errorf("read Stalwart JMAP session: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("Stalwart JMAP session returned HTTP %d", resp.StatusCode)
	}
	var session struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &session); err != nil {
		return 0, fmt.Errorf("decode Stalwart JMAP session: %w", err)
	}
	coreRaw := session.Capabilities["urn:ietf:params:jmap:core"]
	if len(coreRaw) == 0 {
		return 0, fmt.Errorf("Stalwart JMAP session missing urn:ietf:params:jmap:core capability")
	}
	var core struct {
		MaxObjectsInGet int `json:"maxObjectsInGet"`
	}
	if err := json.Unmarshal(coreRaw, &core); err != nil {
		return 0, fmt.Errorf("decode Stalwart JMAP core capability: %w", err)
	}
	if core.MaxObjectsInGet < 1 {
		return 0, fmt.Errorf("Stalwart JMAP session maxObjectsInGet is invalid")
	}
	return core.MaxObjectsInGet, nil
}

func (c *jmapQueueReader) call(ctx context.Context, calls []any) (json.RawMessage, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("wait for Stalwart JMAP rate limit: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:stalwart:jmap"},
		"methodCalls": calls,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Stalwart JMAP request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build Stalwart JMAP request: %w", err)
	}
	request.SetBasicAuth(c.username, c.password)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send Stalwart JMAP request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read Stalwart JMAP response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Stalwart JMAP returned HTTP %d", response.StatusCode)
	}
	var result struct {
		MethodResponses []json.RawMessage `json:"methodResponses"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("decode Stalwart JMAP response: %w", err)
	}
	return json.Marshal(result.MethodResponses)
}

func methodResult(raw json.RawMessage, callID string) (json.RawMessage, error) {
	var responses []json.RawMessage
	if err := json.Unmarshal(raw, &responses); err != nil {
		return nil, fmt.Errorf("decode Stalwart method responses: %w", err)
	}
	for _, response := range responses {
		var parts []json.RawMessage
		if err := json.Unmarshal(response, &parts); err != nil || len(parts) != 3 {
			continue
		}
		var id string
		if err := json.Unmarshal(parts[2], &id); err != nil || id != callID {
			continue
		}
		if len(parts[0]) > 0 && string(parts[0]) == "\"error\"" {
			return nil, fmt.Errorf("Stalwart JMAP call %q failed: %s", callID, string(parts[1]))
		}
		return parts[1], nil
	}
	return nil, fmt.Errorf("Stalwart JMAP response omitted call %q", callID)
}
