package api

import (
	"errors"
	"math"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

type analyticsOverview struct {
	Contacts      int64   `json:"contacts"`
	Subscribed    int64   `json:"subscribed"`
	CampaignsSent int64   `json:"campaigns_sent"`
	Submitted     int64   `json:"emails_submitted"`
	Opens         int64   `json:"opens"`
	Clicks        int64   `json:"clicks"`
	Bounces       int64   `json:"bounces"`
	Complaints    int64   `json:"complaints"`
	Unsubscribes  int64   `json:"unsubscribes"`
	OpenRate      float64 `json:"open_rate"`
	ClickRate     float64 `json:"click_rate"`
	BounceRate    float64 `json:"bounce_rate"`
}

func (s *Server) analyticsOverview(w http.ResponseWriter, r *http.Request) {
	var result analyticsOverview
	err := s.pool.QueryRow(r.Context(), `
		SELECT
			(SELECT count(*) FROM contacts WHERE organization_id = $1),
			(SELECT count(*) FROM contacts WHERE organization_id = $1 AND status = 'subscribed'),
			(SELECT count(*) FROM campaigns WHERE organization_id = $1 AND status IN ('sending', 'completed')),
			(SELECT count(*) FROM delivery_jobs WHERE organization_id = $1 AND status = 'submitted'),
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
		URL   string `json:"url"`
		Clicks int64 `json:"clicks"`
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

	httpx.JSON(w, http.StatusOK, map[string]any{
		"campaign": c,
		"recipients": map[string]any{
			"total":     totalRecipients,
			"by_status": recipients,
		},
		"events": map[string]any{
			"by_type": events,
		},
		"rates": map[string]float64{
			"open_rate":   percent(events["opened"], totalRecipients),
			"click_rate":  percent(events["clicked"], totalRecipients),
			"bounce_rate": percent(events["bounced"], totalRecipients),
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
