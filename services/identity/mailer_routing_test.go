package identity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testMailer struct {
	provider string
	err      error
	calls    int
}

func (m *testMailer) Send(context.Context, MailMessage) error {
	m.calls++
	return m.err
}

func (m *testMailer) Provider() string { return m.provider }

func TestPlatformMailerFallsBackAndOpensCircuit(t *testing.T) {
	primary := &testMailer{provider: "smtp", err: errors.New("connection refused")}
	fallback := &testMailer{provider: "resend"}
	mailer := &platformMailer{
		primary: primary, fallback: fallback,
		breaker: newMailerCircuitBreaker(5, time.Minute, 2*time.Minute),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for i := 0; i < 5; i++ {
		if err := mailer.Send(context.Background(), MailMessage{To: "person@example.test"}); err != nil {
			t.Fatalf("fallback send %d: %v", i+1, err)
		}
	}
	if primary.calls != 5 || fallback.calls != 5 {
		t.Fatalf("calls before circuit opens: primary=%d fallback=%d", primary.calls, fallback.calls)
	}
	if err := mailer.Send(context.Background(), MailMessage{To: "person@example.test"}); err != nil {
		t.Fatal(err)
	}
	if primary.calls != 5 || fallback.calls != 6 {
		t.Fatalf("calls with open circuit: primary=%d fallback=%d", primary.calls, fallback.calls)
	}
}

func TestPlatformMailerFailoverClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "SMTP authentication failure", err: &smtpDeliveryError{phase: "AUTH PLAIN", code: 535, response: "auth failed"}, want: true},
		{name: "SMTP transient response", err: &smtpDeliveryError{phase: "RCPT TO", code: 451, response: "retry"}, want: true},
		{name: "SMTP permanent response", err: &smtpDeliveryError{phase: "RCPT TO", code: 550, response: "rejected"}, want: true},
		{name: "Resend rate limited", err: &resendDeliveryError{status: 429, temporary: true}, want: true},
		{name: "Resend server failure", err: &resendDeliveryError{status: 503, temporary: true}, want: true},
		{name: "Resend permanent client error", err: &resendDeliveryError{status: 400}, want: false},
		{name: "SMTP connection failure", err: errors.New("dial tcp: connection refused"), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldFailoverMail(test.err); got != test.want {
				t.Fatalf("shouldFailoverMail() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPlatformMailerUsesResendFakeWhenSMTPIsUnavailable(t *testing.T) {
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer re_fake_platform_key" {
			t.Errorf("authorization header = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fake_message_id"}`)
	}))
	defer server.Close()
	mailer := &platformMailer{
		primary: &smtpMailer{addr: "127.0.0.1:1", host: "localhost", from: "platform@example.test", tlsMode: "none"},
		fallback: &resendMailer{
			apiKey: "re_fake_platform_key", fromEmail: "platform@example.test",
			baseURL: server.URL, client: server.Client(),
		},
		breaker: newMailerCircuitBreaker(5, time.Minute, 2*time.Minute),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := mailer.Send(context.Background(), MailMessage{
		To: "person@example.test", Subject: "Verification", Text: "verify",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(requestBody, `"from":"platform@example.test"`) ||
		!strings.Contains(requestBody, `"to":["person@example.test"]`) ||
		!strings.Contains(requestBody, `"name":"workspace_id","value":"platform"`) ||
		!strings.Contains(requestBody, `"name":"campaign_id","value":"platform"`) ||
		!strings.Contains(requestBody, `"name":"message_id"`) {
		t.Fatalf("Resend request did not contain platform sender and recipient: %s", requestBody)
	}
}

func TestMailerCircuitBreakerCooldown(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	breaker := newMailerCircuitBreaker(2, time.Minute, 2*time.Minute)
	breaker.now = func() time.Time { return now }
	breaker.Failure()
	breaker.Failure()
	if breaker.Allow() {
		t.Fatal("circuit should be open after threshold")
	}
	now = now.Add(2 * time.Minute)
	if !breaker.Allow() {
		t.Fatal("circuit should close after cooldown")
	}
}
