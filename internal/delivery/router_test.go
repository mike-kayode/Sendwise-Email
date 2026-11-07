package delivery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type staticRoutingSettings struct {
	settings WorkspaceRouting
	err      error
}

func (s staticRoutingSettings) ForWorkspace(context.Context, string, string) (WorkspaceRouting, error) {
	return s.settings, s.err
}

func TestRoutingResolver(t *testing.T) {
	tests := []struct {
		name     string
		kind     DeliveryKind
		settings WorkspaceRouting
		want     []string
	}{
		{name: "platform defaults to stalwart and resend", kind: PlatformDelivery, want: []string{ProviderStalwart, ProviderResend}},
		{name: "workspace default is stalwart", kind: CampaignDelivery, want: []string{ProviderStalwart}},
		{name: "resend campaign has no cross-provider fallback", kind: CampaignDelivery, settings: WorkspaceRouting{Provider: ProviderResend}, want: []string{ProviderResend}},
		{name: "fallback requires both providers authenticated", kind: CampaignDelivery, settings: WorkspaceRouting{Provider: ProviderStalwart, FallbackProvider: ProviderResend, StalwartDomainAuth: true}, want: []string{ProviderStalwart}},
		{name: "authenticated providers allow explicit fallback", kind: CampaignDelivery, settings: WorkspaceRouting{Provider: ProviderResend, FallbackProvider: ProviderStalwart, StalwartDomainAuth: true, ResendDomainAuth: true}, want: []string{ProviderResend, ProviderStalwart}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := RoutingResolver{Settings: staticRoutingSettings{settings: test.settings}}
			got, err := resolver.Resolve(context.Background(), test.kind, "workspace-a", "news.example.test")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("Resolve() = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("Resolve() = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestRoutingResolverRejectsInvalidConfiguration(t *testing.T) {
	resolver := RoutingResolver{Settings: staticRoutingSettings{settings: WorkspaceRouting{Provider: ProviderResend, FallbackProvider: ProviderResend}}}
	if _, err := resolver.Resolve(context.Background(), CampaignDelivery, "workspace-a", "example.test"); err == nil {
		t.Fatal("expected same-provider fallback to be rejected")
	}
	if _, err := (RoutingResolver{}).Resolve(context.Background(), "unknown", "workspace-a", "example.test"); err == nil {
		t.Fatal("expected unknown delivery kind to be rejected")
	}
}

func TestCircuitBreakerWindowAndOpenDuration(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	breaker := NewCircuitBreaker(2, time.Minute, 2*time.Minute)
	breaker.now = func() time.Time { return now }

	if breaker.Failure() {
		t.Fatal("circuit opened before threshold")
	}
	now = now.Add(61 * time.Second)
	if breaker.Failure() {
		t.Fatal("failure outside the rolling window should be discarded")
	}
	now = now.Add(61 * time.Second)
	if breaker.Failure() {
		t.Fatal("circuit opened before threshold")
	}
	if !breaker.Failure() {
		t.Fatal("circuit did not open at threshold")
	}
	if breaker.Allow() {
		t.Fatal("open circuit allowed a request")
	}
	now = now.Add(2 * time.Minute)
	if !breaker.Allow() {
		t.Fatal("circuit remained open after cooldown")
	}
}

type providerFunc func(context.Context, Message) (*Result, error)

func (f providerFunc) Send(ctx context.Context, message Message) (*Result, error) {
	return f(ctx, message)
}

type routingSettingsFunc func(context.Context, string, string) (WorkspaceRouting, error)

func (f routingSettingsFunc) ForWorkspace(ctx context.Context, workspaceID, fromDomain string) (WorkspaceRouting, error) {
	return f(ctx, workspaceID, fromDomain)
}

func TestRouterPlatformFailoverAndCampaignIsolation(t *testing.T) {
	var mu sync.Mutex
	stalwartCalls := 0
	resendCalls := 0
	router := &Router{
		Resolver: RoutingResolver{Settings: routingSettingsFunc(func(context.Context, string, string) (WorkspaceRouting, error) {
			return WorkspaceRouting{Provider: ProviderResend}, nil
		})},
		Providers: map[string]Provider{
			ProviderStalwart: providerFunc(func(context.Context, Message) (*Result, error) {
				mu.Lock()
				stalwartCalls++
				mu.Unlock()
				return nil, errors.New("connection refused")
			}),
			ProviderResend: providerFunc(func(context.Context, Message) (*Result, error) {
				mu.Lock()
				resendCalls++
				mu.Unlock()
				return &Result{MessageID: "resend-id", Accepted: true}, nil
			}),
		},
	}
	result, err := router.Send(context.Background(), PlatformDelivery, "", "platform.example", Message{ID: "platform-1"})
	if err != nil || result.MessageID != "resend-id" {
		t.Fatalf("platform send = (%v, %v), want resend success", result, err)
	}
	result, err = router.Send(context.Background(), CampaignDelivery, "workspace-a", "workspace.example", Message{ID: "campaign-1"})
	if err != nil || result.MessageID != "resend-id" {
		t.Fatalf("campaign send = (%v, %v), want resend success", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if stalwartCalls != 1 || resendCalls != 2 {
		t.Fatalf("provider calls = stalwart:%d resend:%d; campaign must not fall back across providers", stalwartCalls, resendCalls)
	}
}

func TestRouterDoesNotFailOverPermanentPlatformError(t *testing.T) {
	resendCalls := 0
	router := &Router{
		Resolver: RoutingResolver{},
		Providers: map[string]Provider{
			ProviderStalwart: providerFunc(func(context.Context, Message) (*Result, error) {
				return nil, &PermanentError{Err: errors.New("recipient rejected")}
			}),
			ProviderResend: providerFunc(func(context.Context, Message) (*Result, error) {
				resendCalls++
				return &Result{Accepted: true}, nil
			}),
		},
	}
	if _, err := router.Send(context.Background(), PlatformDelivery, "", "", Message{ID: "platform-2"}); err == nil {
		t.Fatal("expected permanent provider error")
	}
	if resendCalls != 0 {
		t.Fatalf("resend called %d times after permanent error", resendCalls)
	}
}
