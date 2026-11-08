package delivery

import "math"

var resendWarmupDailyCaps = [...]int{200, 500, 1000, 2500, 5000, 10000, 20000, 40000, 100000}

func WarmupDailyCap(profile string, start int, growth float64, maximum, successfulDays int) int {
	if profile == "resend" {
		return ResendWarmupDailyCap(successfulDays)
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

func ResendWarmupDailyCap(qualifiedDays int) int {
	if qualifiedDays < 0 {
		qualifiedDays = 0
	}
	if qualifiedDays >= len(resendWarmupDailyCaps) {
		return resendWarmupDailyCaps[len(resendWarmupDailyCaps)-1]
	}
	return resendWarmupDailyCaps[qualifiedDays]
}
