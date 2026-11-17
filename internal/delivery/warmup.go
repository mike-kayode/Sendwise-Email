package delivery

import (
	"errors"
	"math"
	"time"
)

const MaxWarmupOverrideDuration = 14 * 24 * time.Hour

// acceleratedWarmupDailyCaps is the fixed daily ramp for the "accelerated"
// warm-up profile: it advances through fixed stages (200 through 100,000
// messages per qualified day) instead of the geometric standard curve.
var acceleratedWarmupDailyCaps = [...]int{200, 500, 1000, 2500, 5000, 10000, 20000, 40000, 100000}

func WarmupDailyCap(profile string, start int, growth float64, maximum, successfulDays int) int {
	if profile == "accelerated" {
		return AcceleratedWarmupDailyCap(successfulDays)
	}
	if start < 1 {
		start = 50
	}
	if growth < 1 {
		growth = 1.5
	}
	if maximum < 1 {
		maximum = 10000
	}
	cap := float64(start) * math.Pow(growth, float64(successfulDays))
	if math.IsInf(cap, 1) || cap >= float64(maximum) {
		return maximum
	}
	return int(cap)
}

func AcceleratedWarmupDailyCap(qualifiedDays int) int {
	if qualifiedDays < 0 {
		qualifiedDays = 0
	}
	if qualifiedDays >= len(acceleratedWarmupDailyCaps) {
		return acceleratedWarmupDailyCaps[len(acceleratedWarmupDailyCaps)-1]
	}
	return acceleratedWarmupDailyCaps[qualifiedDays]
}

func ValidateWarmupOverride(dailyCap int, expiresAt, now time.Time) error {
	if dailyCap < 1 || int64(dailyCap) > 1<<31-1 {
		return errors.New("warm-up daily cap must be a positive 32-bit integer")
	}
	if expiresAt.IsZero() || !expiresAt.After(now) {
		return errors.New("warm-up override expiry must be in the future")
	}
	if expiresAt.After(now.Add(MaxWarmupOverrideDuration)) {
		return errors.New("warm-up override cannot exceed 14 days")
	}
	return nil
}

func EffectiveWarmupDailyCap(
	curveCap int,
	override *int,
	expiresAt *time.Time,
	now time.Time,
	domainDailyQuota *int64,
	unlimited bool,
) int {
	if override == nil || expiresAt == nil || !expiresAt.After(now) {
		return curveCap
	}
	if !unlimited && domainDailyQuota != nil && int64(*override) > *domainDailyQuota {
		return int(*domainDailyQuota)
	}
	return *override
}
