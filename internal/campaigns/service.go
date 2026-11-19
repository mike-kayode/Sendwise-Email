package campaigns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	segmentcontacts "github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/messaging"
)

var (
	ErrNotFound         = errors.New("campaign not found")
	ErrInvalidState     = errors.New("campaign is not in a sendable state")
	ErrSegmentNotFound  = errors.New("campaign segment not found")
	ErrInvalidSegment   = errors.New("campaign segment rules are invalid")
	ErrDomainUnverified = errors.New("sending domain is not verified")
	ErrDomainConflict   = errors.New("sending domain is owned by another workspace")
	ErrInvalidSender    = errors.New("campaign sender email is invalid")
	ErrPostalAddress    = errors.New("campaign sender postal address is required")
	ErrSenderIdentity   = errors.New("campaign From address is not a verified workspace sender identity")
	ErrSenderProfile    = errors.New("sender profile is incomplete")
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

type messageIDPublisher interface {
	PublishWithID(ctx context.Context, subject, messageID string, data []byte) error
}

func NewService(pool *pgxpool.Pool, publisher Publisher, logger *slog.Logger, maxAttempts int, requireVerifiedDomain bool) *Service {
	return &Service{pool: pool, publisher: publisher, logger: logger, maxAttempts: maxAttempts, requireVerifiedDomain: requireVerifiedDomain}
}

// RepublishPending re-enqueues durable jobs that have no in-flight NATS
// message. Claims are shared across workers and stale claims are recoverable.
func (s *Service) RepublishPending(ctx context.Context, limit int) (int, error) {
	return s.republishPending(ctx, "", "", limit, 10*time.Minute)
}

func (s *Service) RepublishPendingWithStaleTimeout(ctx context.Context, limit int, staleAfter time.Duration) (int, error) {
	return s.republishPending(ctx, "", "", limit, staleAfter)
}

func (s *Service) republishPending(ctx context.Context, campaignID, organizationID string, limit int, staleAfter time.Duration) (int, error) {
	if s.publisher == nil {
		return 0, nil
	}
	if limit <= 0 {
		return 0, nil
	}
	if staleAfter <= 0 {
		staleAfter = 10 * time.Minute
	}
	if limit > 1000 {
		limit = 1000
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin pending-job claim: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		WITH candidates AS (
			SELECT dj.id
			FROM delivery_jobs dj
			JOIN campaigns c
			  ON c.id = dj.campaign_id AND c.organization_id = dj.organization_id
			WHERE c.status = 'sending'
			  AND dj.next_attempt_at <= now()
			  AND (
				  dj.status = 'deferred'
				  OR (
					  dj.status = 'queued'
					  AND dj.processing_started_at IS NULL
					  AND (
						  (
							  dj.nats_published_at IS NULL
							  AND (
								  dj.nats_publish_claimed_at IS NULL
								  OR dj.nats_publish_claimed_at < now() - interval '5 minutes'
							  )
						  )
						  OR dj.nats_published_at < now() - $4::interval
					  )
				  )
				  OR (
					  dj.status = 'processing'
					  AND dj.processing_started_at < now() - $4::interval
				  )
			  )
			  AND ($2::text = '' OR dj.campaign_id::text = $2)
			  AND ($3::text = '' OR dj.organization_id::text = $3)
			ORDER BY dj.created_at, dj.id
			LIMIT $1
			FOR UPDATE OF dj SKIP LOCKED
		)
		UPDATE delivery_jobs dj
		SET status = 'queued',
		    nats_publish_claimed_at = now(),
		    nats_published_at = NULL,
		    processing_started_at = NULL,
		    updated_at = now()
		FROM candidates
		WHERE dj.id = candidates.id
		RETURNING dj.id, dj.organization_id, dj.campaign_id, dj.contact_id
	`, limit, campaignID, organizationID, fmt.Sprintf("%f seconds", staleAfter.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("claim pending jobs: %w", err)
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
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit pending-job claim: %w", err)
	}

	published := 0
	var publishErr error
	for _, job := range jobs {
		payload, err := json.Marshal(job)
		if err != nil {
			return published, fmt.Errorf("encode delivery job %s: %w", job.JobID, err)
		}
		if err := s.publishJob(ctx, job.JobID, payload); err != nil {
			s.logger.Error("republish delivery job", "job_id", job.JobID, "error", err)
			if _, resetErr := s.pool.Exec(ctx, `
				UPDATE delivery_jobs
				SET status = 'deferred',
				    next_attempt_at = GREATEST(next_attempt_at, now() + interval '15 seconds'),
				    nats_publish_claimed_at = NULL,
				    nats_published_at = NULL,
				    last_error = $2,
				    updated_at = now()
				WHERE id = $1 AND status = 'queued' AND nats_publish_claimed_at IS NOT NULL
			`, job.JobID, err.Error()); resetErr != nil {
				if publishErr == nil {
					publishErr = fmt.Errorf("restore failed publish for job %s: %w", job.JobID, resetErr)
				}
			}
			if publishErr == nil {
				publishErr = fmt.Errorf("publish delivery job %s: %w", job.JobID, err)
			}
			continue
		}
		if _, err := s.pool.Exec(ctx, `
			UPDATE delivery_jobs
			SET nats_publish_claimed_at = NULL, nats_published_at = now(), updated_at = now()
			WHERE id = $1 AND status = 'queued' AND nats_publish_claimed_at IS NOT NULL
		`, job.JobID); err != nil {
			if publishErr == nil {
				publishErr = fmt.Errorf("record published delivery job %s: %w", job.JobID, err)
			}
			continue
		}
		published++
	}
	return published, publishErr
}

func (s *Service) publishJob(ctx context.Context, jobID string, payload []byte) error {
	if publisher, ok := s.publisher.(messageIDPublisher); ok {
		return publisher.PublishWithID(ctx, messaging.SubjectDeliveryJobs, jobID, payload)
	}
	return s.publisher.Publish(ctx, messaging.SubjectDeliveryJobs, payload)
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
	var senderIdentityID *string
	err = tx.QueryRow(ctx, `
		SELECT status, list_id, segment_id, sender_email, sender_identity_id
		FROM campaigns
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, campaignID, organizationID).Scan(&status, &listID, &segmentID, &senderEmail, &senderIdentityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("load campaign: %w", err)
	}
	var senderIdentityValid bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM sender_identities si
			JOIN sending_domains sd
			  ON sd.organization_id = si.organization_id AND lower(sd.domain) = lower(si.domain)
			WHERE si.id = $1 AND si.organization_id = $2
			  AND lower(si.local_part || '@' || si.domain) = lower($3)
			  AND sd.status = 'verified' AND NOT sd.owner_conflict
			  AND sd.admin_disabled_at IS NULL
		)
	`, senderIdentityID, organizationID, senderEmail).Scan(&senderIdentityValid); err != nil {
		return 0, fmt.Errorf("check campaign sender identity: %w", err)
	}
	if !senderIdentityValid {
		return 0, ErrSenderIdentity
	}
	var senderProfileComplete bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sender_profiles
			WHERE organization_id = $1
			  AND btrim(company_name) <> '' AND btrim(website) <> ''
			  AND btrim(support_email) <> ''
		)
	`, organizationID).Scan(&senderProfileComplete); err != nil {
		return 0, fmt.Errorf("check sender profile: %w", err)
	}
	if !senderProfileComplete {
		return 0, ErrSenderProfile
	}
	switch status {
	case "processing", "sending", "paused", "scheduled":
	default:
		return 0, ErrInvalidState
	}

	normalizedSender, senderErr := delivery.NormalizeSenderAddress(senderEmail)
	if senderErr != nil {
		return 0, ErrInvalidSender
	}
	if normalizedSender != senderEmail {
		if _, err := tx.Exec(ctx, `
			UPDATE campaigns SET sender_email = $3, updated_at = now()
			WHERE id = $1 AND organization_id = $2
		`, campaignID, organizationID, normalizedSender); err != nil {
			return 0, fmt.Errorf("normalize campaign sender: %w", err)
		}
	}
	senderEmail = normalizedSender
	if senderEmail != "" {
		domain := domainOf(senderEmail)
		if domain == "" {
			if s.requireVerifiedDomain {
				return 0, ErrDomainUnverified
			}
		} else {
			var ownerConflict, stalwartAuthenticated bool
			err := tx.QueryRow(ctx, `
				SELECT owner_conflict, stalwart_authenticated
				FROM sending_domains
				WHERE lower(domain) = $1 AND organization_id = $2
			`, domain, organizationID).Scan(&ownerConflict, &stalwartAuthenticated)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return 0, fmt.Errorf("check sending domain: %w", err)
			}
			if err == nil && ownerConflict {
				return 0, ErrDomainConflict
			}
			if s.requireVerifiedDomain && (errors.Is(err, pgx.ErrNoRows) || !stalwartAuthenticated) {
				return 0, ErrDomainUnverified
			}
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

	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_recipient_tracking
			(organization_id, campaign_id, recipient_id, contact_id, delivery_job_id,
			 tracking_id, email, delivery_status, queued_at)
		SELECT r.organization_id, r.campaign_id, r.id, r.contact_id, dj.id,
		       'trk_' || encode(gen_random_bytes(24), 'hex'), r.email, dj.status, now()
		FROM campaign_recipients r
		JOIN delivery_jobs dj
		  ON dj.campaign_id = r.campaign_id AND dj.contact_id = r.contact_id
		WHERE r.campaign_id = $1 AND r.organization_id = $2
		ON CONFLICT (campaign_id, contact_id) DO UPDATE
		SET delivery_job_id = EXCLUDED.delivery_job_id,
		    email = EXCLUDED.email,
		    updated_at = now()
	`, campaignID, organizationID); err != nil {
		return 0, fmt.Errorf("create recipient tracking rows: %w", err)
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

	var queuedCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_jobs
		WHERE campaign_id = $1 AND organization_id = $2 AND status = 'queued'
	`, campaignID, organizationID).Scan(&queuedCount); err != nil {
		return 0, fmt.Errorf("count queued jobs: %w", err)
	}
	if s.publisher == nil {
		s.logger.Warn("no message publisher configured; delivery jobs remain queued", "campaign_id", campaignID)
		return queuedCount, nil
	}

	const publishBatchSize = 500
	for {
		published, err := s.republishPending(ctx, campaignID, organizationID, publishBatchSize, 10*time.Minute)
		if err != nil {
			return queuedCount, fmt.Errorf("publish campaign delivery jobs: %w", err)
		}
		if published < publishBatchSize {
			break
		}
	}

	return queuedCount, nil
}

func domainOf(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		return email[idx+1:]
	}
	return ""
}
