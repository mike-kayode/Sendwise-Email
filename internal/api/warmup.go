package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/domainwarmup"
	"github.com/khayode/email-platform/internal/httpx"
)

type warmupUsage struct {
	Reserved   int `json:"reserved"`
	Successful int `json:"successful"`
	Bounced    int `json:"bounced"`
}

type warmupOverride struct {
	DailyCap  int        `json:"daily_cap"`
	ExpiresAt time.Time  `json:"expires_at"`
	Reason    string     `json:"reason"`
	SetBy     *string    `json:"set_by"`
	SetAt     *time.Time `json:"set_at"`
	Active    bool       `json:"active"`
}

type warmupWarnings struct {
	VerifiedWithin14Days bool    `json:"verified_within_14_days"`
	HasSendingHistory    bool    `json:"has_sending_history"`
	RecentBounceRate     float64 `json:"recent_bounce_rate"`
}

type warmupDetails struct {
	Profile               string                               `json:"profile"`
	SuccessfulDays        int                                  `json:"successful_days"`
	CurveCap              int                                  `json:"curve_cap"`
	Override              *warmupOverride                      `json:"override"`
	EffectiveCapToday     int                                  `json:"effective_cap_today"`
	UsageToday            warmupUsage                          `json:"usage_today"`
	Warnings              warmupWarnings                       `json:"warnings,omitempty"`
	Graduated             bool                                 `json:"graduated"`
	GraduatedAt           *time.Time                           `json:"graduated_at,omitempty"`
	GraduatedBy           *string                              `json:"graduated_by,omitempty"`
	GraduatedReason       string                               `json:"graduated_reason,omitempty"`
	GraduationEligibility delivery.WarmupGraduationEligibility `json:"graduation_eligibility"`
}

type customerWarmupSummary struct {
	Profile           string `json:"profile"`
	SuccessfulDays    int    `json:"successful_days"`
	CurveCap          int    `json:"curve_cap"`
	EffectiveCapToday int    `json:"effective_cap_today"`
	OverrideActive    bool   `json:"override_active"`
	WarmupComplete    bool   `json:"warmup_complete"`
}

func (s *Server) loadWarmupDetails(ctx context.Context, domainID, organizationID string) (warmupDetails, error) {
	var organization, domain, profile string
	var overrideValue pgtype.Int4
	var overrideExpiresAt, overrideSetAt pgtype.Timestamptz
	var overrideReason string
	var overrideSetBy pgtype.UUID
	var graduatedAt pgtype.Timestamptz
	var graduatedByID pgtype.UUID
	var graduatedReason string
	var dailyDefault, dailyOverride pgtype.Int8
	var unlimited bool
	var verifiedAt pgtype.Timestamptz
	var now time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT sd.organization_id::text, sd.domain, sd.warmup_profile,
		       sd.warmup_daily_cap_override, sd.warmup_override_expires_at,
		       sd.warmup_override_reason, sd.warmup_override_set_by,
		       sd.warmup_override_set_at, sd.warmup_graduated_at,
		       sd.warmup_graduated_by, sd.warmup_graduated_reason,
		       pd.daily_limit, dq.daily_limit,
		       COALESCE(dq.unlimited, false), sd.verified_at, now()
		FROM sending_domains sd
		LEFT JOIN platform_quota_defaults pd ON pd.stream = 'marketing'
		LEFT JOIN domain_quotas dq ON dq.domain_id = sd.id AND dq.stream = 'marketing'
		WHERE sd.id = $1 AND ($2 = '' OR sd.organization_id::text = $2)
	`, domainID, organizationID).Scan(
		&organization, &domain, &profile, &overrideValue, &overrideExpiresAt,
		&overrideReason, &overrideSetBy, &overrideSetAt,
		&graduatedAt, &graduatedByID, &graduatedReason,
		&dailyDefault, &dailyOverride, &unlimited, &verifiedAt, &now,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return warmupDetails{}, pgx.ErrNoRows
	}
	if err != nil {
		return warmupDetails{}, fmt.Errorf("load sending-domain warm-up settings: %w", err)
	}
	if profile == "" {
		profile = "standard"
	}

	qualifiedDayFilter := ""
	if profile == "accelerated" {
		qualifiedDayFilter = " AND bounce_count::numeric / NULLIF(successful_count, 0) < 0.02"
	}
	sendingDay := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	var details warmupDetails
	details.Profile = profile
	var graduationDays int
	var successfulSends int64
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE sending_day < $3 AND successful_count > 0`+qualifiedDayFilter+`),
		       COALESCE(max(reserved_count) FILTER (WHERE sending_day = $3), 0),
		       COALESCE(max(successful_count) FILTER (WHERE sending_day = $3), 0),
		       COALESCE(max(bounce_count) FILTER (WHERE sending_day = $3), 0),
		       COALESCE(bool_or(reserved_count > 0 OR successful_count > 0 OR bounce_count > 0), false)
		FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND lower(domain) = lower($2)
	`, organization, domain, sendingDay).Scan(
		&details.SuccessfulDays, &details.UsageToday.Reserved,
		&details.UsageToday.Successful, &details.UsageToday.Bounced,
		&details.Warnings.HasSendingHistory,
	); err != nil {
		return warmupDetails{}, fmt.Errorf("load sending-domain warm-up usage: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE sending_day < $3 AND successful_count > 0),
		       COALESCE(sum(successful_count), 0)
		FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND lower(domain) = lower($2)
	`, organization, domain, sendingDay).Scan(&graduationDays, &successfulSends); err != nil {
		return warmupDetails{}, fmt.Errorf("load sending-domain graduation history: %w", err)
	}

	var recentBounced, recentSuccessful int64
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(bounce_count), 0), COALESCE(sum(successful_count), 0)
		FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND lower(domain) = lower($2)
		  AND sending_day >= ($3::date - 6) AND sending_day <= $3::date
	`, organization, domain, sendingDay).Scan(&recentBounced, &recentSuccessful); err != nil {
		return warmupDetails{}, fmt.Errorf("load recent sending-domain bounce rate: %w", err)
	}
	totalRecent := recentBounced + recentSuccessful
	if totalRecent > 0 {
		details.Warnings.RecentBounceRate = float64(recentBounced) / float64(totalRecent)
	}
	details.GraduationEligibility = delivery.CheckWarmupGraduationEligibility(
		graduationDays, successfulSends, recentBounced, recentSuccessful,
	)
	details.Warnings.VerifiedWithin14Days = verifiedAt.Valid &&
		verifiedAt.Time.After(now.Add(-14*24*time.Hour))

	if graduatedAt.Valid {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return warmupDetails{}, fmt.Errorf("begin warm-up graduation health check: %w", err)
		}
		revoked, reputation, err := domainwarmup.RevokeIfUnsafe(ctx, tx, domainID)
		if err != nil {
			_ = tx.Rollback(ctx)
			return warmupDetails{}, fmt.Errorf("check warm-up graduation reputation: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return warmupDetails{}, fmt.Errorf("commit warm-up graduation health check: %w", err)
		}
		if revoked {
			graduatedAt.Valid = false
			graduatedByID.Valid = false
			s.logger.Warn("warm-up graduation automatically revoked",
				"domain_id", domainID,
				"bounce_rate_7_days", reputation.BounceRate,
				"complaint_rate_7_days", reputation.ComplaintRate,
			)
		}
	}

	curveCap := delivery.WarmupDailyCap(
		profile, s.cfg.WarmupStartPerDay, s.cfg.WarmupGrowthFactor,
		s.cfg.MaxDailyEmails, details.SuccessfulDays,
	)
	var override *int
	var expiry *time.Time
	if overrideValue.Valid {
		value := int(overrideValue.Int32)
		override = &value
	}
	if overrideExpiresAt.Valid {
		value := overrideExpiresAt.Time
		expiry = &value
	}
	var effectiveDailyQuota *int64
	if !unlimited {
		daily := dailyOverride
		if !daily.Valid {
			daily = dailyDefault
		}
		if daily.Valid {
			effectiveDailyQuota = &daily.Int64
		}
	}
	if graduatedAt.Valid {
		curveCap = delivery.GraduatedWarmupDailyCap(effectiveDailyQuota, unlimited)
	}
	details.CurveCap = curveCap
	details.EffectiveCapToday = delivery.EffectiveWarmupDailyCap(
		curveCap, override, expiry, now, effectiveDailyQuota, unlimited,
	)
	if graduatedAt.Valid {
		details.Graduated = true
		value := graduatedAt.Time
		details.GraduatedAt = &value
		if graduatedByID.Valid {
			value, err := uuid.FromBytes(graduatedByID.Bytes[:])
			if err != nil {
				return warmupDetails{}, fmt.Errorf("decode warm-up graduation actor: %w", err)
			}
			text := value.String()
			details.GraduatedBy = &text
		}
		details.GraduatedReason = graduatedReason
	}
	if override != nil && expiry != nil {
		var setBy *string
		if overrideSetBy.Valid {
			value, err := uuid.FromBytes(overrideSetBy.Bytes[:])
			if err != nil {
				return warmupDetails{}, fmt.Errorf("decode warm-up override actor: %w", err)
			}
			text := value.String()
			setBy = &text
		}
		var setAt *time.Time
		if overrideSetAt.Valid {
			value := overrideSetAt.Time
			setAt = &value
		}
		details.Override = &warmupOverride{
			DailyCap: *override, ExpiresAt: *expiry, Reason: overrideReason,
			SetBy: setBy, SetAt: setAt, Active: expiry.After(now),
		}
	}
	return details, nil
}

func (s *Server) getAdminDomainWarmup(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	details, err := s.loadWarmupDetails(r.Context(), domainID, "")
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load admin domain warm-up state", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_load_failed", "Domain warm-up data could not be loaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": details})
}

type warmupOverrideInput struct {
	DailyCap  int       `json:"daily_cap"`
	ExpiresAt time.Time `json:"expires_at"`
	UpdatedBy string    `json:"updated_by"`
	Reason    string    `json:"reason"`
}

type warmupOverrideClearInput struct {
	UpdatedBy string `json:"updated_by"`
	Reason    string `json:"reason"`
}

type warmupGraduationInput struct {
	UpdatedBy         string `json:"updated_by"`
	Reason            string `json:"reason"`
	ConfirmIneligible bool   `json:"confirm_ineligible"`
}

type warmupGraduationClearInput struct {
	UpdatedBy string `json:"updated_by"`
	Reason    string `json:"reason"`
}

func (s *Server) setAdminDomainWarmupOverride(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input warmupOverrideInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide a valid warm-up override.")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(input.UpdatedBy); err != nil || input.Reason == "" || len(input.Reason) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_override", "An admin user ID and a reason of 1 to 500 characters are required.")
		return
	}
	now := time.Now().UTC()
	if err := delivery.ValidateWarmupOverride(input.DailyCap, input.ExpiresAt, now); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_override", err.Error()+".")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin domain warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be updated.")
		return
	}
	defer tx.Rollback(r.Context())
	var databaseNow time.Time
	if err := tx.QueryRow(r.Context(), `SELECT now()`).Scan(&databaseNow); err != nil {
		s.logger.Error("read warm-up override validation time", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be updated.")
		return
	}
	if err := delivery.ValidateWarmupOverride(input.DailyCap, input.ExpiresAt, databaseNow); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_override", err.Error()+".")
		return
	}
	tag, err := tx.Exec(r.Context(), `
		UPDATE sending_domains
		SET warmup_daily_cap_override = $2,
		    warmup_override_expires_at = $3,
		    warmup_override_reason = $4,
		    warmup_override_set_by = $5,
		    warmup_override_set_at = now(),
		    updated_at = now()
		WHERE id = $1
	`, domainID, input.DailyCap, input.ExpiresAt.UTC(), input.Reason, input.UpdatedBy)
	if err != nil {
		s.logger.Error("write domain warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be updated.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err := makeWarmupDeferredJobsDue(r.Context(), tx, domainID); err != nil {
		s.logger.Error("wake deferred jobs after warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be updated.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit domain warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be updated.")
		return
	}
	details, err := s.loadWarmupDetails(r.Context(), domainID, "")
	if err != nil {
		s.logger.Error("reload domain warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The override was saved but its effective state could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": details})
}

func (s *Server) clearAdminDomainWarmupOverride(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input warmupOverrideClearInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide a valid warm-up override clear request.")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(input.UpdatedBy); err != nil || input.Reason == "" || len(input.Reason) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_override", "An admin user ID and a reason of 1 to 500 characters are required.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin domain warm-up override clear", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be cleared.")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `
		UPDATE sending_domains
		SET warmup_daily_cap_override = NULL,
		    warmup_override_expires_at = NULL,
		    warmup_override_reason = '',
		    warmup_override_set_by = NULL,
		    warmup_override_set_at = NULL,
		    updated_at = now()
		WHERE id = $1
	`, domainID)
	if err != nil {
		s.logger.Error("clear domain warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be cleared.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err := makeWarmupDeferredJobsDue(r.Context(), tx, domainID); err != nil {
		s.logger.Error("wake deferred jobs after clearing warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be cleared.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit domain warm-up override clear", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The domain warm-up override could not be cleared.")
		return
	}
	details, err := s.loadWarmupDetails(r.Context(), domainID, "")
	if err != nil {
		s.logger.Error("reload cleared domain warm-up override", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_update_failed", "The override was cleared but its effective state could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": details})
}

func makeWarmupDeferredJobsDue(ctx context.Context, tx pgx.Tx, domainID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE delivery_jobs dj
		SET next_attempt_at = now(),
		    failure_started_at = NULL,
		    processing_started_at = NULL,
		    nats_publish_claimed_at = NULL,
		    nats_published_at = NULL,
		    updated_at = now()
		FROM campaigns c, sending_domains sd
		WHERE sd.id = $1
		  AND c.id = dj.campaign_id
		  AND c.organization_id = dj.organization_id
		  AND sd.organization_id = dj.organization_id
		  AND lower(split_part(c.sender_email, '@', 2)) = lower(sd.domain)
		  AND dj.status = 'deferred'
	`, domainID); err != nil {
		return fmt.Errorf("make domain deferred jobs due: %w", err)
	}
	return nil
}

func (s *Server) graduateAdminDomainWarmup(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input warmupGraduationInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_graduation", "Provide a valid warm-up graduation request.")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(input.UpdatedBy); err != nil || input.Reason == "" || len(input.Reason) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_graduation", "An admin user ID and a reason of 1 to 500 characters are required.")
		return
	}
	details, err := s.loadWarmupDetails(r.Context(), domainID, "")
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load warm-up graduation eligibility", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain could not be graduated.")
		return
	}
	if details.Graduated {
		httpx.Error(w, http.StatusConflict, "warmup_already_graduated", "The sending domain is already graduated.")
		return
	}
	if !details.GraduationEligibility.Eligible && !input.ConfirmIneligible {
		httpx.Error(w, http.StatusConflict, "warmup_graduation_ineligible", "Confirm the ineligible graduation explicitly and provide a reason.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin warm-up graduation", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain could not be graduated.")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `
		UPDATE sending_domains
		SET warmup_graduated_at = now(),
		    warmup_graduated_by = $2,
		    warmup_graduated_reason = $3,
		    updated_at = now()
		WHERE id = $1 AND warmup_graduated_at IS NULL
	`, domainID, input.UpdatedBy, input.Reason)
	if err != nil {
		s.logger.Error("write warm-up graduation", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain could not be graduated.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "warmup_already_graduated", "The sending domain is already graduated.")
		return
	}
	if err := makeWarmupDeferredJobsDue(r.Context(), tx, domainID); err != nil {
		s.logger.Error("wake deferred jobs after warm-up graduation", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain could not be graduated.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit warm-up graduation", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain could not be graduated.")
		return
	}
	details, err = s.loadWarmupDetails(r.Context(), domainID, "")
	if err != nil {
		s.logger.Error("reload graduated warm-up state", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain graduated but its state could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": details})
}

func (s *Server) revokeAdminDomainWarmupGraduation(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("id")
	if _, err := uuid.Parse(domainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input warmupGraduationClearInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_graduation", "Provide a valid warm-up graduation revoke request.")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if _, err := uuid.Parse(input.UpdatedBy); err != nil || input.Reason == "" || len(input.Reason) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_graduation", "An admin user ID and a reason of 1 to 500 characters are required.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin warm-up graduation revoke", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain graduation could not be revoked.")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `
		UPDATE sending_domains
		SET warmup_graduated_at = NULL,
		    warmup_graduated_by = NULL,
		    warmup_graduated_reason = '',
		    updated_at = now()
		WHERE id = $1 AND warmup_graduated_at IS NOT NULL
	`, domainID)
	if err != nil {
		s.logger.Error("revoke warm-up graduation", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain graduation could not be revoked.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusConflict, "warmup_not_graduated", "The sending domain is not graduated.")
		return
	}
	if err := makeWarmupDeferredJobsDue(r.Context(), tx, domainID); err != nil {
		s.logger.Error("wake deferred jobs after warm-up graduation revoke", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain graduation could not be revoked.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit warm-up graduation revoke", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain graduation could not be revoked.")
		return
	}
	details, err := s.loadWarmupDetails(r.Context(), domainID, "")
	if err != nil {
		s.logger.Error("reload revoked warm-up state", "error", err, "domain_id", domainID)
		httpx.Error(w, http.StatusInternalServerError, "warmup_graduation_failed", "The domain graduation was revoked but its state could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": details})
}

func (s *Server) loadCustomerWarmupSummary(ctx context.Context, domainID, organizationID string) (customerWarmupSummary, error) {
	details, err := s.loadWarmupDetails(ctx, domainID, organizationID)
	if err != nil {
		return customerWarmupSummary{}, err
	}
	return customerWarmupSummary{
		Profile: details.Profile, SuccessfulDays: details.SuccessfulDays,
		CurveCap: details.CurveCap, EffectiveCapToday: details.EffectiveCapToday,
		OverrideActive: details.Override != nil && details.Override.Active,
		WarmupComplete: details.Graduated,
	}, nil
}
