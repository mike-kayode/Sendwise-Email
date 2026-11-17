package delivery

import (
	"testing"
	"time"
)

func TestAcceleratedWarmupDailyCapRequiresQualifiedDayStages(t *testing.T) {
	want := []int{200, 500, 1000, 2500, 5000, 10000, 20000, 40000, 100000, 100000}
	for day, expected := range want {
		if got := AcceleratedWarmupDailyCap(day); got != expected {
			t.Errorf("AcceleratedWarmupDailyCap(%d) = %d, want %d", day, got, expected)
		}
	}
}

func TestWarmupDailyCapDispatchesOnProfile(t *testing.T) {
	if got := WarmupDailyCap("accelerated", 50, 1.5, 10000, 2); got != 1000 {
		t.Fatalf("accelerated warm-up cap = %d, want 1000", got)
	}
	if got := WarmupDailyCap("standard", 50, 1.5, 10000, 2); got != 112 {
		t.Fatalf("standard warm-up cap = %d, want 112", got)
	}
}

func TestEffectiveWarmupDailyCapUsesActiveOverrideAndDomainQuota(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	expires := now.Add(3 * 24 * time.Hour)
	override := 10_000
	quota := int64(7_500)

	if got := EffectiveWarmupDailyCap(500, &override, &expires, now, &quota, false); got != 7_500 {
		t.Fatalf("effective cap = %d, want override limited by quota to 7500", got)
	}
	if got := EffectiveWarmupDailyCap(500, &override, &expires, now, &quota, true); got != 10_000 {
		t.Fatalf("effective cap with unlimited quota = %d, want 10000", got)
	}
	if got := EffectiveWarmupDailyCap(500, &override, &expires, now, nil, false); got != 10_000 {
		t.Fatalf("effective cap without daily quota = %d, want 10000", got)
	}
}

func TestEffectiveWarmupDailyCapExpiredOverrideRevertsToCurve(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	expires := now
	override := 10_000

	if got := EffectiveWarmupDailyCap(500, &override, &expires, now, nil, false); got != 500 {
		t.Fatalf("effective cap after expiry = %d, want curve cap 500", got)
	}
	if got := EffectiveWarmupDailyCap(500, &override, nil, now, nil, false); got != 500 {
		t.Fatalf("effective cap without expiry = %d, want curve cap 500", got)
	}
}

func TestValidateWarmupOverrideDuration(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		cap       int
		expiresAt time.Time
		wantError bool
	}{
		{name: "valid maximum", cap: 1, expiresAt: now.Add(MaxWarmupOverrideDuration)},
		{name: "no expiry", cap: 1, wantError: true},
		{name: "expired", cap: 1, expiresAt: now, wantError: true},
		{name: "too long", cap: 1, expiresAt: now.Add(MaxWarmupOverrideDuration + time.Second), wantError: true},
		{name: "non-positive cap", cap: 0, expiresAt: now.Add(time.Hour), wantError: true},
		{name: "cap exceeds database integer", cap: int(1 << 31), expiresAt: now.Add(time.Hour), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateWarmupOverride(test.cap, test.expiresAt, now)
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateWarmupOverride() error = %v, wantError %v", err, test.wantError)
			}
		})
	}
}
