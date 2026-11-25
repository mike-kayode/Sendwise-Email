package api

import (
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

type analyticsOverview struct {
	Contacts      int64                 `json:"contacts"`
	Subscribed    int64                 `json:"subscribed"`
	CampaignsSent int64                 `json:"campaigns_sent"`
	Submitted     int64                 `json:"emails_submitted"`
	Opens         int64                 `json:"opens"`
	Clicks        int64                 `json:"clicks"`
	Bounces       int64                 `json:"bounces"`
	Complaints    int64                 `json:"complaints"`
	Unsubscribes  int64                 `json:"unsubscribes"`
	OpenRate      float64               `json:"open_rate"`
	ClickRate     float64               `json:"click_rate"`
	BounceRate    float64               `json:"bounce_rate"`
	EgressHealth  *egressHealthResponse `json:"egress_health,omitempty"`
}

func (s *Server) analyticsOverview(w http.ResponseWriter, r *http.Request) {
	var result analyticsOverview
	err := s.pool.QueryRow(r.Context(), `
		SELECT
			(SELECT count(*) FROM contacts WHERE organization_id = $1),
			(SELECT count(*) FROM contacts WHERE organization_id = $1 AND status = 'subscribed'),
			(SELECT count(*) FROM campaigns WHERE organization_id = $1 AND status IN ('sending', 'completed')),
			(SELECT count(*) FROM delivery_jobs WHERE organization_id = $1 AND status = 'submitted' AND created_at >= now() - interval '30 days'),
			(SELECT count(*) FROM message_events WHERE organization_id = $1 AND event_type = 'opened' AND occurred_at >= now() - interval '30 days'),
			(SELECT count(*) FROM message_events WHERE organization_id = $1 AND event_type = 'clicked' AND occurred_at >= now() - interval '30 days'),
			(SELECT count(*) FROM message_events WHERE organization_id = $1 AND event_type = 'bounced' AND occurred_at >= now() - interval '30 days'),
			(SELECT count(*) FROM suppressions WHERE organization_id = $1 AND reason = 'complaint'),
			(SELECT count(*) FROM suppressions WHERE organization_id = $1 AND reason = 'unsubscribe')
	`, s.organizationID(r)).Scan(
		&result.Contacts, &result.Subscribed, &result.CampaignsSent, &result.Submitted,
		&result.Opens, &result.Clicks, &result.Bounces, &result.Complaints, &result.Unsubscribes,
	)
	if err != nil {
		s.logger.Error("analytics overview", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load analytics.")
		return
	}

	if result.Submitted > 0 {
		result.OpenRate = percent(result.Opens, result.Submitted)
		result.ClickRate = percent(result.Clicks, result.Submitted)
		result.BounceRate = percent(result.Bounces, result.Submitted)
	}
	if egress, err := s.loadDeliveryEgressHealth(r.Context()); err == nil {
		result.EgressHealth = egress
	} else {
		s.logger.Warn("load delivery egress health for overview", "error", err)
	}

	httpx.JSON(w, http.StatusOK, result)
}

func (s *Server) campaignReport(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	campaignID := r.PathValue("id")

	c, err := s.loadCampaign(r.Context(), campaignID, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "The campaign could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load campaign report", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the report.")
		return
	}

	recipientRows, err := s.pool.Query(r.Context(), `
		SELECT status, count(*)
		FROM campaign_recipients
		WHERE campaign_id = $1 AND organization_id = $2
		GROUP BY status
	`, campaignID, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient statistics.")
		return
	}
	defer recipientRows.Close()

	recipients := make(map[string]int64)
	var totalRecipients int64
	for recipientRows.Next() {
		var status string
		var count int64
		if err := recipientRows.Scan(&status, &count); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient statistics.")
			return
		}
		recipients[status] = count
		totalRecipients += count
	}

	eventRows, err := s.pool.Query(r.Context(), `
		SELECT event_type, count(*)
		FROM message_events
		WHERE campaign_id = $1 AND organization_id = $2
		GROUP BY event_type
	`, campaignID, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load engagement statistics.")
		return
	}
	defer eventRows.Close()

	events := make(map[string]int64)
	for eventRows.Next() {
		var eventType string
		var count int64
		if err := eventRows.Scan(&eventType, &count); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load engagement statistics.")
			return
		}
		events[eventType] = count
	}

	linkRows, err := s.pool.Query(r.Context(), `
		SELECT url, count(*)
		FROM message_events
		WHERE campaign_id = $1 AND organization_id = $2
		  AND event_type = 'clicked' AND url IS NOT NULL
		GROUP BY url
		ORDER BY count(*) DESC
		LIMIT 10
	`, campaignID, orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load link statistics.")
		return
	}
	defer linkRows.Close()

	type linkStat struct {
		URL    string `json:"url"`
		Clicks int64  `json:"clicks"`
	}
	topLinks := make([]linkStat, 0)
	for linkRows.Next() {
		var stat linkStat
		if err := linkRows.Scan(&stat.URL, &stat.Clicks); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load link statistics.")
			return
		}
		topLinks = append(topLinks, stat)
	}

	var delivered, uniqueOpens, openEvents, uniqueClicks, clickEvents, failed, unsubscribes, complaints int64
	if err := s.pool.QueryRow(r.Context(), `
		SELECT
			count(*) FILTER (WHERE delivery_status IN ('submitted', 'accepted', 'delivered')),
			count(*) FILTER (WHERE open_count > 0),
			COALESCE(sum(open_count), 0),
			count(*) FILTER (WHERE click_count > 0),
			COALESCE(sum(click_count), 0),
			count(*) FILTER (WHERE delivery_status = 'failed'),
			count(*) FILTER (WHERE delivery_status = 'unsubscribed'),
			count(*) FILTER (WHERE delivery_status = 'complained')
		FROM campaign_recipient_tracking
		WHERE organization_id = $1 AND campaign_id = $2
	`, orgID, campaignID).Scan(
		&delivered, &uniqueOpens, &openEvents, &uniqueClicks, &clickEvents,
		&failed, &unsubscribes, &complaints,
	); err != nil {
		s.logger.Error("load recipient tracking summary", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient tracking summary.")
		return
	}

	journeyFunnel := make(map[string]int64)
	journeyRows, err := s.pool.Query(r.Context(), `
		SELECT journey_status, count(*)::bigint
		FROM campaign_recipient_tracking
		WHERE organization_id = $1::uuid AND campaign_id = $2::uuid
		GROUP BY journey_status
	`, orgID, campaignID)
	if err != nil {
		s.logger.Error("load campaign delivery journey funnel", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load delivery journey funnel.")
		return
	}
	for journeyRows.Next() {
		var stage string
		var count int64
		if err := journeyRows.Scan(&stage, &count); err != nil {
			journeyRows.Close()
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load delivery journey funnel.")
			return
		}
		journeyFunnel[stage] = count
	}
	if err := journeyRows.Err(); err != nil {
		journeyRows.Close()
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load delivery journey funnel.")
		return
	}
	journeyRows.Close()
	journeyFunnel["opened"] = uniqueOpens
	journeyFunnel["clicked"] = uniqueClicks
	stuckAfter := s.cfg.DeliveryJourneyStuckAfter
	if stuckAfter <= 0 {
		stuckAfter = 30 * time.Minute
	}
	var stuckCount int64
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)::bigint
		FROM campaign_recipient_tracking crt
		JOIN delivery_jobs dj
		  ON dj.organization_id = crt.organization_id AND dj.id = crt.delivery_job_id
		WHERE crt.organization_id = $1::uuid AND crt.campaign_id = $2::uuid
		  AND dj.status = 'submitted'
		  AND crt.journey_status IN ('submitted_to_mail_server', 'relaying')
		  AND COALESCE(crt.journey_last_stalwart_event_at, crt.accepted_at, dj.updated_at)
		      <= now() - $3::interval
	`, orgID, campaignID, fmt.Sprintf("%f seconds", stuckAfter.Seconds())).Scan(&stuckCount); err != nil {
		s.logger.Error("count stuck campaign delivery journeys", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not count stuck delivery journeys.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"campaign": c,
		"recipients": map[string]any{
			"total":     totalRecipients,
			"by_status": recipients,
		},
		"events": map[string]any{
			"by_type": events,
		},
		"journey": map[string]any{
			"funnel": journeyFunnel,
			"stuck":  stuckCount,
		},
		"rates": map[string]float64{
			"open_rate":      percent(events["opened"], totalRecipients),
			"click_rate":     percent(events["clicked"], totalRecipients),
			"bounce_rate":    percent(events["bounced"], totalRecipients),
			"delivered_rate": percent(delivered, totalRecipients),
		},
		"summary": map[string]int64{
			"total_recipients": totalRecipients,
			"sent":             delivered,
			"delivered":        delivered,
			"unique_opens":     uniqueOpens,
			"open_events":      openEvents,
			"unique_clicks":    uniqueClicks,
			"click_events":     clickEvents,
			"failed":           failed,
			"unsubscribes":     unsubscribes,
			"complaints":       complaints,
		},
		"top_links": topLinks,
	})
}

func percent(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator) * 100
	return math.Round(value*100) / 100
}

type campaignRecipientRow struct {
	RecipientID              string  `json:"recipient_id"`
	Name                     string  `json:"name"`
	Email                    string  `json:"email"`
	DeliveryStatus           string  `json:"delivery_status"`
	QueuedAt                 *string `json:"queued_at"`
	AcceptedAt               *string `json:"accepted_at"`
	DeliveredAt              *string `json:"delivered_at"`
	OpenCount                int64   `json:"open_count"`
	FirstOpenedAt            *string `json:"first_opened_at"`
	LastOpenedAt             *string `json:"last_opened_at"`
	ClickCount               int64   `json:"click_count"`
	LastClickedAt            *string `json:"last_clicked_at"`
	LastActivityAt           *string `json:"last_activity_at"`
	EngagementClassification string  `json:"engagement_classification"`
	JourneyStatus            string  `json:"journey_status"`
	JourneyReason            string  `json:"journey_reason,omitempty"`
	IsDelayed                bool    `json:"is_delayed"`
	DelayedReason            string  `json:"delayed_reason,omitempty"`
}

func (s *Server) listCampaignRecipients(w http.ResponseWriter, r *http.Request) {
	orgID, campaignID := s.organizationID(r), r.PathValue("id")
	if _, err := s.loadCampaign(r.Context(), campaignID, orgID); err != nil {
		httpx.Error(w, http.StatusNotFound, "campaign_not_found", "Campaign not found.")
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	opened := strings.TrimSpace(r.URL.Query().Get("opened"))
	clicked := strings.TrimSpace(r.URL.Query().Get("clicked"))
	classification := strings.TrimSpace(r.URL.Query().Get("classification"))
	stage := strings.TrimSpace(r.URL.Query().Get("stage"))
	if stage != "" && !validJourneyStage(stage) {
		httpx.Error(w, http.StatusBadRequest, "invalid_stage", "The requested delivery journey stage is invalid.")
		return
	}
	sort := strings.TrimSpace(r.URL.Query().Get("sort"))
	limit, offset := 25, 0
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
			httpx.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be non-negative.")
			return
		}
		offset = value
	}
	orderBy := "crt.last_opened_at DESC NULLS LAST, crt.last_clicked_at DESC NULLS LAST, crt.email"
	switch sort {
	case "open_count":
		orderBy = "crt.open_count DESC, crt.email"
	case "click_count":
		orderBy = "crt.click_count DESC, crt.email"
	case "delivery_date":
		orderBy = "crt.delivered_at DESC NULLS LAST, crt.accepted_at DESC NULLS LAST"
	case "last_activity":
		orderBy = "GREATEST(crt.last_opened_at, crt.last_clicked_at) DESC NULLS LAST"
	}
	stuckAfter := s.cfg.DeliveryJourneyStuckAfter
	if stuckAfter <= 0 {
		stuckAfter = 30 * time.Minute
	}
	stuckAfterInterval := fmt.Sprintf("%f seconds", stuckAfter.Seconds())
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM campaign_recipient_tracking crt
		JOIN contacts c ON c.id = crt.contact_id
		WHERE crt.organization_id = $1 AND crt.campaign_id = $2
		  AND ($3::text = '' OR crt.delivery_status = $3::text)
		  AND ($4::text = '' OR crt.email ILIKE '%' || $4::text || '%' OR c.first_name ILIKE '%' || $4::text || '%' OR c.last_name ILIKE '%' || $4::text || '%')
		  AND ($5::text = '' OR ($5::text = 'yes' AND crt.open_count > 0) OR ($5::text = 'no' AND crt.open_count = 0))
		  AND ($6::text = '' OR ($6::text = 'yes' AND crt.click_count > 0) OR ($6::text = 'no' AND crt.click_count = 0))
		  AND ($7::text = '' OR crt.engagement_classification = $7::text)
		  AND ($8::text = '' OR crt.journey_status = $8::text
		       OR ($8::text = 'deferred' AND crt.journey_status IN ('deferred_by_sendwise', 'deferred_by_recipient'))
		       OR ($8::text = 'opened' AND crt.open_count > 0)
		       OR ($8::text = 'clicked' AND crt.click_count > 0))
	`, orgID, campaignID, status, search, opened, clicked, classification, stage).Scan(&total); err != nil {
		s.logger.Error("count campaign recipients", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not count campaign recipients.")
		return
	}
	query := `
		SELECT crt.recipient_id::text,
		       COALESCE(NULLIF(btrim(c.first_name || ' ' || c.last_name), ''), ''),
		       crt.email, crt.delivery_status,
		       crt.queued_at::text, crt.accepted_at::text, crt.delivered_at::text,
		       crt.open_count, crt.first_opened_at::text, crt.last_opened_at::text,
		       crt.click_count, crt.last_clicked_at::text,
		       GREATEST(crt.last_opened_at, crt.last_clicked_at)::text,
		       crt.engagement_classification, crt.journey_status,
		       COALESCE(crt.journey_reason, ''),
		       (crt.journey_status IN ('submitted_to_mail_server', 'relaying')
		        AND COALESCE(crt.journey_last_stalwart_event_at, crt.accepted_at, dj.updated_at, crt.updated_at)
		            <= now() - $11::interval) AS is_delayed
		FROM campaign_recipient_tracking crt
		JOIN contacts c ON c.id = crt.contact_id
		LEFT JOIN delivery_jobs dj ON dj.id = crt.delivery_job_id
		WHERE crt.organization_id = $1 AND crt.campaign_id = $2
		  AND ($3::text = '' OR crt.delivery_status = $3::text)
		  AND ($4::text = '' OR crt.email ILIKE '%' || $4::text || '%' OR c.first_name ILIKE '%' || $4::text || '%' OR c.last_name ILIKE '%' || $4::text || '%')
		  AND ($5::text = '' OR ($5::text = 'yes' AND crt.open_count > 0) OR ($5::text = 'no' AND crt.open_count = 0))
		  AND ($6::text = '' OR ($6::text = 'yes' AND crt.click_count > 0) OR ($6::text = 'no' AND crt.click_count = 0))
		  AND ($7::text = '' OR crt.engagement_classification = $7::text)
		  AND ($8::text = '' OR crt.journey_status = $8::text
		       OR ($8::text = 'deferred' AND crt.journey_status IN ('deferred_by_sendwise', 'deferred_by_recipient'))
		       OR ($8::text = 'opened' AND crt.open_count > 0)
		       OR ($8::text = 'clicked' AND crt.click_count > 0))
		ORDER BY ` + orderBy + `
		LIMIT $9::integer OFFSET $10::integer
	`
	rows, err := s.pool.Query(r.Context(), query, orgID, campaignID, status, search, opened, clicked, classification, stage, limit, offset, stuckAfterInterval)
	if err != nil {
		s.logger.Error("list campaign recipients", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list campaign recipients.")
		return
	}
	defer rows.Close()
	result := make([]campaignRecipientRow, 0, limit)
	for rows.Next() {
		var row campaignRecipientRow
		if err := rows.Scan(&row.RecipientID, &row.Name, &row.Email, &row.DeliveryStatus,
			&row.QueuedAt, &row.AcceptedAt, &row.DeliveredAt, &row.OpenCount,
			&row.FirstOpenedAt, &row.LastOpenedAt, &row.ClickCount, &row.LastClickedAt,
			&row.LastActivityAt, &row.EngagementClassification, &row.JourneyStatus,
			&row.JourneyReason, &row.IsDelayed,
		); err != nil {
			s.logger.Error("scan campaign recipient", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list campaign recipients.")
			return
		}
		if row.IsDelayed {
			row.DelayedReason = row.JourneyReason
		}
		result = append(result, row)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data":       result,
		"pagination": map[string]int{"total": total, "limit": limit, "offset": offset},
	})
}

func (s *Server) getCampaignRecipientActivity(w http.ResponseWriter, r *http.Request) {
	orgID, campaignID, recipientID := s.organizationID(r), r.PathValue("id"), r.PathValue("recipientId")
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM campaign_recipient_tracking
			WHERE organization_id = $1 AND campaign_id = $2 AND recipient_id = $3
		)
	`, orgID, campaignID, recipientID).Scan(&exists); err != nil || !exists {
		httpx.Error(w, http.StatusNotFound, "recipient_not_found", "Recipient activity not found.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT event_type, event_timestamp::text, COALESCE(ip_address::text, ''),
		       COALESCE(user_agent, ''), COALESCE(device_type, ''),
		       COALESCE(browser, ''), COALESCE(operating_system, ''),
		       COALESCE(country, ''), COALESCE(region, ''), COALESCE(city, ''),
		       COALESCE(destination_url, ''), event_metadata,
		       is_automated, automation_type
		FROM email_tracking_events
		WHERE organization_id = $1 AND campaign_id = $2 AND recipient_id = $3
		ORDER BY event_timestamp ASC, id ASC
	`, orgID, campaignID, recipientID)
	if err != nil {
		s.logger.Error("list recipient activity", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient activity.")
		return
	}
	defer rows.Close()
	type activity struct {
		EventType       string `json:"event_type"`
		Timestamp       string `json:"timestamp"`
		IPAddress       string `json:"ip_address"`
		UserAgent       string `json:"user_agent"`
		DeviceType      string `json:"device_type"`
		Browser         string `json:"browser"`
		OperatingSystem string `json:"operating_system"`
		Country         string `json:"country"`
		Region          string `json:"region"`
		City            string `json:"city"`
		DestinationURL  string `json:"destination_url"`
		Metadata        []byte `json:"metadata"`
		IsAutomated     bool   `json:"is_automated"`
		AutomationType  string `json:"automation_type"`
	}
	result := make([]activity, 0)
	for rows.Next() {
		var item activity
		if err := rows.Scan(&item.EventType, &item.Timestamp, &item.IPAddress, &item.UserAgent,
			&item.DeviceType, &item.Browser, &item.OperatingSystem, &item.Country, &item.Region,
			&item.City, &item.DestinationURL, &item.Metadata, &item.IsAutomated, &item.AutomationType,
		); err != nil {
			s.logger.Error("scan recipient activity", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load recipient activity.")
			return
		}
		result = append(result, item)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": result})
}

func (s *Server) exportCampaignRecipients(w http.ResponseWriter, r *http.Request) {
	orgID, campaignID := s.organizationID(r), r.PathValue("id")
	rows, err := s.pool.Query(r.Context(), `
		SELECT crt.email, crt.delivery_status, crt.accepted_at, crt.delivered_at,
		       crt.open_count, crt.first_opened_at, crt.last_opened_at,
		       crt.click_count, crt.last_clicked_at,
		       crt.engagement_classification, crt.journey_status,
		       COALESCE(last_event.smtp_code::text, ''),
		       COALESCE(last_event.reason, ''),
		       COALESCE(last_event.attempt_number, 0),
		       COALESCE(last_event.remote_mx_host, '')
		FROM campaign_recipient_tracking crt
		LEFT JOIN LATERAL (
			SELECT smtp_code, reason, attempt_number, remote_mx_host
			FROM delivery_journey_events
			WHERE organization_id = crt.organization_id
			  AND delivery_job_id = crt.delivery_job_id
			ORDER BY event_timestamp DESC, id DESC
			LIMIT 1
		) last_event ON true
		WHERE crt.organization_id = $1::uuid AND crt.campaign_id = $2::uuid
		ORDER BY crt.email
	`, orgID, campaignID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export recipient tracking.")
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="campaign-recipient-tracking.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"recipient_email", "delivery_status", "accepted_at", "delivered_at", "open_count", "first_opened_at", "last_opened_at", "click_count", "last_clicked_at", "engagement_classification", "last_stage", "last_smtp_code", "last_reason", "attempts", "remote_mx_host"})
	for rows.Next() {
		var email, status, classification, journeyStatus, smtpCode, reason, remoteHost string
		var accepted, delivered, firstOpen, lastOpen, lastClick *time.Time
		var opens, clicks int64
		var attempts int
		if err := rows.Scan(&email, &status, &accepted, &delivered, &opens, &firstOpen, &lastOpen, &clicks, &lastClick, &classification,
			&journeyStatus, &smtpCode, &reason, &attempts, &remoteHost); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export recipient tracking.")
			return
		}
		writer.Write([]string{
			email, status, timeValue(accepted), timeValue(delivered),
			strconv.FormatInt(opens, 10), timeValue(firstOpen), timeValue(lastOpen),
			strconv.FormatInt(clicks, 10), timeValue(lastClick), classification,
			journeyStatus, smtpCode, reason, strconv.Itoa(attempts), remoteHost,
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		s.logger.Error("flush recipient tracking CSV", "error", err)
	}
}

func timeValue(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
