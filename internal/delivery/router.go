package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/khayode/email-platform/internal/observability"
)

type DeliveryKind string

const (
	PlatformDelivery DeliveryKind = "platform"
	CampaignDelivery DeliveryKind = "campaign"
)

const (
	ProviderStalwart = "stalwart"
	ProviderLog      = "log"
)

type RoutingResolver struct{}

// Resolve returns the provider chain for a delivery kind. Everything that
// flows through this router — campaigns and the platform alike — sends via
// Stalwart. Platform transactional fallback is not part of this package, so
// campaign code cannot route through it.
func (r RoutingResolver) Resolve(ctx context.Context, kind DeliveryKind, workspaceID, fromDomain string) ([]string, error) {
	switch kind {
	case PlatformDelivery, CampaignDelivery:
		return []string{ProviderStalwart}, nil
	default:
		return nil, fmt.Errorf("unsupported delivery kind %q", kind)
	}
}

type CircuitBreaker struct {
	mu        sync.Mutex
	failures  []time.Time
	openUntil time.Time
	threshold int
	window    time.Duration
	openFor   time.Duration
	now       func() time.Time
}

func NewCircuitBreaker(threshold int, window, openFor time.Duration) *CircuitBreaker {
	if threshold < 1 {
		threshold = 5
	}
	if window <= 0 {
		window = time.Minute
	}
	if openFor <= 0 {
		openFor = 2 * time.Minute
	}
	return &CircuitBreaker{
		threshold: threshold,
		window:    window,
		openFor:   openFor,
		now:       time.Now,
	}
}

func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if now.Before(b.openUntil) {
		return false
	}
	if !b.openUntil.IsZero() {
		b.openUntil = time.Time{}
		b.failures = nil
	}
	return true
}

func (b *CircuitBreaker) Failure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	cutoff := now.Add(-b.window)
	firstValid := 0
	for firstValid < len(b.failures) && b.failures[firstValid].Before(cutoff) {
		firstValid++
	}
	b.failures = append(b.failures[firstValid:], now)
	if len(b.failures) < b.threshold {
		return false
	}
	b.openUntil = now.Add(b.openFor)
	return true
}

func (b *CircuitBreaker) Success() {
	b.mu.Lock()
	b.failures = nil
	b.openUntil = time.Time{}
	b.mu.Unlock()
}

type Router struct {
	Resolver  RoutingResolver
	Providers map[string]Provider
	Breaker   *CircuitBreaker
	Logger    *slog.Logger
}

func (r *Router) Send(ctx context.Context, kind DeliveryKind, workspaceID, fromDomain string, message Message) (*Result, error) {
	providers, err := r.Resolver.Resolve(ctx, kind, workspaceID, fromDomain)
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, errors.New("delivery resolver returned no providers")
	}

	var lastErr error
	for i, name := range providers {
		provider, ok := r.Providers[name]
		if !ok {
			lastErr = fmt.Errorf("delivery provider %q is not configured", name)
			continue
		}
		if name == ProviderStalwart && kind == PlatformDelivery && r.Breaker != nil && !r.Breaker.Allow() {
			if r.Logger != nil {
				r.Logger.Warn("delivery provider circuit open; failing over", "kind", kind, "from", name, "to", nextProvider(providers, i))
			}
			observability.ProviderFallbacks.WithLabelValues(string(kind), name, nextProvider(providers, i), "circuit_open").Inc()
			continue
		}
		result, sendErr := provider.Send(ctx, message)
		if sendErr == nil {
			if name == ProviderStalwart && r.Breaker != nil {
				r.Breaker.Success()
			}
			return result, nil
		}
		lastErr = sendErr
		if name == ProviderStalwart && kind == PlatformDelivery && r.Breaker != nil {
			r.Breaker.Failure()
		}
		if i+1 < len(providers) && shouldFailover(kind, sendErr) {
			next := providers[i+1]
			if r.Logger != nil {
				r.Logger.Warn("delivery provider failed; trying fallback", "kind", kind, "from", name, "to", next, "error", sendErr)
			}
			observability.ProviderFallbacks.WithLabelValues(string(kind), name, next, "provider_error").Inc()
			continue
		}
		return nil, sendErr
	}
	if lastErr == nil {
		lastErr = errors.New("all delivery providers are unavailable")
	}
	return nil, lastErr
}

func nextProvider(providers []string, index int) string {
	if index+1 < len(providers) {
		return providers[index+1]
	}
	return "none"
}

func shouldFailover(kind DeliveryKind, err error) bool {
	var permanent *PermanentError
	if errors.As(err, &permanent) {
		var providerErr *ProviderError
		return errors.As(permanent.Err, &providerErr) && providerErr.Temporary
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Temporary
	}
	if kind != PlatformDelivery && kind != CampaignDelivery {
		return false
	}
	return true
}
