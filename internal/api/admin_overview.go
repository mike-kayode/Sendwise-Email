package api

import (
	"net/http"
	"time"

	"github.com/khayode/email-platform/internal/httpx"
)

type adminOperationalMetrics struct {
	DomainCount      int       `json:"domain_count"`
	CampaignCount    int       `json:"campaign_count"`
	MessagesToday    int       `json:"messages_today"`
	Messages7Days    int       `json:"messages_7_days"`
	Messages30Days   int       `json:"messages_30_days"`
	DeliveryRate30d  float64   `json:"delivery_rate_30_days"`
	BounceRate30d    float64   `json:"bounce_rate_30_days"`
	ComplaintRate30d float64   `json:"complaint_rate_30_days"`
	QueueDepth       int       `json:"queue_depth"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s *Server) getAdminOperationalMetrics(w http.ResponseWriter, r *http.Request) {
	var metrics adminOperationalMetrics
	err := s.pool.QueryRow(r.Context(), `
		WITH deliveries AS (
			SELECT created_at, status
			FROM delivery_jobs
			UNION ALL
			SELECT created_at, status
			FROM transactional_messages
		),
		last_30_days AS (
			SELECT status
			FROM deliveries
			WHERE created_at >= now() - interval '30 days'
		)
		SELECT
			(SELECT count(*)::int FROM sending_domains),
			(SELECT count(*)::int FROM campaigns),
			(SELECT count(*)::int FROM deliveries WHERE created_at >= now() - interval '1 day'),
			(SELECT count(*)::int FROM deliveries WHERE created_at >= now() - interval '7 days'),
			(SELECT count(*)::int FROM deliveries WHERE created_at >= now() - interval '30 days'),
			(SELECT count(*)::int FROM deliveries WHERE status IN ('queued', 'processing', 'deferred', 'sending')),
			CASE WHEN count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered', 'bounced', 'complained', 'failed')) = 0 THEN 0
				ELSE 100.0 * count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered')) /
					count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered', 'bounced', 'complained', 'failed'))
			END,
			CASE WHEN count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered', 'bounced', 'complained', 'failed')) = 0 THEN 0
				ELSE 100.0 * count(*) FILTER (WHERE status = 'bounced') /
					count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered', 'bounced', 'complained', 'failed'))
			END,
			CASE WHEN count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered', 'bounced', 'complained', 'failed')) = 0 THEN 0
				ELSE 100.0 * count(*) FILTER (WHERE status = 'complained') /
					count(*) FILTER (WHERE status IN ('accepted', 'submitted', 'delivered', 'bounced', 'complained', 'failed'))
			END,
			now()
		FROM last_30_days
	`).Scan(
		&metrics.DomainCount, &metrics.CampaignCount, &metrics.MessagesToday,
		&metrics.Messages7Days, &metrics.Messages30Days, &metrics.QueueDepth,
		&metrics.DeliveryRate30d, &metrics.BounceRate30d, &metrics.ComplaintRate30d,
		&metrics.UpdatedAt,
	)
	if err != nil {
		s.logger.Error("load admin operational metrics", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load operational metrics.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": metrics})
}
