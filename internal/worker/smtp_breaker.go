package worker

import (
	"sync"
	"time"
)

const (
	smtpBreakerFailureThreshold = 5
	smtpBreakerFailureWindow    = 30 * time.Second
	smtpBreakerInitialCooldown  = 30 * time.Second
	smtpBreakerMaximumCooldown  = 5 * time.Minute
)

type smtpCircuitBreaker struct {
	mu            sync.Mutex
	failures      []time.Time
	openUntil     time.Time
	cooldown      time.Duration
	probeInFlight bool
}

func (b *smtpCircuitBreaker) Allow(now time.Time) (bool, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true, time.Time{}
	}
	if now.Before(b.openUntil) || b.probeInFlight {
		return false, b.openUntil
	}
	b.probeInFlight = true
	return true, b.openUntil
}

func (b *smtpCircuitBreaker) Failure(now time.Time) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cooldown == 0 {
		b.cooldown = smtpBreakerInitialCooldown
	}
	if b.probeInFlight {
		b.probeInFlight = false
		b.openUntil = now.Add(b.cooldown)
		b.cooldown = min(b.cooldown*2, smtpBreakerMaximumCooldown)
		return b.openUntil, true
	}

	firstRecent := 0
	for firstRecent < len(b.failures) && now.Sub(b.failures[firstRecent]) > smtpBreakerFailureWindow {
		firstRecent++
	}
	b.failures = append(b.failures[firstRecent:], now)
	if len(b.failures) < smtpBreakerFailureThreshold {
		return now.Add(smtpBreakerInitialCooldown), false
	}
	b.failures = nil
	b.openUntil = now.Add(b.cooldown)
	b.cooldown = min(b.cooldown*2, smtpBreakerMaximumCooldown)
	return b.openUntil, true
}

func (b *smtpCircuitBreaker) ProbeSucceeded(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	wasOpen := !b.openUntil.IsZero()
	b.openUntil = time.Time{}
	b.probeInFlight = false
	b.failures = nil
	b.cooldown = smtpBreakerInitialCooldown
	return wasOpen
}

func (b *smtpCircuitBreaker) Open(now time.Time) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openUntil, !b.openUntil.IsZero() && now.Before(b.openUntil)
}
