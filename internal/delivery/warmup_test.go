package delivery

import "testing"

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
