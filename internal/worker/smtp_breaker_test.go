package worker

import (
	"testing"
	"time"
)

func TestSMTPBreakerOpensAfterConsecutiveInfrastructureFailures(t *testing.T) {
	breaker := smtpCircuitBreaker{}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for failure := 1; failure < smtpBreakerFailureThreshold; failure++ {
		if _, opened := breaker.Failure(now.Add(time.Duration(failure) * time.Second)); opened {
			t.Fatalf("breaker opened on failure %d before threshold", failure)
		}
	}
	until, opened := breaker.Failure(now.Add(5 * time.Second))
	if !opened || !until.Equal(now.Add(35*time.Second)) {
		t.Fatalf("fifth failure opened=%t until=%s, want true and %s", opened, until, now.Add(35*time.Second))
	}
	if allowed, _ := breaker.Allow(now.Add(10 * time.Second)); allowed {
		t.Fatal("breaker allowed work during cooldown")
	}
	if allowed, _ := breaker.Allow(until); !allowed {
		t.Fatal("breaker did not allow a probe after cooldown")
	}
	if allowed, _ := breaker.Allow(until); allowed {
		t.Fatal("breaker allowed more than one concurrent probe")
	}
	if !breaker.ProbeSucceeded(until) {
		t.Fatal("successful probe did not close the breaker")
	}
	if allowed, _ := breaker.Allow(until); !allowed {
		t.Fatal("closed breaker did not allow the next job")
	}
}

func TestSMTPBreakerDoublesCooldownAfterFailedProbe(t *testing.T) {
	breaker := smtpCircuitBreaker{}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	var until time.Time
	for failure := 0; failure < smtpBreakerFailureThreshold; failure++ {
		until, _ = breaker.Failure(now.Add(time.Duration(failure) * time.Second))
	}
	if allowed, _ := breaker.Allow(until); !allowed {
		t.Fatal("first probe was not allowed")
	}
	nextOpen, opened := breaker.Failure(until.Add(time.Second))
	if !opened || !nextOpen.Equal(until.Add(time.Second+2*smtpBreakerInitialCooldown)) {
		t.Fatalf("failed probe opened=%t until=%s, want cooldown doubled to 60 seconds", opened, nextOpen)
	}
}
