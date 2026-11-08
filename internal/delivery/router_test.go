package delivery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestRoutingResolverIsStalwartOnly(t *testing.T) {
	tests := []struct {
		name string
		kind DeliveryKind
	}{
		{name: "platform delivery resolves to stalwart", kind: PlatformDelivery},
		{name: "workspace campaigns resolve to stalwart", kind: CampaignDelivery},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := (RoutingResolver{}).Resolve(context.Background(), test.kind, "workspace-a", "news.example.test")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0] != ProviderStalwart {
				t.Fatalf("Resolve(%q) = %v, want [%s]", test.kind, got, ProviderStalwart)
			}
		})
	}
}

func TestRoutingResolverRejectsUnknownKind(t *testing.T) {
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

// TestCampaignsSendViaStalwartEvenWithPlatformResendConfigured is the Step 1
// proof that campaign delivery cannot reach the platform Resend fallback: the
// fallback environment variables are fully configured, yet a campaign message
// still leaves only through the registered Stalwart provider.
func TestCampaignsSendViaStalwartEvenWithPlatformResendConfigured(t *testing.T) {
	t.Setenv("PLATFORM_RESEND_API_KEY", "re_fake_platform_key_for_test_only")
	t.Setenv("PLATFORM_FROM_EMAIL", "platform@example.test")

	var stalwartCalls int
	transport := NewCampaignRouter(providerFunc(func(context.Context, Message) (*Result, error) {
		stalwartCalls++
		return &Result{MessageID: "stalwart-id", Accepted: true}, nil
	}), slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := transport.Send(context.Background(), Message{
		ID:          "campaign-1",
		WorkspaceID: "workspace-a",
		From:        "Sender <sender@news.example.test>",
		To:          "reader@example.test",
		Subject:     "Hello",
		HTML:        "<p>Hello</p>",
	})
	if err != nil {
		t.Fatalf("campaign send: %v", err)
	}
	if result.MessageID != "stalwart-id" {
		t.Fatalf("campaign send via %q, want the Stalwart provider", result.MessageID)
	}
	if stalwartCalls != 1 {
		t.Fatalf("stalwart called %d times, want 1", stalwartCalls)
	}
	providers, err := (RoutingResolver{}).Resolve(context.Background(), CampaignDelivery, "workspace-a", "news.example.test")
	if err != nil || len(providers) != 1 || providers[0] != ProviderStalwart {
		t.Fatalf("campaign providers = %v (err %v), want [stalwart]", providers, err)
	}
}

func TestRouterReturnsPermanentErrorWithoutFallback(t *testing.T) {
	router := &Router{
		Resolver: RoutingResolver{},
		Providers: map[string]Provider{
			ProviderStalwart: providerFunc(func(context.Context, Message) (*Result, error) {
				return nil, &PermanentError{Err: errors.New("recipient rejected")}
			}),
		},
	}
	if _, err := router.Send(context.Background(), CampaignDelivery, "workspace-a", "example.test", Message{ID: "campaign-2"}); err == nil {
		t.Fatal("expected permanent provider error")
	}
}
