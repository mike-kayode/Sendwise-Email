package quota

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StreamMarketing     = "marketing"
	StreamTransactional = "transactional"

	SourceCampaignDelivery       = "campaign_delivery"
	SourceTransactionalRecipient = "transactional_recipient"
)

var (
	ErrDomainNotFound      = errors.New("sending domain not found")
	ErrReservationReleased = errors.New("domain quota reservation was already released")
	ErrInvalidReservation  = errors.New("domain quota reservation is invalid")
)

type Window string

const (
	WindowPerSecond Window = "per_second"
	WindowDaily     Window = "daily"
	WindowMonthly   Window = "monthly"
)

type ExceededError struct {
	Window  Window
	Limit   int64
	Used    int64
	ResetAt time.Time
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("%s sending quota of %d exceeded", e.Window, e.Limit)
}

type Defaults struct {
	Stream         string
	DailyLimit     *int64    `json:"daily_limit"`
	MonthlyLimit   *int64    `json:"monthly_limit"`
	PerSecondLimit int64     `json:"per_second_limit"`
	UpdatedBy      *string   `json:"updated_by,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Snapshot struct {
	Stream              string    `json:"stream"`
	DailyLimit          *int64    `json:"daily_limit"`
	MonthlyLimit        *int64    `json:"monthly_limit"`
	PerSecondLimit      int64     `json:"per_second_limit"`
	DailyOverride       *int64    `json:"daily_override"`
	MonthlyOverride     *int64    `json:"monthly_override"`
	PerSecondOverride   *int64    `json:"per_second_override"`
	Unlimited           bool      `json:"unlimited"`
	Notes               string    `json:"notes,omitempty"`
	DailyUsed           int64     `json:"daily_used"`
	MonthlyUsed         int64     `json:"monthly_used"`
	PerSecondUsed       int64     `json:"per_second_used"`
	DailyResetAt        time.Time `json:"daily_reset_at"`
	MonthlyResetAt      time.Time `json:"monthly_reset_at"`
	PerSecondResetAt    time.Time `json:"per_second_reset_at"`
	DailyUsagePercent   float64   `json:"daily_usage_percent"`
	MonthlyUsagePercent float64   `json:"monthly_usage_percent"`
}

type Reservation struct {
	DomainID string
	Stream   string
	Source   string
	SourceID string
}

type Scanner interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func SeedDefaults(
	ctx context.Context,
	tx pgx.Tx,
	marketingDaily, marketingRate int64,
	transactionalDaily, transactionalMonthly, transactionalRate, hardRate int64,
) error {
	if marketingDaily < 1 || marketingRate < 1 || transactionalDaily < 1 ||
		transactionalMonthly < 1 || transactionalRate < 1 || hardRate < 1 {
		return errors.New("quota seed limits must be positive")
	}
	if marketingRate > hardRate {
		marketingRate = hardRate
	}
	if transactionalRate > hardRate {
		transactionalRate = hardRate
	}

	for _, defaults := range []Defaults{
		{Stream: StreamMarketing, DailyLimit: int64Pointer(marketingDaily), PerSecondLimit: marketingRate},
		{Stream: StreamTransactional, DailyLimit: int64Pointer(transactionalDaily), MonthlyLimit: int64Pointer(transactionalMonthly), PerSecondLimit: transactionalRate},
	} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO platform_quota_defaults (stream, daily_limit, monthly_limit, per_second_limit)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (stream) DO NOTHING
		`, defaults.Stream, defaults.DailyLimit, defaults.MonthlyLimit, defaults.PerSecondLimit); err != nil {
			return fmt.Errorf("insert %s quota defaults: %w", defaults.Stream, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE platform_quota_defaults
			SET daily_limit = $2, monthly_limit = $3, per_second_limit = $4,
			    seeded_from_env = true, updated_at = now()
			WHERE stream = $1 AND NOT seeded_from_env
		`, defaults.Stream, defaults.DailyLimit, defaults.MonthlyLimit, defaults.PerSecondLimit); err != nil {
			return fmt.Errorf("seed %s quota defaults from environment: %w", defaults.Stream, err)
		}
	}
	return nil
}

func SeedPoolDefaults(
	ctx context.Context,
	pool *pgxpool.Pool,
	marketingDaily, marketingRate int64,
	transactionalDaily, transactionalMonthly, transactionalRate, hardRate int64,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin quota defaults seed: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := SeedDefaults(
		ctx, tx, marketingDaily, marketingRate,
		transactionalDaily, transactionalMonthly, transactionalRate, hardRate,
	); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit quota defaults seed: %w", err)
	}
	return nil
}

func GetSnapshot(ctx context.Context, queryer Scanner, domainID, stream string, hardRate int64) (Snapshot, error) {
	var snapshot Snapshot
	var dailyDefault, monthlyDefault, dailyOverride, monthlyOverride, rateOverride pgtype.Int8
	var perSecondDefault int64
	err := queryer.QueryRow(ctx, `
		SELECT pd.daily_limit, pd.monthly_limit, pd.per_second_limit,
		       dq.daily_limit, dq.monthly_limit, dq.per_second_limit,
		       COALESCE(dq.unlimited, false), COALESCE(dq.notes, '')
		FROM sending_domains sd
		JOIN platform_quota_defaults pd ON pd.stream = $2
		LEFT JOIN domain_quotas dq ON dq.domain_id = sd.id AND dq.stream = pd.stream
		WHERE sd.id = $1
	`, domainID, stream).Scan(
		&dailyDefault, &monthlyDefault, &perSecondDefault,
		&dailyOverride, &monthlyOverride, &rateOverride,
		&snapshot.Unlimited, &snapshot.Notes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrDomainNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load effective %s quotas: %w", stream, err)
	}

	snapshot.Stream = stream
	snapshot.DailyOverride = pgtypeInt8Pointer(dailyOverride)
	snapshot.MonthlyOverride = pgtypeInt8Pointer(monthlyOverride)
	snapshot.PerSecondOverride = pgtypeInt8Pointer(rateOverride)
	snapshot.DailyLimit = snapshot.DailyOverride
	if snapshot.DailyLimit == nil {
		snapshot.DailyLimit = pgtypeInt8Pointer(dailyDefault)
	}
	snapshot.MonthlyLimit = snapshot.MonthlyOverride
	if snapshot.MonthlyLimit == nil {
		snapshot.MonthlyLimit = pgtypeInt8Pointer(monthlyDefault)
	}
	snapshot.PerSecondLimit = perSecondDefault
	if snapshot.PerSecondOverride != nil {
		snapshot.PerSecondLimit = *snapshot.PerSecondOverride
	}
	if hardRate > 0 && snapshot.PerSecondLimit > hardRate {
		snapshot.PerSecondLimit = hardRate
	}

	var now time.Time
	if err := queryer.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Snapshot{}, fmt.Errorf("read quota snapshot time: %w", err)
	}
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	second := now.Truncate(time.Second)
	snapshot.DailyResetAt = day.AddDate(0, 0, 1)
	snapshot.MonthlyResetAt = month.AddDate(0, 1, 0)
	snapshot.PerSecondResetAt = second.Add(time.Second)
	if err := queryer.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT sent_count FROM domain_usage
			WHERE domain_id = $1 AND stream = $2 AND period = 'day' AND period_start = $3
		), 0), COALESCE((
			SELECT sent_count FROM domain_usage
			WHERE domain_id = $1 AND stream = $2 AND period = 'month' AND period_start = $4
		), 0)
	`, domainID, stream, day, month).Scan(&snapshot.DailyUsed, &snapshot.MonthlyUsed); err != nil {
		return Snapshot{}, fmt.Errorf("load %s quota usage: %w", stream, err)
	}
	if err := queryer.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT sent_count FROM domain_rate_usage
			WHERE domain_id = $1 AND stream = $2 AND second_start = $3
		), 0)
	`, domainID, stream, second).Scan(&snapshot.PerSecondUsed); err != nil {
		return Snapshot{}, fmt.Errorf("load %s rate usage: %w", stream, err)
	}
	if snapshot.DailyLimit != nil && *snapshot.DailyLimit > 0 {
		snapshot.DailyUsagePercent = math.Min(100, 100*float64(snapshot.DailyUsed)/float64(*snapshot.DailyLimit))
	}
	if snapshot.MonthlyLimit != nil && *snapshot.MonthlyLimit > 0 {
		snapshot.MonthlyUsagePercent = math.Min(100, 100*float64(snapshot.MonthlyUsed)/float64(*snapshot.MonthlyLimit))
	}
	return snapshot, nil
}

func Reserve(ctx context.Context, tx pgx.Tx, domainID, stream, source, sourceID string, count, hardRate int64) (reservation Reservation, err error) {
	if count < 1 || hardRate < 1 || (stream != StreamMarketing && stream != StreamTransactional) ||
		(source != SourceCampaignDelivery && source != SourceTransactionalRecipient) {
		return Reservation{}, ErrInvalidReservation
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT domain_quota_reserve`); err != nil {
		return Reservation{}, fmt.Errorf("savepoint domain quota reservation: %w", err)
	}
	defer func() {
		if err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT domain_quota_reserve`)
		}
		_, releaseErr := tx.Exec(ctx, `RELEASE SAVEPOINT domain_quota_reserve`)
		if err == nil && releaseErr != nil {
			err = fmt.Errorf("release domain quota reservation savepoint: %w", releaseErr)
		}
	}()

	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, domainID+":"+stream); err != nil {
		return Reservation{}, fmt.Errorf("lock domain quota: %w", err)
	}
	var existingStatus, existingDomainID string
	var existingCount int64
	err = tx.QueryRow(ctx, `
		SELECT status, domain_id::text, reserved_count FROM domain_quota_reservations
		WHERE stream = $1 AND source = $2 AND source_id = $3
		FOR UPDATE
	`, stream, source, sourceID).Scan(&existingStatus, &existingDomainID, &existingCount)
	wasReleased := false
	if err == nil {
		if existingDomainID != domainID || existingCount != count {
			return Reservation{}, ErrInvalidReservation
		}
		if existingStatus != "released" {
			return Reservation{DomainID: domainID, Stream: stream, Source: source, SourceID: sourceID}, nil
		}
		wasReleased = true
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, fmt.Errorf("check existing domain quota reservation: %w", err)
	}

	var dailyDefault, monthlyDefault, dailyOverride, monthlyOverride, rateOverride pgtype.Int8
	var perSecondDefault int64
	var unlimited bool
	err = tx.QueryRow(ctx, `
		SELECT pd.daily_limit, pd.monthly_limit, pd.per_second_limit,
		       dq.daily_limit, dq.monthly_limit, dq.per_second_limit,
		       COALESCE(dq.unlimited, false)
		FROM sending_domains sd
		JOIN platform_quota_defaults pd ON pd.stream = $2
		LEFT JOIN domain_quotas dq ON dq.domain_id = sd.id AND dq.stream = pd.stream
		WHERE sd.id = $1
		FOR UPDATE OF sd
	`, domainID, stream).Scan(
		&dailyDefault, &monthlyDefault, &perSecondDefault,
		&dailyOverride, &monthlyOverride, &rateOverride, &unlimited,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrDomainNotFound
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("load %s limits for reservation: %w", stream, err)
	}
	dailyLimit := pgtypeInt8Pointer(dailyOverride)
	if dailyLimit == nil {
		dailyLimit = pgtypeInt8Pointer(dailyDefault)
	}
	monthlyLimit := pgtypeInt8Pointer(monthlyOverride)
	if monthlyLimit == nil {
		monthlyLimit = pgtypeInt8Pointer(monthlyDefault)
	}
	perSecondLimit := perSecondDefault
	if rateOverride.Valid {
		perSecondLimit = rateOverride.Int64
	}
	if perSecondLimit > hardRate {
		perSecondLimit = hardRate
	}
	if unlimited {
		dailyLimit = nil
		monthlyLimit = nil
	}

	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return Reservation{}, fmt.Errorf("read quota reservation time: %w", err)
	}
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	second := now.Truncate(time.Second)
	if err := incrementUsage(ctx, tx, domainID, stream, "day", day, count, dailyLimit); err != nil {
		if isLimitError(err) {
			return Reservation{}, &ExceededError{Window: WindowDaily, Limit: limitValue(dailyLimit), Used: usedFromError(err), ResetAt: day.AddDate(0, 0, 1)}
		}
		return Reservation{}, err
	}
	if err := incrementUsage(ctx, tx, domainID, stream, "month", month, count, monthlyLimit); err != nil {
		if isLimitError(err) {
			return Reservation{}, &ExceededError{Window: WindowMonthly, Limit: limitValue(monthlyLimit), Used: usedFromError(err), ResetAt: month.AddDate(0, 1, 0)}
		}
		return Reservation{}, err
	}
	if err := incrementRate(ctx, tx, domainID, stream, second, count, perSecondLimit); err != nil {
		if isLimitError(err) {
			return Reservation{}, &ExceededError{Window: WindowPerSecond, Limit: perSecondLimit, Used: usedFromError(err), ResetAt: second.Add(time.Second)}
		}
		return Reservation{}, err
	}
	if wasReleased {
		if _, err := tx.Exec(ctx, `
			UPDATE domain_quota_reservations
			SET daily_period = $4, monthly_period = $5, second_period = $6,
			    status = 'reserved', updated_at = now()
			WHERE stream = $1 AND source = $2 AND source_id = $3 AND status = 'released'
		`, stream, source, sourceID, day, month, second); err != nil {
			return Reservation{}, fmt.Errorf("reactivate domain quota reservation: %w", err)
		}
	} else if _, err := tx.Exec(ctx, `
		INSERT INTO domain_quota_reservations
			(stream, source, source_id, domain_id, daily_period, monthly_period, second_period, reserved_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, stream, source, sourceID, domainID, day, month, second, count); err != nil {
		return Reservation{}, fmt.Errorf("record domain quota reservation: %w", err)
	}
	return Reservation{DomainID: domainID, Stream: stream, Source: source, SourceID: sourceID}, nil
}

func Retain(ctx context.Context, tx pgx.Tx, stream, source, sourceID string) error {
	_, err := tx.Exec(ctx, `
		UPDATE domain_quota_reservations
		SET status = 'retained', updated_at = now()
		WHERE stream = $1 AND source = $2 AND source_id = $3 AND status = 'reserved'
	`, stream, source, sourceID)
	if err != nil {
		return fmt.Errorf("retain domain quota reservation: %w", err)
	}
	return nil
}

func Release(ctx context.Context, tx pgx.Tx, stream, source, sourceID string) error {
	var reservation Reservation
	var day, month time.Time
	var second time.Time
	var count int64
	err := tx.QueryRow(ctx, `
		SELECT domain_id::text, stream, source, source_id::text,
		       daily_period, monthly_period, second_period, reserved_count
		FROM domain_quota_reservations
		WHERE stream = $1 AND source = $2 AND source_id = $3 AND status = 'reserved'
		FOR UPDATE
	`, stream, source, sourceID).Scan(
		&reservation.DomainID, &reservation.Stream, &reservation.Source,
		&reservation.SourceID, &day, &month, &second, &count,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load domain quota reservation for release: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domain_usage SET sent_count = GREATEST(sent_count - $4, 0), updated_at = now()
		WHERE domain_id = $1 AND stream = $2 AND period = 'day' AND period_start = $3
	`, reservation.DomainID, stream, day, count); err != nil {
		return fmt.Errorf("release daily domain quota usage: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domain_usage SET sent_count = GREATEST(sent_count - $4, 0), updated_at = now()
		WHERE domain_id = $1 AND stream = $2 AND period = 'month' AND period_start = $3
	`, reservation.DomainID, stream, month, count); err != nil {
		return fmt.Errorf("release monthly domain quota usage: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domain_rate_usage SET sent_count = GREATEST(sent_count - $4, 0)
		WHERE domain_id = $1 AND stream = $2 AND second_start = $3
	`, reservation.DomainID, stream, second, count); err != nil {
		return fmt.Errorf("release per-second domain quota usage: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE domain_quota_reservations
		SET status = 'released', updated_at = now()
		WHERE stream = $1 AND source = $2 AND source_id = $3 AND status = 'reserved'
	`, stream, source, sourceID); err != nil {
		return fmt.Errorf("mark domain quota reservation released: %w", err)
	}
	return nil
}

func incrementUsage(ctx context.Context, tx pgx.Tx, domainID, stream, period string, start time.Time, count int64, limit *int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO domain_usage (domain_id, stream, period, period_start, sent_count)
		VALUES ($1, $2, $3, $4, 0)
		ON CONFLICT (domain_id, stream, period, period_start) DO NOTHING
	`, domainID, stream, period, start); err != nil {
		return fmt.Errorf("create %s usage bucket: %w", period, err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE domain_usage
		SET sent_count = sent_count + $5, updated_at = now()
		WHERE domain_id = $1 AND stream = $2 AND period = $3 AND period_start = $4
		  AND ($6::bigint IS NULL OR sent_count + $5 <= $6)
	`, domainID, stream, period, start, count, limit)
	if err != nil {
		return fmt.Errorf("increment %s usage: %w", period, err)
	}
	if tag.RowsAffected() == 0 {
		var used int64
		if err := tx.QueryRow(ctx, `
			SELECT sent_count FROM domain_usage
			WHERE domain_id = $1 AND stream = $2 AND period = $3 AND period_start = $4
		`, domainID, stream, period, start).Scan(&used); err != nil {
			return fmt.Errorf("read current %s usage: %w", period, err)
		}
		return usageLimitError{used: used}
	}
	return nil
}

func incrementRate(ctx context.Context, tx pgx.Tx, domainID, stream string, second time.Time, count, limit int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO domain_rate_usage (domain_id, stream, second_start, sent_count)
		VALUES ($1, $2, $3, 0)
		ON CONFLICT (domain_id, stream, second_start) DO NOTHING
	`, domainID, stream, second); err != nil {
		return fmt.Errorf("create per-second rate bucket: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE domain_rate_usage
		SET sent_count = sent_count + $4
		WHERE domain_id = $1 AND stream = $2 AND second_start = $3
		  AND sent_count + $4 <= $5
	`, domainID, stream, second, count, limit)
	if err != nil {
		return fmt.Errorf("increment per-second rate usage: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var used int64
		if err := tx.QueryRow(ctx, `
			SELECT sent_count FROM domain_rate_usage
			WHERE domain_id = $1 AND stream = $2 AND second_start = $3
		`, domainID, stream, second).Scan(&used); err != nil {
			return fmt.Errorf("read current per-second rate usage: %w", err)
		}
		return usageLimitError{used: used}
	}
	return nil
}

type usageLimitError struct{ used int64 }

func (e usageLimitError) Error() string { return "quota limit exceeded" }

func usedFromError(err error) int64 {
	var limitError usageLimitError
	if errors.As(err, &limitError) {
		return limitError.used
	}
	return 0
}

func isLimitError(err error) bool {
	var limitError usageLimitError
	return errors.As(err, &limitError)
}

func limitValue(limit *int64) int64 {
	if limit == nil {
		return 0
	}
	return *limit
}

func pgtypeInt8Pointer(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	return int64Pointer(value.Int64)
}

func int64Pointer(value int64) *int64 { return &value }
