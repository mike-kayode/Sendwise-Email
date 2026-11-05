package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/campaigns"
	segmentcontacts "github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/httpx"
)

const campaignColumns = `
	id, organization_id, name, subject, preview_text, sender_name, sender_email,
	COALESCE(reply_to, ''), list_id, segment_id, template_id, content, status,
	recipient_count, track_opens, track_clicks, scheduled_at, started_at,
	completed_at, paused_at, created_at, updated_at
`

type campaign struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id,omitempty"`
	Name           string          `json:"name"`
	Subject        string          `json:"subject"`
	PreviewText    string          `json:"preview_text"`
	SenderName     string          `json:"sender_name"`
	SenderEmail    string          `json:"sender_email"`
	ReplyTo        string          `json:"reply_to"`
	ListID         *string         `json:"list_id"`
	SegmentID      *string         `json:"segment_id"`
	TemplateID     *string         `json:"template_id"`
	Content        json.RawMessage `json:"content"`
	Status         string          `json:"status"`
	RecipientCount int             `json:"recipient_count"`
	TrackOpens     bool            `json:"track_opens"`
	TrackClicks    bool            `json:"track_clicks"`
	ScheduledAt    *time.Time      `json:"scheduled_at"`
	StartedAt      *time.Time      `json:"started_at"`
	CompletedAt    *time.Time      `json:"completed_at"`
	PausedAt       *time.Time      `json:"paused_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func scanCampaign(row pgx.Row) (*campaign, error) {
	var c campaign
	var content []byte
	if err := row.Scan(
		&c.ID, &c.OrganizationID, &c.Name, &c.Subject, &c.PreviewText,
		&c.SenderName, &c.SenderEmail, &c.ReplyTo, &c.ListID, &c.SegmentID,
		&c.TemplateID, &content, &c.Status, &c.RecipientCount,
		&c.TrackOpens, &c.TrackClicks, &c.ScheduledAt, &c.StartedAt,
		&c.CompletedAt, &c.PausedAt, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(content) == 0 {
		content = []byte(`{"version":1,"blocks":[]}`)
	}
	c.Content = json.RawMessage(content)
	return &c, nil
}

func (s *Server) loadCampaign(ctx context.Context, id, organizationID string) (*campaign, error) {
	return scanCampaign(s.pool.QueryRow(ctx, `
		SELECT `+campaignColumns+`
		FROM campaigns
		WHERE id = $1 AND organization_id = $2
	`, id, organizationID))
}

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	// Clients use status=all to mean "no status filter" (campaigns tabs,
	// automations view). Without this the filter matches nothing and every
	// unfiltered list renders empty.
	if strings.EqualFold(status, "all") {
		status = ""
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	limit, offset := pagination(r)

	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*) FROM campaigns
		WHERE organization_id = $1
		  AND ($2 = '' OR status = $2)
		  AND ($3 = '' OR name ILIKE '%' || $3 || '%' OR subject ILIKE '%' || $3 || '%')
	`, orgID, status, search).Scan(&total); err != nil {
		s.logger.Error("count campaigns", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaigns.")
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT `+campaignColumns+`
		FROM campaigns
		WHERE organization_id = $1
		  AND ($2 = '' OR status = $2)
		  AND ($3 = '' OR name ILIKE '%' || $3 || '%' OR subject ILIKE '%' || $3 || '%')
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5
	`, orgID, status, search, limit, offset)
	if err != nil {
		s.logger.Error("list campaigns", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaigns.")
		return
	}
	defer rows.Close()

	items := make([]campaign, 0, limit)
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			s.logger.Error("scan campaign", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load campaigns.")
			return
		}
		items = append(items, *c)
	}

	httpx.JSON(w, http.StatusOK, listResponse[campaign]{
		Data:       items,
		Pagination: paginationResponse{Total: total, Limit: limit, Offset: offset},
	})
}

type campaignInput struct {
	Name        string          `json:"name"`
	Subject     string          `json:"subject"`
	PreviewText string          `json:"preview_text"`
	SenderName  string          `json:"sender_name"`
	SenderEmail string          `json:"sender_email"`
	ReplyTo     string          `json:"reply_to"`
	ListID      string          `json:"list_id"`
	SegmentID   string          `json:"segment_id"`
	TemplateID  string          `json:"template_id"`
	Content     json.RawMessage `json:"content"`
	TrackOpens  *bool           `json:"track_opens"`
	TrackClicks *bool           `json:"track_clicks"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	var input campaignInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Campaign name is required.")
		return
	}
	if len(input.Content) == 0 {
		input.Content = json.RawMessage(`{"version":1,"blocks":[]}`)
	}
	if !json.Valid(input.Content) {
		httpx.Error(w, http.StatusBadRequest, "invalid_content", "Campaign content must be valid JSON.")
		return
	}

	c, err := scanCampaign(s.pool.QueryRow(r.Context(), `
		INSERT INTO campaigns (
			organization_id, created_by, name, subject, preview_text,
			sender_name, sender_email, reply_to, list_id, segment_id,
			template_id, content, track_opens, track_clicks, scheduled_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9::uuid, $10::uuid,
			$11::uuid, $12::jsonb, COALESCE($13, true), COALESCE($14, true), $15
		)
		RETURNING `+campaignColumns,
		s.organizationID(r), s.claims(r).Subject, input.Name,
		strings.TrimSpace(input.Subject), strings.TrimSpace(input.PreviewText),
		strings.TrimSpace(input.SenderName), strings.TrimSpace(input.SenderEmail),
		nullIfEmpty(input.ReplyTo), nullIfEmpty(input.ListID), nullIfEmpty(input.SegmentID),
		nullIfEmpty(input.TemplateID), []byte(input.Content), input.TrackOpens,
		input.TrackClicks, input.ScheduledAt,
	))
	if err != nil {
		s.logger.Error("create campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the campaign.")
		return
	}

	httpx.JSON(w, http.StatusCreated, c)
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadCampaign(r.Context(), r.PathValue("id"), s.organizationID(r))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the campaign.")
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

type campaignUpdateInput struct {
	Name        *string         `json:"name"`
	Subject     *string         `json:"subject"`
	PreviewText *string         `json:"preview_text"`
	SenderName  *string         `json:"sender_name"`
	SenderEmail *string         `json:"sender_email"`
	ReplyTo     *string         `json:"reply_to"`
	ListID      *string         `json:"list_id"`
	SegmentID   *string         `json:"segment_id"`
	TemplateID  *string         `json:"template_id"`
	Content     json.RawMessage `json:"content"`
	TrackOpens  *bool           `json:"track_opens"`
	TrackClicks *bool           `json:"track_clicks"`
	ScheduledAt *time.Time      `json:"scheduled_at"`
}

func (s *Server) updateCampaign(w http.ResponseWriter, r *http.Request) {
	var input campaignUpdateInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Campaign name is required.")
		return
	}

	var content any
	if len(input.Content) > 0 {
		if !json.Valid(input.Content) {
			httpx.Error(w, http.StatusBadRequest, "invalid_content", "Campaign content must be valid JSON.")
			return
		}
		content = []byte(input.Content)
	}

	c, err := scanCampaign(s.pool.QueryRow(r.Context(), `
		UPDATE campaigns
		SET name = COALESCE($3, name),
		    subject = COALESCE($4, subject),
		    preview_text = COALESCE($5, preview_text),
		    sender_name = COALESCE($6, sender_name),
		    sender_email = COALESCE($7, sender_email),
		    reply_to = COALESCE($8, reply_to),
		    list_id = COALESCE($9::uuid, list_id),
		    segment_id = COALESCE($10::uuid, segment_id),
		    template_id = COALESCE($11::uuid, template_id),
		    content = COALESCE($12::jsonb, content),
		    track_opens = COALESCE($13, track_opens),
		    track_clicks = COALESCE($14, track_clicks),
		    scheduled_at = COALESCE($15, scheduled_at),
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+campaignColumns,
		r.PathValue("id"), s.organizationID(r),
		input.Name, input.Subject, input.PreviewText, input.SenderName, input.SenderEmail,
		input.ReplyTo, input.ListID, input.SegmentID, input.TemplateID,
		content, input.TrackOpens, input.TrackClicks, input.ScheduledAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("update campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the campaign.")
		return
	}

	httpx.JSON(w, http.StatusOK, c)
}

func (s *Server) deleteCampaign(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM campaigns
		WHERE id = $1 AND organization_id = $2
		  AND status IN ('draft', 'ready', 'cancelled')
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		s.logger.Error("delete campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the campaign.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "campaign_not_deletable", "Only draft, ready, or cancelled campaigns can be deleted.")
		return
	}
	httpx.NoContent(w)
}

type validationCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (s *Server) validateCampaign(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadCampaign(r.Context(), r.PathValue("id"), s.organizationID(r))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load campaign for validation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not validate the campaign.")
		return
	}

	checks := make([]validationCheck, 0, 6)
	add := func(name, status, message string) {
		checks = append(checks, validationCheck{Name: name, Status: status, Message: message})
	}

	if strings.TrimSpace(c.Name) != "" {
		add("campaign_name", "pass", "Campaign has a name.")
	} else {
		add("campaign_name", "error", "Campaign name is required.")
	}

	if strings.TrimSpace(c.Subject) != "" {
		add("subject", "pass", "Subject line is set.")
	} else {
		add("subject", "error", "Subject line is required.")
	}

	if strings.TrimSpace(c.SenderEmail) != "" && strings.Contains(c.SenderEmail, "@") && strings.TrimSpace(c.SenderName) != "" {
		add("sender", "pass", "Sender identity is set.")
	} else {
		add("sender", "error", "Sender name and sender email are required.")
	}

	var design delivery.Design
	if err := json.Unmarshal(c.Content, &design); err == nil && len(design.Blocks) > 0 {
		add("content", "pass", "Email content contains at least one block.")
	} else {
		add("content", "error", "Add at least one content block to the email.")
	}

	switch {
	case c.SegmentID != nil:
		var rules []byte
		err := s.pool.QueryRow(r.Context(), `
			SELECT rules FROM segments WHERE id = $1 AND organization_id = $2
		`, *c.SegmentID, s.organizationID(r)).Scan(&rules)
		if errors.Is(err, pgx.ErrNoRows) {
			add("audience", "error", "The selected segment is unavailable.")
		} else if err != nil {
			add("audience", "error", "Could not load the selected segment.")
		} else if _, _, err := segmentcontacts.CompileSegmentRules(rules, 1); err != nil {
			add("audience", "error", "The selected segment has invalid rules.")
		} else {
			add("audience", "pass", "Segment rules are valid.")
		}
	case c.ListID != nil:
		var exists bool
		if err := s.pool.QueryRow(r.Context(), `
			SELECT EXISTS (SELECT 1 FROM contact_lists WHERE id = $1 AND organization_id = $2)
		`, *c.ListID, s.organizationID(r)).Scan(&exists); err == nil && exists {
			add("audience", "pass", "Audience list exists.")
		} else {
			add("audience", "error", "The selected audience list no longer exists.")
		}
	default:
		add("audience", "warning", "No list selected; all subscribed contacts will receive this campaign.")
	}

	senderDomain := ""
	if idx := strings.LastIndex(c.SenderEmail, "@"); idx >= 0 {
		senderDomain = strings.ToLower(c.SenderEmail[idx+1:])
	}
	if senderDomain == "" {
		add("sending_domain", "error", "The sender email must include a domain.")
	} else {
		var verified bool
		if err := s.pool.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM sending_domains
				WHERE organization_id = $1 AND lower(domain) = $2 AND status = 'verified'
			)
		`, s.organizationID(r), senderDomain).Scan(&verified); err != nil {
			add("sending_domain", "error", "Could not check sending domain verification.")
		} else if verified {
			add("sending_domain", "pass", "Sending domain is verified.")
		} else if s.cfg.RequireVerifiedDomain {
			add("sending_domain", "error", "Verify the sending domain before sending.")
		} else {
			add("sending_domain", "warning", "Sending domain is not verified. Configure SPF, DKIM, and DMARC before production sending.")
		}
	}

	valid := true
	for _, check := range checks {
		if check.Status == "error" {
			valid = false
			break
		}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"valid":  valid,
		"checks": checks,
	})
}

type scheduleInput struct {
	ScheduledAt time.Time `json:"scheduled_at"`
}

func (s *Server) scheduleCampaign(w http.ResponseWriter, r *http.Request) {
	var input scheduleInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if input.ScheduledAt.IsZero() {
		httpx.Error(w, http.StatusBadRequest, "missing_schedule", "scheduled_at is required.")
		return
	}
	if input.ScheduledAt.Before(time.Now().Add(-time.Minute)) {
		httpx.Error(w, http.StatusBadRequest, "invalid_schedule", "scheduled_at must be in the future.")
		return
	}

	c, err := scanCampaign(s.pool.QueryRow(r.Context(), `
		UPDATE campaigns
		SET status = 'scheduled', scheduled_at = $3, updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status IN ('draft', 'ready')
		RETURNING `+campaignColumns,
		r.PathValue("id"), s.organizationID(r), input.ScheduledAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusConflict, "campaign_not_schedulable", "Only draft or ready campaigns can be scheduled.")
		return
	}
	if err != nil {
		s.logger.Error("schedule campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not schedule the campaign.")
		return
	}

	httpx.JSON(w, http.StatusOK, c)
}

func (s *Server) sendCampaign(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	campaignID := r.PathValue("id")

	c, err := s.loadCampaign(r.Context(), campaignID, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the campaign.")
		return
	}

	switch c.Status {
	case "draft", "ready", "scheduled":
	case "paused":
		httpx.Error(w, http.StatusConflict, "campaign_paused", "Resume the campaign before sending it.")
		return
	default:
		httpx.Error(w, http.StatusConflict, "campaign_not_sendable", "The campaign is not in a sendable state.")
		return
	}

	tag, err := s.pool.Exec(r.Context(), `
		UPDATE campaigns
		SET status = 'processing', started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status IN ('draft', 'ready', 'scheduled')
	`, campaignID, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not start the campaign.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "campaign_busy", "The campaign is already being processed.")
		return
	}

	queued, queueErr := s.campaigns.Queue(r.Context(), campaignID, orgID)
	if queueErr != nil {
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE campaigns SET status = 'failed', updated_at = now()
			WHERE id = $1 AND organization_id = $2
		`, campaignID, orgID)
		s.writeQueueError(w, queueErr)
		return
	}

	updated, _ := s.loadCampaign(r.Context(), campaignID, orgID)
	httpx.JSON(w, http.StatusAccepted, map[string]any{
		"campaign":    updated,
		"queued_jobs": queued,
	})
}

func (s *Server) pauseCampaign(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE campaigns
		SET status = 'paused', paused_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2
		  AND status IN ('scheduled', 'processing', 'sending')
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		s.logger.Error("pause campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not pause the campaign.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "campaign_not_pausable", "The campaign cannot be paused in its current state.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) resumeCampaign(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	campaignID := r.PathValue("id")

	tag, err := s.pool.Exec(r.Context(), `
		UPDATE campaigns
		SET status = 'sending', updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND status = 'paused'
	`, campaignID, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resume the campaign.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "campaign_not_paused", "The campaign is not paused.")
		return
	}

	queued, err := s.campaigns.Queue(r.Context(), campaignID, orgID)
	if err != nil {
		_, _ = s.pool.Exec(r.Context(), `
			UPDATE campaigns SET status = 'paused', updated_at = now()
			WHERE id = $1 AND organization_id = $2
		`, campaignID, orgID)
		s.writeQueueError(w, err)
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"status":      "sending",
		"queued_jobs": queued,
	})
}

func (s *Server) cancelCampaign(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	campaignID := r.PathValue("id")

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not cancel the campaign.")
		return
	}
	defer tx.Rollback(r.Context())

	tag, err := tx.Exec(r.Context(), `
		UPDATE campaigns
		SET status = 'cancelled', completed_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2
		  AND status IN ('draft', 'ready', 'scheduled', 'processing', 'sending', 'paused')
	`, campaignID, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not cancel the campaign.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "campaign_not_cancellable", "The campaign cannot be cancelled in its current state.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE delivery_jobs
		SET status = 'cancelled', last_error = 'campaign_cancelled', updated_at = now()
		WHERE campaign_id = $1 AND organization_id = $2
		  AND status IN ('queued', 'processing', 'deferred')
	`, campaignID, orgID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not cancel pending deliveries.")
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE campaign_recipients
		SET status = 'skipped'
		WHERE campaign_id = $1 AND organization_id = $2 AND status = 'pending'
	`, campaignID, orgID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update pending recipients.")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not cancel the campaign.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) writeQueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, campaigns.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
	case errors.Is(err, campaigns.ErrSegmentNotFound), errors.Is(err, campaigns.ErrInvalidSegment):
		httpx.Error(w, http.StatusBadRequest, "invalid_segment", "The campaign segment is unavailable or has invalid rules.")
	case errors.Is(err, campaigns.ErrInvalidState):
		httpx.Error(w, http.StatusConflict, "campaign_invalid_state", "The campaign is not in a sendable state.")
	default:
		s.logger.Error("queue campaign", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not queue the campaign.")
	}
}
