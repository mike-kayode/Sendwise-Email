package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicContactStoresSubmissionAndNotifies(t *testing.T) {
	if integrationPool == nil {
		t.Skip("set TEST_DATABASE_URL or INTEGRATION_DATABASE_URL to run public contact integration test")
	}

	server := newIntegrationServer(t)
	server.cfg.ContactNotifyEmail = "support@example.test"
	email := "contact-" + uuid.NewString() + "@example.test"
	notified := false
	server.contactNotifier = func(_ context.Context, name, notifiedEmail, topic, message string) error {
		notified = true
		if name != "Ada Lovelace" || notifiedEmail != email ||
			topic != "Plans and pricing" || !strings.Contains(message, "available plans") {
			t.Fatalf("unexpected notification values: %q, %q, %q, %q", name, notifiedEmail, topic, message)
		}
		return nil
	}

	payload, err := json.Marshal(publicContactRequest{
		Name:    "Ada Lovelace",
		Email:   email,
		Topic:   "Plans and pricing",
		Message: "I would like to know more about the available plans.",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/contact", strings.NewReader(string(payload)))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusAccepted, response.Body)
	}
	if !notified {
		t.Fatal("configured contact notification was not sent")
	}

	var status string
	if err := integrationPool.QueryRow(context.Background(),
		`SELECT notification_status FROM contact_messages WHERE email = $1`, email,
	).Scan(&status); err != nil {
		t.Fatalf("load stored contact: %v", err)
	}
	if status != "sent" {
		t.Fatalf("notification status = %q, want sent", status)
	}
	if _, err := integrationPool.Exec(context.Background(), `DELETE FROM contact_messages WHERE email = $1`, email); err != nil {
		t.Fatalf("remove test contact: %v", err)
	}
}

func TestPublicContactNotificationReachesMailpit(t *testing.T) {
	if integrationPool == nil {
		t.Skip("set TEST_DATABASE_URL or INTEGRATION_DATABASE_URL to run public contact integration test")
	}
	mailpitURL := strings.TrimRight(os.Getenv("MAILPIT_API_URL"), "/")
	if mailpitURL == "" {
		t.Skip("set MAILPIT_API_URL to verify notification delivery through local SMTP")
	}

	server := newIntegrationServer(t)
	server.cfg.SMTPAddr = "127.0.0.1:1025"
	server.cfg.SMTPHost = "127.0.0.1"
	server.cfg.SMTPFrom = "sendwise-test@example.test"
	server.cfg.ContactNotifyEmail = "contact-" + uuid.NewString() + "@example.test"
	server.contactNotifier = func(ctx context.Context, name, email, topic, message string) error {
		return sendContactNotification(ctx, server.cfg, name, email, topic, message)
	}

	payload, err := json.Marshal(publicContactRequest{
		Name:    "Mailpit Contact Proof",
		Email:   "sender@example.test",
		Topic:   "Something else",
		Message: "This message verifies the Sendwise contact notification path.",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/contact", strings.NewReader(string(payload)))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusAccepted, response.Body)
	}

	var status string
	if err := integrationPool.QueryRow(context.Background(),
		`SELECT notification_status FROM contact_messages WHERE email = $1`, "sender@example.test",
	).Scan(&status); err != nil {
		t.Fatalf("load stored contact: %v", err)
	}
	if status != "sent" {
		t.Fatalf("notification status = %q, want sent", status)
	}

	searchURL := mailpitURL + "/api/v1/search?" + url.Values{
		"query": {"to:" + server.cfg.ContactNotifyEmail},
	}.Encode()
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := client.Get(searchURL)
		if err != nil {
			t.Fatalf("search Mailpit for contact notification: %v", err)
		}
		body, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if readErr != nil {
			t.Fatalf("read Mailpit response: %v", readErr)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("Mailpit status = %d, body: %s", res.StatusCode, body)
		}
		if strings.Contains(string(body), server.cfg.ContactNotifyEmail) &&
			strings.Contains(string(body), "Sendwise contact: Something else") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Mailpit did not receive notification for %s", server.cfg.ContactNotifyEmail)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := integrationPool.Exec(context.Background(),
		`DELETE FROM contact_messages WHERE email = $1`, "sender@example.test",
	); err != nil {
		t.Fatalf("remove test contact: %v", err)
	}
}
