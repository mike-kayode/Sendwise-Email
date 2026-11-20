package deliveryjourney

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
}

type QueueReader interface {
	LookupRecipient(ctx context.Context, returnPath, recipient string) (QueueRecipient, bool, error)
}

type Reconciler struct {
	pool         *pgxpool.Pool
	reader       QueueReader
	bounceDomain string
	limiter      *rate.Limiter
}

func NewReconciler(pool *pgxpool.Pool, managementURL, username, password, bounceDomain string) *Reconciler {
	limiter := rate.NewLimiter(rate.Limit(2), 1)
	reader := &jmapQueueReader{
		endpoint: strings.TrimRight(managementURL, "/") + "/jmap/",
		username: username,
		password: password,
		client:   &http.Client{Timeout: 15 * time.Second},
		limiter:  limiter,
	}
	return &Reconciler{
		pool:         pool,
		reader:       reader,
		bounceDomain: bounceDomain,
		limiter:      limiter,
	}
}

func NewReconcilerWithReader(pool *pgxpool.Pool, reader QueueReader, bounceDomain string) *Reconciler {
	return &Reconciler{
		pool:         pool,
		reader:       reader,
		bounceDomain: bounceDomain,
		limiter:      rate.NewLimiter(rate.Limit(2), 1),
	}
}

type ReconcileCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type ReconcileResult struct {
	JobID  string
	Status string
}

type BackfillResult struct {
	Counts     map[string]int `json:"counts"`
	Processed  int            `json:"processed"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
	DryRun     bool           `json:"dry_run"`
}

type backfillCandidate struct {
	organizationID string
	jobID          string
	email          string
	token          string
}

func (r *Reconciler) ReconcileBatch(ctx context.Context, limit int, staleAfter time.Duration) ([]ReconcileResult, error) {
	if limit < 1 {
		return nil, nil
	}
	if limit > 1000 {
		limit = 1000
	}
	if staleAfter <= 0 {
		return nil, errors.New("delivery journey stale threshold must be positive")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT crt.organization_id::text, crt.delivery_job_id::text,
		       crt.email, COALESCE(v.token, '')
		FROM campaign_recipient_tracking crt
		JOIN delivery_jobs dj
		  ON dj.organization_id = crt.organization_id
		 AND dj.id = crt.delivery_job_id
		LEFT JOIN delivery_verp_tokens v
		  ON v.organization_id = crt.organization_id
		 AND v.delivery_job_id = crt.delivery_job_id
		WHERE dj.status IN ('submitted', 'accepted')
		  AND crt.journey_status IN ('processing', 'submitted_to_mail_server', 'relaying')
		  AND COALESCE(crt.journey_last_reconciled_at, '-infinity'::timestamptz)
		      <= now() - $2::interval
		ORDER BY COALESCE(crt.journey_last_reconciled_at, crt.accepted_at, dj.updated_at),
		         crt.delivery_job_id
		LIMIT $1::integer
	`, limit, fmt.Sprintf("%f seconds", staleAfter.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("select delivery journeys for reconciliation: %w", err)
	}
	type candidate struct {
		organizationID string
		jobID          string
		email          string
		token          string
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.organizationID, &item.jobID, &item.email, &item.token); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan delivery journey reconciliation candidate: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate delivery journey reconciliation candidates: %w", err)
	}
	rows.Close()

	results := make([]ReconcileResult, 0, len(candidates))
	for _, item := range candidates {
		if err := r.limiter.Wait(ctx); err != nil {
			return results, fmt.Errorf("wait for Stalwart reconciliation rate limit: %w", err)
		}
		stage := "accepted_final_state_unknown"
		var details QueueRecipient
		found := false
		if item.token != "" && r.reader != nil {
			returnPath, err := delivery.FormatVERPReturnPath(r.bounceDomain, item.token)
			if err != nil {
				return results, fmt.Errorf("format VERP return path for job %s: %w", item.jobID, err)
			}
			details, found, err = r.reader.LookupRecipient(ctx, returnPath, item.email)
			if err != nil {
				return results, fmt.Errorf("query Stalwart queue for job %s: %w", item.jobID, err)
			}
			if found {
				stage = queueJourneyStage(details)
			}
		}
		event := Event{
			OrganizationID: item.organizationID,
			JobID:          item.jobID,
			Type:           stage,
			Source:         "reconciliation",
			SourceEventID:  reconciliationEventID(item.jobID, stage, details),
			EventHash:      reconciliationEventID(item.jobID, stage, details),
			OccurredAt:     time.Now().UTC(),
			RemoteHost:     details.RemoteHost,
			SMTPCode:       details.SMTPCode,
			EnhancedCode:   details.EnhancedCode,
			Reason:         details.Reason,
			Attempt:        details.Attempt,
			NextRetryAt:    details.NextRetryAt,
		}
		if _, err := NewStore(r.pool).Record(ctx, event); err != nil {
			return results, fmt.Errorf("record reconciled delivery journey for job %s: %w", item.jobID, err)
		}
		if _, err := r.pool.Exec(ctx, `
			UPDATE campaign_recipient_tracking
			SET journey_last_reconciled_at = now(), updated_at = now()
			WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
		`, item.organizationID, item.jobID); err != nil {
			return results, fmt.Errorf("record delivery journey reconciliation time for job %s: %w", item.jobID, err)
		}
		results = append(results, ReconcileResult{JobID: item.jobID, Status: stage})
	}
	return results, nil
}

func (r *Reconciler) BackfillBatch(ctx context.Context, afterJobID string, limit int, dryRun bool) (BackfillResult, error) {
	if limit < 1 || limit > 1000 {
		return BackfillResult{}, errors.New("delivery journey backfill batch must be between 1 and 1000")
	}
	if afterJobID != "" {
		cursor, err := uuid.Parse(afterJobID)
		if err != nil {
			return BackfillResult{}, errors.New("delivery journey backfill cursor is invalid")
		}
		afterJobID = cursor.String()
	}
	rows, err := r.pool.Query(ctx, `
		SELECT dj.organization_id::text, dj.id::text, COALESCE(crt.email, ''), COALESCE(v.token, '')
		FROM delivery_jobs dj
		LEFT JOIN campaign_recipient_tracking crt
		  ON crt.organization_id = dj.organization_id AND crt.delivery_job_id = dj.id
		LEFT JOIN delivery_verp_tokens v
		  ON v.organization_id = dj.organization_id AND v.delivery_job_id = dj.id
		WHERE dj.status IN ('submitted', 'accepted')
		  AND dj.id::text > $1::text
		  AND (crt.delivery_job_id IS NULL OR crt.journey_status IN
		       ('processing', 'submitted_to_mail_server', 'relaying'))
		ORDER BY dj.id
		LIMIT $2::integer
	`, afterJobID, limit+1)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("select submitted delivery jobs for backfill: %w", err)
	}
	candidates := make([]backfillCandidate, 0, limit+1)
	for rows.Next() {
		var item backfillCandidate
		if err := rows.Scan(&item.organizationID, &item.jobID, &item.email, &item.token); err != nil {
			rows.Close()
			return BackfillResult{}, fmt.Errorf("scan submitted delivery job for backfill: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return BackfillResult{}, fmt.Errorf("iterate submitted delivery jobs for backfill: %w", err)
	}
	rows.Close()

	result := BackfillResult{Counts: make(map[string]int), DryRun: dryRun}
	result.HasMore = len(candidates) > limit
	if result.HasMore {
		candidates = candidates[:limit]
	}
	for _, item := range candidates {
		result.NextCursor = item.jobID
		if item.email == "" {
			result.Counts["untracked"]++
			continue
		}
		stage := "accepted_final_state_unknown"
		var details QueueRecipient
		if item.token != "" && r.reader != nil {
			if err := r.limiter.Wait(ctx); err != nil {
				return result, fmt.Errorf("wait for Stalwart backfill rate limit: %w", err)
			}
			returnPath, err := delivery.FormatVERPReturnPath(r.bounceDomain, item.token)
			if err != nil {
				return result, fmt.Errorf("format VERP return path for job %s: %w", item.jobID, err)
			}
			var found bool
			details, found, err = r.reader.LookupRecipient(ctx, returnPath, item.email)
			if err != nil {
				return result, fmt.Errorf("query Stalwart for backfill job %s: %w", item.jobID, err)
			}
			if found {
				stage = queueJourneyStage(details)
			}
		}
		if !dryRun {
			eventID := reconciliationEventID(item.jobID, stage, details)
			if _, err := NewStore(r.pool).Record(ctx, Event{
				OrganizationID: item.organizationID,
				JobID:          item.jobID,
				Type:           stage,
				Source:         "reconciliation",
				SourceEventID:  eventID,
				EventHash:      eventID,
				OccurredAt:     time.Now().UTC(),
				RemoteHost:     details.RemoteHost,
				SMTPCode:       details.SMTPCode,
				EnhancedCode:   details.EnhancedCode,
				Reason:         details.Reason,
				Attempt:        details.Attempt,
				NextRetryAt:    details.NextRetryAt,
			}); err != nil {
				return result, fmt.Errorf("record backfilled delivery journey for job %s: %w", item.jobID, err)
			}
			if _, err := r.pool.Exec(ctx, `
				UPDATE campaign_recipient_tracking
				SET journey_last_reconciled_at = now(), updated_at = now()
				WHERE organization_id = $1::uuid AND delivery_job_id = $2::uuid
			`, item.organizationID, item.jobID); err != nil {
				return result, fmt.Errorf("record backfill timestamp for job %s: %w", item.jobID, err)
			}
		}
		result.Counts[stage]++
	}
	result.Processed = len(candidates)
	return result, nil
}

func queueJourneyStage(recipient QueueRecipient) string {
	switch strings.ToLower(recipient.Status) {
	case "completed":
		if recipient.SMTPCode != nil && *recipient.SMTPCode >= 400 && *recipient.SMTPCode < 500 {
			return "deferred_by_recipient"
		}
		if recipient.SMTPCode != nil && *recipient.SMTPCode >= 500 {
			return "bounced"
		}
		return "accepted_by_recipient_server"
	case "temporaryfailure":
		return "deferred_by_recipient"
	case "permanentfailure":
		return "bounced"
	case "scheduled":
		return "relaying"
	default:
		return "accepted_final_state_unknown"
	}
}

func reconciliationEventID(jobID, stage string, details QueueRecipient) string {
	retry := ""
	if details.NextRetryAt != nil {
		retry = details.NextRetryAt.UTC().Format(time.RFC3339Nano)
	}
	attempt := 0
	if details.Attempt != nil {
		attempt = *details.Attempt
	}
	value := fmt.Sprintf("%s:%s:%s:%s:%s:%d:%s:%d",
		jobID, stage, details.RemoteHost, details.EnhancedCode, details.Reason,
		attempt, retry, intValue(details.SMTPCode))
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

type jmapQueueReader struct {
	endpoint string
	username string
	password string
	client   *http.Client
	limiter  *rate.Limiter
}

func (c *jmapQueueReader) LookupRecipient(ctx context.Context, returnPath, recipient string) (QueueRecipient, bool, error) {
	query, err := c.call(ctx, []any{[]any{
		"x:QueuedMessage/query",
		map[string]any{"filter": map[string]string{"returnPath": returnPath}, "limit": 10},
		"query",
	}})
	if err != nil {
		return QueueRecipient{}, false, err
	}
	queryResult, err := methodResult(query, "query")
	if err != nil {
		return QueueRecipient{}, false, err
	}
	var queryData struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(queryResult, &queryData); err != nil {
		return QueueRecipient{}, false, fmt.Errorf("decode Stalwart queued-message query: %w", err)
	}
	if len(queryData.IDs) == 0 {
		return QueueRecipient{}, false, nil
	}
	get, err := c.call(ctx, []any{[]any{
		"x:QueuedMessage/get",
		map[string]any{
			"ids":        queryData.IDs,
			"properties": []string{"id", "returnPath", "recipients", "nextRetry", "retryCount"},
		},
		"get",
	}})
	if err != nil {
		return QueueRecipient{}, false, err
	}
	getResult, err := methodResult(get, "get")
	if err != nil {
		return QueueRecipient{}, false, err
	}
	var getData struct {
		List []struct {
			ReturnPath string `json:"returnPath"`
			RetryCount int    `json:"retryCount"`
			NextRetry  string `json:"nextRetry"`
			Recipients map[string]struct {
				Status           string `json:"status"`
				RemoteHostname   string `json:"remoteHostname"`
				ResponseCode     *int   `json:"responseCode"`
				ResponseEnhanced string `json:"responseEnhanced"`
				ResponseMessage  string `json:"responseMessage"`
				RetryDue         string `json:"retryDue"`
			} `json:"recipients"`
		} `json:"list"`
	}
	if err := json.Unmarshal(getResult, &getData); err != nil {
		return QueueRecipient{}, false, fmt.Errorf("decode Stalwart queued-message details: %w", err)
	}
	for _, message := range getData.List {
		if !strings.EqualFold(message.ReturnPath, returnPath) {
			continue
		}
		for address, recipientState := range message.Recipients {
			if !strings.EqualFold(address, recipient) {
				continue
			}
			attempt := message.RetryCount + 1
			state := QueueRecipient{
				Status:       recipientState.Status,
				RemoteHost:   recipientState.RemoteHostname,
				SMTPCode:     recipientState.ResponseCode,
				EnhancedCode: recipientState.ResponseEnhanced,
				Reason:       recipientState.ResponseMessage,
				Attempt:      &attempt,
			}
			retry := firstNonEmpty(recipientState.RetryDue, message.NextRetry)
			if parsed, err := time.Parse(time.RFC3339Nano, retry); err == nil {
				state.NextRetryAt = &parsed
			}
			return state, true, nil
		}
	}
	return QueueRecipient{}, false, nil
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
		if len(parts[0]) > 0 && string(parts[0]) == `"error"` {
			return nil, fmt.Errorf("Stalwart JMAP call %q failed: %s", callID, string(parts[1]))
		}
		return parts[1], nil
	}
	return nil, fmt.Errorf("Stalwart JMAP response omitted call %q", callID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
