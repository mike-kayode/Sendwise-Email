package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

type adminCampaign struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	WorkspaceName  string     `json:"workspace_name"`
	WorkspaceSlug  string     `json:"workspace_slug"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	RecipientCount int        `json:"recipient_count"`
	SubmittedCount int        `json:"submitted_count"`
	BouncedCount   int        `json:"bounced_count"`
	ComplaintCount int        `json:"complaint_count"`
	FailedCount    int        `json:"failed_count"`
	ScheduledAt    *time.Time `json:"scheduled_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

type adminCampaignDeliveryDiagnostics struct {
	StatusCounts map[string]int `json:"status_counts"`
	CommonErrors []struct {
		Error string `json:"error"`
		Count int    `json:"count"`
	} `json:"common_errors"`
	OldestQueuedAt *time.Time `json:"oldest_queued_at"`
}

func (s *Server) getAdminCampaignDelivery(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	if _, err := uuid.Parse(campaignID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID is invalid.")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM campaigns WHERE id = $1)`, campaignID).Scan(&exists); err != nil {
		s.logger.Error("check admin campaign delivery target", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	}
	var result adminCampaignDeliveryDiagnostics
	result.StatusCounts = make(map[string]int)
	if err := s.pool.QueryRow(r.Context(), `
		SELECT min(created_at) FILTER (WHERE status = 'queued')
		FROM delivery_jobs
		WHERE campaign_id = $1
	`, campaignID).Scan(&result.OldestQueuedAt); err != nil {
		s.logger.Error("load oldest queued admin campaign job", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT status, count(*)::int
		FROM delivery_jobs
		WHERE campaign_id = $1
		GROUP BY status
	`, campaignID)
	if err != nil {
		s.logger.Error("load admin campaign job counts", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
		return
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			s.logger.Error("scan admin campaign job counts", "error", err, "campaign_id", campaignID)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
			return
		}
		result.StatusCounts[status] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logger.Error("iterate admin campaign job counts", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
		return
	}
	rows.Close()
	errorsRows, err := s.pool.Query(r.Context(), `
		SELECT last_error, count(*)::int
		FROM delivery_jobs
		WHERE campaign_id = $1 AND NULLIF(btrim(last_error), '') IS NOT NULL
		GROUP BY last_error
		ORDER BY count(*) DESC, last_error
		LIMIT 5
	`, campaignID)
	if err != nil {
		s.logger.Error("load common admin campaign delivery errors", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
		return
	}
	result.CommonErrors = make([]struct {
		Error string `json:"error"`
		Count int    `json:"count"`
	}, 0, 5)
	for errorsRows.Next() {
		var entry struct {
			Error string `json:"error"`
			Count int    `json:"count"`
		}
		if err := errorsRows.Scan(&entry.Error, &entry.Count); err != nil {
			errorsRows.Close()
			s.logger.Error("scan common admin campaign delivery errors", "error", err, "campaign_id", campaignID)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
			return
		}
		result.CommonErrors = append(result.CommonErrors, entry)
	}
	if err := errorsRows.Err(); err != nil {
		errorsRows.Close()
		s.logger.Error("iterate common admin campaign delivery errors", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign delivery diagnostics.")
		return
	}
	errorsRows.Close()
	httpx.JSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *Server) resumeAdminCampaignDeferredJobs(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	if _, err := uuid.Parse(campaignID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE delivery_jobs dj
		SET next_attempt_at = now(), failure_started_at = NULL,
		    nats_publish_claimed_at = NULL, nats_published_at = NULL,
		    updated_at = now()
		FROM campaigns c
		WHERE dj.campaign_id = $1 AND dj.organization_id = c.organization_id
		  AND c.id = dj.campaign_id AND c.status = 'sending'
		  AND dj.status = 'deferred'
	`, campaignID)
	if err != nil {
		s.logger.Error("resume deferred admin campaign jobs", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resume deferred campaign jobs.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int64{"resumed": tag.RowsAffected()})
}

func (s *Server) replayAdminCampaignDeadLetters(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	if _, err := uuid.Parse(campaignID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID is invalid.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin admin campaign dead-letter replay", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not replay dead-lettered campaign jobs.")
		return
	}
	defer tx.Rollback(r.Context())
	var replayed int64
	err = tx.QueryRow(r.Context(), `
		WITH candidates AS (
			SELECT dlq.id, dj.id AS job_id, dj.organization_id, dj.campaign_id, dj.contact_id
			FROM dead_letter_queue dlq
			JOIN delivery_jobs dj ON dj.id = dlq.delivery_job_id
			JOIN campaigns c ON c.id = dj.campaign_id
			    AND c.organization_id = dj.organization_id AND c.status = 'sending'
			WHERE dj.campaign_id = $1 AND dlq.replayed_at IS NULL
			  AND dj.status = 'failed'
			  AND NOT EXISTS (
			      SELECT 1 FROM message_events prior
			      WHERE prior.delivery_job_id = dj.id
			        AND prior.event_type IN ('submitted', 'accepted', 'delivered')
			  )
			  AND NOT EXISTS (
			      SELECT 1 FROM dead_letter_queue earlier
			      WHERE earlier.delivery_job_id = dj.id AND earlier.replayed_at IS NULL
			        AND (earlier.created_at, earlier.id) < (dlq.created_at, dlq.id)
			  )
			ORDER BY dlq.created_at, dlq.id
			LIMIT 1000
			FOR UPDATE OF dlq, dj SKIP LOCKED
		),
		replayed AS (
			UPDATE delivery_jobs dj
			SET status = 'queued', attempts = 0, failure_started_at = NULL,
			    dead_lettered_at = NULL, next_attempt_at = now(),
			    nats_publish_claimed_at = NULL, nats_published_at = NULL,
			    processing_started_at = NULL, last_error = NULL, updated_at = now()
			FROM candidates
			WHERE dj.id = candidates.job_id
			RETURNING dj.id, dj.campaign_id, dj.contact_id
		),
		marked AS (
			UPDATE dead_letter_queue dlq
			SET replayed_at = now()
			FROM replayed
			WHERE dlq.delivery_job_id = replayed.id AND dlq.replayed_at IS NULL
			RETURNING replayed.campaign_id, replayed.contact_id
		)
		, updated_recipients AS (
			UPDATE campaign_recipients cr
		SET status = 'queued'
		FROM (SELECT DISTINCT campaign_id, contact_id FROM marked) marked
		WHERE cr.campaign_id = marked.campaign_id
		  AND cr.contact_id = marked.contact_id
			RETURNING cr.contact_id
		)
		SELECT count(*) FROM replayed
	`, campaignID).Scan(&replayed)
	if err != nil {
		s.logger.Error("replay admin campaign dead letters", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not replay dead-lettered campaign jobs.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit admin campaign dead-letter replay", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not replay dead-lettered campaign jobs.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int64{"replayed": replayed})
}

func (s *Server) listAdminCampaigns(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if len(search) > 100 || len(status) > 32 {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", "Campaign filters are too long.")
		return
	}
	if workspaceID != "" {
		if _, err := uuid.Parse(workspaceID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID must be a UUID.")
			return
		}
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			httpx.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			httpx.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	filter := `
		WHERE ($1 = '' OR c.id::text = $1 OR c.name ILIKE '%' || $1 || '%'
			OR c.organization_id::text ILIKE '%' || $1 || '%'
			OR o.name ILIKE '%' || $1 || '%' OR o.slug ILIKE '%' || $1 || '%'
			OR EXISTS (
				SELECT 1 FROM delivery_jobs dj
				WHERE dj.campaign_id = c.id AND dj.message_id = $1
			))
		  AND ($2 = '' OR c.status = $2)
		  AND ($3 = '' OR c.organization_id::text = $3)
	`
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM campaigns c
		JOIN organizations o ON o.id = c.organization_id
	`+filter, search, status, workspaceID).Scan(&total); err != nil {
		s.logger.Error("count admin campaigns", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign metadata.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT c.id::text, c.organization_id::text, o.name, o.slug, c.name,
		       c.status, c.recipient_count,
		       COALESCE(d.submitted_count, 0), COALESCE(d.bounced_count, 0),
		       COALESCE(d.complaint_count, 0), COALESCE(d.failed_count, 0),
		       c.scheduled_at, c.started_at, c.completed_at, c.created_at
		FROM campaigns c
		JOIN organizations o ON o.id = c.organization_id
		LEFT JOIN LATERAL (
			SELECT count(*) FILTER (WHERE status IN ('submitted', 'accepted'))::int AS submitted_count,
			       count(*) FILTER (WHERE status = 'bounced')::int AS bounced_count,
			       count(*) FILTER (WHERE status = 'complained')::int AS complaint_count,
			       count(*) FILTER (WHERE status = 'failed')::int AS failed_count
			FROM delivery_jobs
			WHERE campaign_id = c.id
		) d ON true
	`+filter+`
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT $4 OFFSET $5
	`, search, status, workspaceID, limit, offset)
	if err != nil {
		s.logger.Error("list admin campaigns", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign metadata.")
		return
	}
	defer rows.Close()
	campaigns := make([]adminCampaign, 0, limit)
	for rows.Next() {
		var campaign adminCampaign
		if err := rows.Scan(
			&campaign.ID, &campaign.WorkspaceID, &campaign.WorkspaceName,
			&campaign.WorkspaceSlug, &campaign.Name, &campaign.Status,
			&campaign.RecipientCount, &campaign.SubmittedCount, &campaign.BouncedCount,
			&campaign.ComplaintCount, &campaign.FailedCount, &campaign.ScheduledAt,
			&campaign.StartedAt, &campaign.CompletedAt, &campaign.CreatedAt,
		); err != nil {
			s.logger.Error("scan admin campaign", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign metadata.")
			return
		}
		campaigns = append(campaigns, campaign)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate admin campaigns", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaign metadata.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": campaigns,
		"pagination": paginationResponse{
			Total: total, Limit: limit, Offset: offset,
		},
	})
}

func (s *Server) stopAdminCampaign(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	if _, err := uuid.Parse(campaignID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID is invalid.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin admin campaign stop", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not stop the campaign.")
		return
	}
	defer tx.Rollback(r.Context())
	var organizationID string
	if err := tx.QueryRow(r.Context(), `
		SELECT organization_id::text
		FROM campaigns
		WHERE id = $1
		FOR UPDATE
	`, campaignID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	} else if err != nil {
		s.logger.Error("load admin campaign for stop", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not stop the campaign.")
		return
	}
	if err := cancelCampaignInTx(r.Context(), tx, campaignID, organizationID); errors.Is(err, errCampaignNotCancellable) {
		httpx.Error(w, http.StatusConflict, "campaign_not_stoppable", "The campaign cannot be stopped in its current state.")
		return
	} else if err != nil {
		s.logger.Error("stop admin campaign", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not stop the campaign.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit admin campaign stop", "error", err, "campaign_id", campaignID)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not stop the campaign.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
