package campaigns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	segmentcontacts "github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/messaging"
)

var (
	ErrNotFound         = errors.New("campaign not found")
	ErrInvalidState     = errors.New("campaign is not in a sendable state")
	ErrSegmentNotFound  = errors.New("campaign segment not found")
	ErrInvalidSegment   = errors.New("campaign segment rules are invalid")
	ErrDomainUnverified = errors.New("sending domain is not verified")
)

type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte) error
}

type Service struct {
	pool                  *pgxpool.Pool
	publisher             Publisher
	logger                *slog.Logger
	maxAttempts           int
	requireVerifiedDomain bool
}

type CampaignRef struct {
	ID             string
	OrganizationID string
}

type JobPayload struct {
	JobID          string `json:"job_id"`
	OrganizationID string `json:"organization_id"`
	CampaignID     string `json:"campaign_id"`
	ContactID      string `json:"contact_id"`
}

func NewService(pool *pgxpool.Pool, publisher Publisher, logger *slog.Logger, maxAttempts int, requireVerifiedDomain bool) *Service {
	return &Service{pool: pool, publisher: publisher, logger: logger, maxAttempts: maxAttempts, requireVerifiedDomain: requireVerifiedDomain}
}

// RepublishPending re-enqueues durable jobs that have no in-flight NATS
// message, for example after a broker restart or a failed publish.
func (s *Service) RepublishPending(ctx context.Context, limit int) (int, error) {
	if s.publisher == nil {
		return 0, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, campaign_id, contact_id
		FROM delivery_jobs
		WHERE status IN ('queued', 'deferred')
		  AND next_attempt_at <= now()
		ORDER BY created_at
		LIMIT $1
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("load pending jobs: %w", err)
	}

	var jobs []JobPayload
	for rows.Next() {
		var job JobPayload
		if err := rows.Scan(&job.JobID, &job.OrganizationID, &job.CampaignID, &job.ContactID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan pending job: %w", err)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate pending jobs: %w", err)
	}

	published := 0
	for _, job := range jobs {
		payload, err := json.Marshal(job)
		if err != nil {
			continue
		}
		if err := s.publisher.Publish(ctx, messaging.SubjectDeliveryJobs, payload); err != nil {
			s.logger.Error("republish delivery job", "job_id", job.JobID, "error", err)
			continue
		}
		published++
	}
	return published, nil
}

// DueCampaigns atomically claims scheduled campaigns whose send time has arrived.
func (s *Service) DueCampaigns(ctx context.Context, limit int) ([]CampaignRef, error) {
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id, organization_id
			FROM campaigns
			WHERE status = 'scheduled'
			  AND scheduled_at IS NOT NULL
			  AND scheduled_at <= now()
			ORDER BY scheduled_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE campaigns c
		SET status = 'processing', updated_at = now()
		FROM due
		WHERE c.id = due.id
		RETURNING c.id, c.organization_id
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due campaigns: %w", err)
	}
	defer rows.Close()

	var refs []CampaignRef
	for rows.Next() {
		var ref CampaignRef
		if err := rows.Scan(&ref.ID, &ref.OrganizationID); err != nil {
			return nil, fmt.Errorf("scan due campaign: %w", err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// Queue creates the recipient snapshot and delivery jobs, then publishes jobs
// to NATS. Jobs are durable in PostgreSQL: a failed publish can be recovered
// by calling Queue again (for example on resume or operator replay).
func (s *Service) Queue(ctx context.Context, campaignID, organizationID string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin queue transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var status string
	var listID, segmentID *string
	var senderEmail string
	err = tx.QueryRow(ctx, `
		SELECT status, list_id, segment_id, sender_email
		FROM campaigns
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, campaignID, organizationID).Scan(&status, &listID, &segmentID, &senderEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load campaign: %w", err)
	}
	switch status {
	case "processing", "sending", "paused", "scheduled":
	default:
		return 0, ErrInvalidState
	}

	if s.requireVerifiedDomain && senderEmail != "" {
		domain := domainOf(senderEmail)
		if domain == "" {
			return 0, ErrDomainUnverified
		}
		var domainStatus string
		err = tx.QueryRow(ctx, `
			SELECT status FROM sending_domains
			WHERE domain = $1 AND organization_id = $2
		`, domain, organizationID).Scan(&domainStatus)
		if errors.Is(err, pgx.ErrNoRows) || domainStatus != "verified" {
			return 0, ErrDomainUnverified
		}
		if err != nil {
			return 0, fmt.Errorf("check sending domain: %w", err)
		}
	}

	var listArg any
	if listID != nil {
		listArg = *listID
	}
	segmentPredicate := ""
	var segmentArgs []any
	if segmentID != nil {
		var rules []byte
		err := tx.QueryRow(ctx, `
			SELECT rules FROM segments
			WHERE id = $1 AND organization_id = $2
		`, *segmentID, organizationID).Scan(&rules)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrSegmentNotFound
		}
		if err != nil {
			return 0, fmt.Errorf("load campaign segment: %w", err)
		}
		predicate, args, err := segmentcontacts.CompileSegmentRules(rules, 4)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidSegment, err)
		}
		segmentPredicate = " AND " + predicate
		segmentArgs = args
	}

	var snapshotCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM campaign_recipients
		WHERE campaign_id = $1 AND organization_id = $2
	`, campaignID, organizationID).Scan(&snapshotCount); err != nil {
		return 0, fmt.Errorf("count snapshot: %w", err)
	}

	if snapshotCount == 0 {
		snapshotQuery := `
			INSERT INTO campaign_recipients (organization_id, campaign_id, contact_id, email)
			SELECT $2, $1, c.id, c.email
			FROM contacts c
			WHERE c.organization_id = $2
			  AND c.status = 'subscribed'
			  AND ($3::uuid IS NULL OR EXISTS (
				  SELECT 1 FROM contact_list_members m
				  WHERE m.contact_id = c.id
				    AND m.list_id = $3
				    AND m.unsubscribed_at IS NULL
			  ))
			` + segmentPredicate + `
			  AND NOT EXISTS (
				  SELECT 1 FROM suppressions s
				  WHERE s.organization_id = $2
				    AND lower(s.email) = lower(c.email)
			  )
			ON CONFLICT (campaign_id, contact_id) DO NOTHING
		`
		snapshotArgs := []any{campaignID, organizationID, listArg}
		snapshotArgs = append(snapshotArgs, segmentArgs...)
		if _, err := tx.Exec(ctx, snapshotQuery, snapshotArgs...); err != nil {
			return 0, fmt.Errorf("snapshot recipients: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status, max_attempts)
		SELECT r.organization_id, r.campaign_id, r.contact_id,
		       gen_random_uuid()::text || '@email-platform.local',
		       'queued', $3
		FROM campaign_recipients r
		WHERE r.campaign_id = $1 AND r.organization_id = $2
		ON CONFLICT (campaign_id, contact_id) DO NOTHING
	`, campaignID, organizationID, s.maxAttempts); err != nil {
		return 0, fmt.Errorf("create delivery jobs: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT id, contact_id
		FROM delivery_jobs
		WHERE campaign_id = $1 AND organization_id = $2 AND status = 'queued'
	`, campaignID, organizationID)
	if err != nil {
		return 0, fmt.Errorf("load queued jobs: %w", err)
	}

	var jobs []JobPayload
	for rows.Next() {
		job := JobPayload{
			OrganizationID: organizationID,
			CampaignID:     campaignID,
		}
		if err := rows.Scan(&job.JobID, &job.ContactID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan queued job: %w", err)
		}
		jobs = append(jobs, job)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate queued jobs: %w", err)
	}

	var recipientCount int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM campaign_recipients WHERE campaign_id = $1 AND organization_id = $2`,
		campaignID, organizationID,
	).Scan(&recipientCount); err != nil {
		return 0, fmt.Errorf("count recipients: %w", err)
	}

	if recipientCount == 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE campaigns
			SET status = 'completed', completed_at = now(), recipient_count = 0, updated_at = now()
			WHERE id = $1 AND organization_id = $2
		`, campaignID, organizationID); err != nil {
			return 0, fmt.Errorf("complete empty campaign: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE campaigns
			SET status = 'sending',
			    recipient_count = $3,
			    started_at = COALESCE(started_at, now()),
			    updated_at = now()
			WHERE id = $1 AND organization_id = $2
		`, campaignID, organizationID, recipientCount); err != nil {
			return 0, fmt.Errorf("update campaign status: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit queue transaction: %w", err)
	}

	if s.publisher == nil {
		s.logger.Warn("no message publisher configured; delivery jobs remain queued", "campaign_id", campaignID)
		return len(jobs), nil
	}

	for _, job := range jobs {
		payload, err := json.Marshal(job)
		if err != nil {
			continue
		}
		if err := s.publisher.Publish(ctx, messaging.SubjectDeliveryJobs, payload); err != nil {
			s.logger.Error("publish delivery job", "job_id", job.JobID, "error", err)
		}
	}

	return len(jobs), nil
}

func domainOf(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return ""
}
