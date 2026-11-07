package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestSharedResendLimiterPrioritizesPlatformRequests(t *testing.T) {
	apiKey := fmt.Sprintf("priority-test-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := WaitResendRequest(ctx, apiKey, 4, false); err != nil {
		t.Fatal(err)
	}

	campaignDone := make(chan error, 1)
	go func() {
		campaignDone <- WaitResendRequest(ctx, apiKey, 4, false)
	}()
	time.Sleep(20 * time.Millisecond)
	platformDone := make(chan error, 1)
	go func() {
		platformDone <- WaitResendRequest(ctx, apiKey, 4, true)
	}()

	select {
	case err := <-platformDone:
		if err != nil {
			t.Fatal(err)
		}
	case err := <-campaignDone:
		t.Fatalf("campaign received the shared rate-limit slot before platform mail: %v", err)
	case <-ctx.Done():
		t.Fatal("platform mail did not receive a rate-limit slot")
	}
	select {
	case err := <-campaignDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("campaign mail did not receive a rate-limit slot")
	}
}

func TestDistributedResendLimiterSharesPriorityAcrossClients(t *testing.T) {
	address := os.Getenv("TEST_REDIS_URL")
	if address == "" {
		t.Skip("set TEST_REDIS_URL to run the shared Redis limiter test")
	}
	options, err := redis.ParseURL(address)
	if err != nil {
		t.Fatal(err)
	}
	platformClient := redis.NewClient(options)
	defer platformClient.Close()
	campaignClient := redis.NewClient(options)
	defer campaignClient.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := platformClient.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis is unavailable: %v", err)
	}

	apiKey := fmt.Sprintf("distributed-priority-test-%d", time.Now().UnixNano())
	if err := waitDistributedResendRequest(ctx, campaignClient, apiKey, 4, false); err != nil {
		t.Fatal(err)
	}
	campaignDone := make(chan error, 1)
	go func() {
		campaignDone <- waitDistributedResendRequest(ctx, campaignClient, apiKey, 4, false)
	}()
	time.Sleep(20 * time.Millisecond)
	platformDone := make(chan error, 1)
	go func() {
		platformDone <- waitDistributedResendRequest(ctx, platformClient, apiKey, 4, true)
	}()
	select {
	case err := <-platformDone:
		if err != nil {
			t.Fatal(err)
		}
	case err := <-campaignDone:
		t.Fatalf("campaign received the distributed slot before platform mail: %v", err)
	case <-ctx.Done():
		t.Fatal("platform mail did not receive a distributed rate-limit slot")
	}
	select {
	case err := <-campaignDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("campaign mail did not receive a distributed rate-limit slot")
	}
}

func TestResendTransportSendsTaggedBatchWithIdempotencyKey(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/emails/batch" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test_key_fake_only" {
			t.Errorf("authorization header = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "batch-key" {
			t.Errorf("idempotency key = %q", got)
		}
		var payload []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload) != 2 {
			t.Fatalf("batch size = %d, want 2", len(payload))
		}
		for i, email := range payload {
			tags, ok := email["tags"].([]any)
			if !ok || len(tags) != 3 {
				t.Fatalf("email %d tags = %#v", i, email["tags"])
			}
			found := map[string]bool{}
			for _, rawTag := range tags {
				tag := rawTag.(map[string]any)
				if name, ok := tag["name"].(string); ok {
					found[name] = true
				}
			}
			for _, field := range []string{"workspace_id", "campaign_id", "message_id"} {
				if !found[field] {
					t.Errorf("email %d missing %s tag", i, field)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"resend-1"},{"id":"resend-2"}]}`))
	}))
	defer server.Close()

	transport := &ResendTransport{
		APIKey: "test_key_fake_only", BaseURL: server.URL, RequestsRate: 1000, Client: server.Client(), MaxRetries: -1,
	}
	messages := []Message{
		{ID: "message-1", WorkspaceID: "workspace-1", CampaignID: "campaign-1", From: "One <one@example.test>", To: "a@example.test"},
		{ID: "message-2", WorkspaceID: "workspace-1", CampaignID: "campaign-1", From: "Two <two@example.test>", To: "b@example.test"},
	}
	results, err := transport.SendBatch(context.Background(), messages, "batch-key")
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || len(results) != 2 || results[0].MessageID != "resend-1" || !results[1].Accepted {
		t.Fatalf("results = %#v, requests = %d", results, requests.Load())
	}
}

func TestResendTransportRetries429AndHonorsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	firstRequestAt := time.Time{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			firstRequestAt = time.Now()
			w.Header().Set("Retry-After", "0.05")
			http.Error(w, `{"message":"rate limited"}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"accepted"}]}`))
	}))
	defer server.Close()
	transport := &ResendTransport{
		APIKey: "test_key_fake_retry", BaseURL: server.URL, RequestsRate: 1000, Client: server.Client(), MaxRetries: 1,
	}
	_, err := transport.Send(context.Background(), Message{ID: "retry-id", To: "person@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("request attempts = %d, want 2", attempts.Load())
	}
	if time.Since(firstRequestAt) < 40*time.Millisecond {
		t.Fatal("retry did not honor Retry-After")
	}
}

func TestResendTransportRejectsOversizedBatch(t *testing.T) {
	messages := make([]Message, resendBatchSize+1)
	transport := &ResendTransport{APIKey: "test_key_fake_only"}
	if _, err := transport.SendBatch(context.Background(), messages, "batch-key"); err == nil || !strings.Contains(err.Error(), "maximum is 100") {
		t.Fatalf("SendBatch() error = %v, want batch limit error", err)
	}
}

func TestParseRetryAfterSupportsRateLimitHeaders(t *testing.T) {
	for _, header := range []string{"Retry-After", "ratelimit-reset", "x-ratelimit-reset"} {
		headers := make(http.Header)
		headers.Set(header, "0.25")
		if got := parseRetryAfter(headers, time.Now()); got != 250*time.Millisecond {
			t.Errorf("parseRetryAfter(%s) = %s, want 250ms", header, got)
		}
	}
}
