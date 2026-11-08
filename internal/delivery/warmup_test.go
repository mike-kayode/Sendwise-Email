package delivery

import "testing"

func TestResendWarmupDailyCapRequiresQualifiedDayStages(t *testing.T) {
	want := []int{200, 500, 1000, 2500, 5000, 10000, 20000, 40000, 100000, 100000}
	for day, expected := range want {
		if got := ResendWarmupDailyCap(day); got != expected {
			t.Errorf("ResendWarmupDailyCap(%d) = %d, want %d", day, got, expected)
		}
	}
}

func TestWarmupDailyCapKeepsStandardProfileBehavior(t *testing.T) {
	if got := WarmupDailyCap("standard", 50, 1.5, 10000, 2); got != 112 {
		t.Fatalf("standard warm-up cap = %d, want 112", got)
	}
}
