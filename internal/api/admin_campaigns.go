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
