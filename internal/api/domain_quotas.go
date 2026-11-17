package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khayode/email-platform/internal/httpx"
	"github.com/khayode/email-platform/internal/quota"
)

type quotaOverrideInput struct {
	Stream         string `json:"stream"`
	DailyLimit     *int64 `json:"daily_limit"`
	MonthlyLimit   *int64 `json:"monthly_limit"`
	PerSecondLimit *int64 `json:"per_second_limit"`
	Unlimited      bool   `json:"unlimited"`
}

type quotaRequestInput struct {
	Stream         string `json:"stream"`
	DailyLimit     *int64 `json:"daily_limit"`
	MonthlyLimit   *int64 `json:"monthly_limit"`
	PerSecondLimit *int64 `json:"per_second_limit"`
	Reason         string `json:"reason"`
}

type quotaRequestRecord struct {
	ID             string    `json:"id"`
	Stream         string    `json:"stream"`
	DailyLimit     *int64    `json:"daily_limit"`
	MonthlyLimit   *int64    `json:"monthly_limit"`
	PerSecondLimit *int64    `json:"per_second_limit"`
	Reason         string    `json:"reason"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type domainQuotaRisk struct {
	YoungDomain     bool    `json:"young_domain"`
	VerifiedDays    int     `json:"verified_days"`
	HighBounceRate  bool    `json:"high_bounce_rate"`
	BounceRate7Days float64 `json:"bounce_rate_7_days"`
}

type domainQuotaData struct {
	DomainID string                    `json:"domain_id"`
	Streams  map[string]quota.Snapshot `json:"streams"`
	Warmup   customerWarmupSummary     `json:"warmup"`
	Risk     domainQuotaRisk           `json:"risk,omitempty"`
	Requests []quotaRequestRecord      `json:"requests,omitempty"`
}

type platformQuotaDefaultInput struct {
	Stream         string `json:"stream"`
	DailyLimit     *int64 `json:"daily_limit"`
	MonthlyLimit   *int64 `json:"monthly_limit"`
	PerSecondLimit int64  `json:"per_second_limit"`
}

func (s *Server) getCustomerDomainQuotas(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	data, err := s.loadDomainQuotaData(r, domainID, s.organizationID(r), false)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, quota.ErrDomainNotFound) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load customer domain quotas", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_load_failed", "Domain quota usage could not be loaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) createCustomerQuotaRequest(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input quotaRequestInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide a valid quota increase request.")
		return
	}
	input.Stream = strings.TrimSpace(input.Stream)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validQuotaStream(input.Stream) || input.Reason == "" || len(input.Reason) > 1000 ||
		(input.DailyLimit == nil && input.MonthlyLimit == nil && input.PerSecondLimit == nil) ||
		!validOptionalLimit(input.DailyLimit) || !validOptionalLimit(input.MonthlyLimit) ||
		!validOptionalLimit(input.PerSecondLimit) ||
		(input.PerSecondLimit != nil && *input.PerSecondLimit > int64(s.cfg.PlatformMaxDomainRate)) {
		httpx.Error(w, http.StatusBadRequest, "invalid_quota_request", "Specify a valid stream, at least one positive requested limit, and a reason.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		INSERT INTO domain_quota_requests
			(domain_id, requested_by, stream, requested_daily_limit, requested_monthly_limit,
			 requested_per_second_limit, reason)
		SELECT d.id, $3, $4, $5, $6, $7, $8
		FROM sending_domains d
		WHERE d.id = $1 AND d.organization_id = $2
	`, domainID, s.organizationID(r), s.claims(r).Subject, input.Stream,
		input.DailyLimit, input.MonthlyLimit, input.PerSecondLimit, input.Reason)
	if err != nil {
		s.logger.Error("create customer quota request", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_request_failed", "The quota increase request could not be recorded.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"status": "pending"})
}

func (s *Server) getAdminDomainQuotas(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	data, err := s.loadDomainQuotaData(r, domainID, "", true)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, quota.ErrDomainNotFound) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load admin domain quotas", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_load_failed", "Domain quota data could not be loaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) updateAdminDomainQuotas(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input struct {
		Streams   []quotaOverrideInput `json:"streams"`
		UpdatedBy string               `json:"updated_by"`
		Reason    string               `json:"reason"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide valid quota overrides.")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(input.UpdatedBy); err != nil || input.Reason == "" || len(input.Reason) > 500 ||
		len(input.Streams) != 2 {
		httpx.Error(w, http.StatusBadRequest, "invalid_quota_update", "Both stream overrides, an admin user ID, and a reason are required.")
		return
	}
	seen := map[string]bool{}
	for _, stream := range input.Streams {
		if !validQuotaStream(stream.Stream) || seen[stream.Stream] ||
			!validOptionalLimit(stream.DailyLimit) || !validOptionalLimit(stream.MonthlyLimit) ||
			!validOptionalLimit(stream.PerSecondLimit) ||
			(stream.PerSecondLimit != nil && *stream.PerSecondLimit > int64(s.cfg.PlatformMaxDomainRate)) {
			httpx.Error(w, http.StatusBadRequest, "invalid_quota_update", "Quota overrides must be positive, unique per stream, and within the platform rate ceiling.")
			return
		}
		seen[stream.Stream] = true
	}
	if !seen[quota.StreamMarketing] || !seen[quota.StreamTransactional] {
		httpx.Error(w, http.StatusBadRequest, "invalid_quota_update", "Both marketing and transactional stream overrides are required.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin domain quota update", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_update_failed", "Domain quotas could not be updated.")
		return
	}
	defer tx.Rollback(r.Context())
	var exists bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM sending_domains WHERE id = $1)`, domainID).Scan(&exists); err != nil {
		s.logger.Error("check domain before quota update", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_update_failed", "Domain quotas could not be updated.")
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	for _, stream := range input.Streams {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO domain_quotas
				(domain_id, stream, daily_limit, monthly_limit, per_second_limit,
				 unlimited, notes, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (domain_id, stream) DO UPDATE SET
				daily_limit = EXCLUDED.daily_limit,
				monthly_limit = EXCLUDED.monthly_limit,
				per_second_limit = EXCLUDED.per_second_limit,
				unlimited = EXCLUDED.unlimited,
				notes = EXCLUDED.notes,
				updated_by = EXCLUDED.updated_by,
				updated_at = EXCLUDED.updated_at
		`, domainID, stream.Stream, stream.DailyLimit, stream.MonthlyLimit,
			stream.PerSecondLimit, stream.Unlimited, input.Reason, input.UpdatedBy); err != nil {
			s.logger.Error("write domain quota override", "error", err, "domain_id", domainID, "stream", stream.Stream)
			httpx.Error(w, http.StatusInternalServerError, "quota_update_failed", "Domain quotas could not be updated.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit domain quota update", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_update_failed", "Domain quotas could not be updated.")
		return
	}
	data, err := s.loadDomainQuotaData(r, domainID, "", true)
	if err != nil {
		s.logger.Error("reload domain quotas after update", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "quota_update_failed", "Domain quotas were updated but could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) getAdminQuotaDefaults(w http.ResponseWriter, r *http.Request) {
	defaults, err := s.loadPlatformQuotaDefaults(r)
	if err != nil {
		s.logger.Error("load platform quota defaults", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "quota_defaults_failed", "Platform quota defaults could not be loaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"defaults": defaults, "hard_rate_limit": s.cfg.PlatformMaxDomainRate,
	}})
}

func (s *Server) updateAdminQuotaDefaults(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Defaults  []platformQuotaDefaultInput `json:"defaults"`
		UpdatedBy string                      `json:"updated_by"`
		Reason    string                      `json:"reason"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide valid platform quota defaults.")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(input.UpdatedBy); err != nil || len(input.Defaults) != 2 ||
		input.Reason == "" || len(input.Reason) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_quota_defaults", "Both stream defaults, an admin user ID, and a reason are required.")
		return
	}
	seen := map[string]bool{}
	for _, item := range input.Defaults {
		if !validQuotaStream(item.Stream) || seen[item.Stream] ||
			!validOptionalLimit(item.DailyLimit) || !validOptionalLimit(item.MonthlyLimit) ||
			item.PerSecondLimit < 1 || item.PerSecondLimit > int64(s.cfg.PlatformMaxDomainRate) {
			httpx.Error(w, http.StatusBadRequest, "invalid_quota_defaults", "Defaults must be positive, unique per stream, and within the platform rate ceiling.")
			return
		}
		seen[item.Stream] = true
	}
	if !seen[quota.StreamMarketing] || !seen[quota.StreamTransactional] {
		httpx.Error(w, http.StatusBadRequest, "invalid_quota_defaults", "Both marketing and transactional defaults are required.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin platform quota defaults update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "quota_defaults_update_failed", "Platform quota defaults could not be updated.")
		return
	}
	defer tx.Rollback(r.Context())
	for _, item := range input.Defaults {
		if _, err := tx.Exec(r.Context(), `
			UPDATE platform_quota_defaults
			SET daily_limit = $2, monthly_limit = $3, per_second_limit = $4,
			    seeded_from_env = true, updated_by = $5, updated_at = now()
			WHERE stream = $1
		`, item.Stream, item.DailyLimit, item.MonthlyLimit, item.PerSecondLimit, input.UpdatedBy); err != nil {
			s.logger.Error("update platform quota default", "error", err, "stream", item.Stream)
			httpx.Error(w, http.StatusInternalServerError, "quota_defaults_update_failed", "Platform quota defaults could not be updated.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit platform quota defaults update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "quota_defaults_update_failed", "Platform quota defaults could not be updated.")
		return
	}
	defaults, err := s.loadPlatformQuotaDefaults(r)
	if err != nil {
		s.logger.Error("reload platform quota defaults", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "quota_defaults_update_failed", "Platform quota defaults were updated but could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"defaults": defaults, "hard_rate_limit": s.cfg.PlatformMaxDomainRate,
	}})
}

func (s *Server) getAdminQuotasNearLimit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		WITH usage AS (
			SELECT domain_id, stream, 'daily'::text AS window_name, sent_count::numeric AS used
			FROM domain_usage
			WHERE period = 'day' AND period_start = (now() AT TIME ZONE 'UTC')::date
			UNION ALL
			SELECT domain_id, stream, 'monthly', sent_count::numeric
			FROM domain_usage
			WHERE period = 'month'
			  AND period_start = date_trunc('month', now() AT TIME ZONE 'UTC')::date
			UNION ALL
			SELECT domain_id, stream, 'per_second', sent_count::numeric
			FROM domain_rate_usage
			WHERE second_start = date_trunc('second', now())
		), effective AS (
			SELECT u.domain_id, u.stream, u.window_name, u.used,
			       CASE u.window_name
			         WHEN 'daily' THEN COALESCE(dq.daily_limit, pd.daily_limit)
			         WHEN 'monthly' THEN COALESCE(dq.monthly_limit, pd.monthly_limit)
			         ELSE COALESCE(dq.per_second_limit, pd.per_second_limit)
			       END AS quota_limit,
			       COALESCE(dq.unlimited, false) AS unlimited
			FROM usage u
			JOIN platform_quota_defaults pd ON pd.stream = u.stream
			LEFT JOIN domain_quotas dq ON dq.domain_id = u.domain_id AND dq.stream = u.stream
		)
		SELECT d.id::text, d.domain, o.name, o.slug, max(100.0 * e.used / e.quota_limit::numeric)
		FROM effective e
		JOIN sending_domains d ON d.id = e.domain_id
		JOIN organizations o ON o.id = d.organization_id
		WHERE e.quota_limit > 0
		  AND (e.window_name = 'per_second' OR NOT e.unlimited)
		  AND 100.0 * e.used / e.quota_limit::numeric >= 80
		GROUP BY d.id, d.domain, o.name, o.slug
		ORDER BY max(100.0 * e.used / e.quota_limit::numeric) DESC, d.domain
		LIMIT 100
	`)
	if err != nil {
		s.logger.Error("query domains near quota limits", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "quota_overview_failed", "Domains near quota limits could not be loaded.")
		return
	}
	defer rows.Close()
	type nearLimitDomain struct {
		ID          string                    `json:"id"`
		Domain      string                    `json:"domain"`
		Workspace   string                    `json:"workspace_name"`
		WorkspaceID string                    `json:"workspace_slug"`
		Percent     float64                   `json:"highest_usage_percent"`
		Streams     map[string]quota.Snapshot `json:"streams"`
	}
	domains := make([]nearLimitDomain, 0)
	for rows.Next() {
		var domain nearLimitDomain
		if err := rows.Scan(&domain.ID, &domain.Domain, &domain.Workspace, &domain.WorkspaceID, &domain.Percent); err != nil {
			s.logger.Error("scan domain near quota", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "quota_overview_failed", "Domains near quota limits could not be loaded.")
			return
		}
		domain.Streams = make(map[string]quota.Snapshot, 2)
		for _, stream := range []string{quota.StreamMarketing, quota.StreamTransactional} {
			snapshot, err := quota.GetSnapshot(r.Context(), s.pool, domain.ID, stream, int64(s.cfg.PlatformMaxDomainRate))
			if err != nil {
				s.logger.Error("load near-limit domain quota snapshot", "error", err, "domain_id", domain.ID, "stream", stream)
				httpx.Error(w, http.StatusInternalServerError, "quota_overview_failed", "Domains near quota limits could not be loaded.")
				return
			}
			domain.Streams[stream] = snapshot
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate domains near quota", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "quota_overview_failed", "Domains near quota limits could not be loaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": domains, "total": len(domains)})
}

func (s *Server) loadDomainQuotaData(r *http.Request, domainID, organizationID string, admin bool) (domainQuotaData, error) {
	var storedDomainID string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT id::text FROM sending_domains
		WHERE id = $1::uuid AND ($2 = '' OR organization_id::text = $2)
	`, domainID, organizationID).Scan(&storedDomainID); err != nil {
		return domainQuotaData{}, err
	}
	data := domainQuotaData{DomainID: storedDomainID, Streams: make(map[string]quota.Snapshot, 2)}
	for _, stream := range []string{quota.StreamMarketing, quota.StreamTransactional} {
		snapshot, err := quota.GetSnapshot(r.Context(), s.pool, domainID, stream, int64(s.cfg.PlatformMaxDomainRate))
		if err != nil {
			return domainQuotaData{}, err
		}
		data.Streams[stream] = snapshot
	}
	warmup, err := s.loadCustomerWarmupSummary(r.Context(), domainID, organizationID)
	if err != nil {
		return domainQuotaData{}, fmt.Errorf("load sending-domain warm-up summary: %w", err)
	}
	data.Warmup = warmup
	if !admin {
		return data, nil
	}
	if err := s.pool.QueryRow(r.Context(), `
		SELECT (verified_at IS NULL OR verified_at > now() - interval '14 days'),
		       GREATEST(0, floor(extract(epoch FROM (now() - COALESCE(verified_at, created_at))) / 86400))::integer,
		       CASE WHEN COALESCE(SUM(bounce_count + successful_count), 0) = 0 THEN 0
		            ELSE COALESCE(SUM(bounce_count), 0)::double precision /
		                 SUM(bounce_count + successful_count)::double precision END
		FROM sending_domains sd
		LEFT JOIN sending_domain_daily_stats stats
		  ON stats.organization_id = sd.organization_id
		 AND lower(stats.domain) = lower(sd.domain)
		 AND stats.sending_day >= (now() AT TIME ZONE 'UTC')::date - 7
		WHERE sd.id = $1::uuid
		GROUP BY sd.id
	`, domainID).Scan(
		&data.Risk.YoungDomain, &data.Risk.VerifiedDays, &data.Risk.BounceRate7Days,
	); err != nil {
		return domainQuotaData{}, fmt.Errorf("calculate domain quota risk: %w", err)
	}
	data.Risk.HighBounceRate = data.Risk.BounceRate7Days > s.cfg.MaxBounceRate
	requestRows, err := s.pool.Query(r.Context(), `
		SELECT id::text, stream, requested_daily_limit, requested_monthly_limit,
		       requested_per_second_limit, reason, status, created_at
		FROM domain_quota_requests
		WHERE domain_id = $1
		ORDER BY created_at DESC
		LIMIT 100
	`, domainID)
	if err != nil {
		return domainQuotaData{}, fmt.Errorf("load domain quota requests: %w", err)
	}
	defer requestRows.Close()
	for requestRows.Next() {
		var request quotaRequestRecord
		var daily, monthly, perSecond pgtype.Int8
		if err := requestRows.Scan(&request.ID, &request.Stream, &daily, &monthly, &perSecond,
			&request.Reason, &request.Status, &request.CreatedAt); err != nil {
			return domainQuotaData{}, fmt.Errorf("scan domain quota request: %w", err)
		}
		request.DailyLimit = int8Pointer(daily)
		request.MonthlyLimit = int8Pointer(monthly)
		request.PerSecondLimit = int8Pointer(perSecond)
		data.Requests = append(data.Requests, request)
	}
	if err := requestRows.Err(); err != nil {
		return domainQuotaData{}, fmt.Errorf("iterate domain quota requests: %w", err)
	}
	return data, nil
}

func (s *Server) loadPlatformQuotaDefaults(r *http.Request) ([]quota.Defaults, error) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT stream, daily_limit, monthly_limit, per_second_limit, updated_by::text, updated_at
		FROM platform_quota_defaults
		ORDER BY stream
	`)
	if err != nil {
		return nil, fmt.Errorf("query platform quota defaults: %w", err)
	}
	defer rows.Close()
	defaults := make([]quota.Defaults, 0, 2)
	for rows.Next() {
		var item quota.Defaults
		var daily, monthly pgtype.Int8
		var updatedByText *string
		if err := rows.Scan(&item.Stream, &daily, &monthly, &item.PerSecondLimit, &updatedByText, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan platform quota defaults: %w", err)
		}
		item.DailyLimit = int8Pointer(daily)
		item.MonthlyLimit = int8Pointer(monthly)
		item.UpdatedBy = updatedByText
		defaults = append(defaults, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate platform quota defaults: %w", err)
	}
	return defaults, nil
}

func int8Pointer(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func validQuotaStream(stream string) bool {
	return stream == quota.StreamMarketing || stream == quota.StreamTransactional
}

func validOptionalLimit(value *int64) bool {
	return value == nil || *value > 0
}
