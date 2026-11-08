package delivery

import "math"

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
