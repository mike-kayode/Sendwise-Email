package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestStalwartRegistrationDeliversAndStoresVerificationEmail(t *testing.T) {
	if os.Getenv("RUN_STALWART_INTEGRATION") != "1" {
		t.Skip("set RUN_STALWART_INTEGRATION=1 to run against the local Stalwart container")
	}

	baseURL := strings.TrimRight(envOr("STALWART_URL", "http://127.0.0.1:8081"), "/")
	domain := os.Getenv("STALWART_DOMAIN")
	submissionUsername := os.Getenv("STALWART_SUBMIT_USERNAME")
	submissionPassword := os.Getenv("STALWART_SUBMIT_PASSWORD")
	if domain == "" || submissionUsername == "" || submissionPassword == "" {
		t.Fatal("STALWART_DOMAIN, STALWART_SUBMIT_USERNAME, and STALWART_SUBMIT_PASSWORD are required")
	}
	sender := submissionUsername + "@" + domain
	smtpAddress := envOr("STALWART_SMTP_ADDR", "127.0.0.1:587")
	smtpHost := envOr("STALWART_SMTP_HOST", "localhost")

	mailer := &smtpMailer{
		addr:     smtpAddress,
		host:     smtpHost,
		username: sender,
		password: submissionPassword,
		from:     sender,
		tlsMode:  "starttls",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tlsConfig, err := localStalwartTLSConfig(ctx, smtpAddress, smtpHost)
	if err != nil {
		t.Fatalf("trust local Stalwart test certificate: %v", err)
	}
	mailer.tlsConfig = tlsConfig
	if err := mailer.Probe(ctx); err != nil {
		t.Fatalf("Stalwart SMTP EHLO/STARTTLS/AUTH probe failed: %v", err)
	}

	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	cfg := Config{
		BaseConfig:              kit.BaseConfig{JWTSecret: "stalwart-integration-test-secret", JWTIssuer: "email-platform"},
		AccessTokenTTL:          time.Minute,
		AppBaseURL:              "https://sendwise.test",
		MailOutboxEncryptionKey: "stalwart-integration-outbox-key",
	}
	store := NewStoreFromPool(pool)
	handler := NewHandler(
		store,
		kit.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL),
		nil,
		mailer,
		cfg,
		kit.NewTestLogger(),
	)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(fmt.Sprintf(
		`{"email":%q,"password":"correct horse battery staple","name":"Stalwart Test","organization_name":"Local Stalwart"}`,
		sender,
	)))
	request.Header.Set("Content-Type", "application/json")
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("registration status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"delivery_status":"retry_queued"`) {
		t.Fatalf("registration queued a retry instead of delivering through Stalwart: %s", response.Body.String())
	}

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		email, err := findStalwartMessage(client, baseURL, sender, submissionPassword)
		if err == nil && email.Subject == "Verify your email address" && email.Recipient() == strings.ToLower(sender) {
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("verification message for %s was not readable from Stalwart: %v", sender, lastErr)
}

func localStalwartTLSConfig(ctx context.Context, address, host string) (*tls.Config, error) {
	var pinnedCertificate []byte
	probeConfig := &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return fmt.Errorf("Stalwart presented no certificate")
			}
			certificate := state.PeerCertificates[0]
			now := time.Now()
			if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
				return fmt.Errorf("Stalwart test certificate is outside its validity period")
			}
			if err := certificate.VerifyHostname(host); err != nil {
				return fmt.Errorf("Stalwart test certificate hostname: %w", err)
			}
			pinnedCertificate = append([]byte(nil), certificate.Raw...)
			return nil
		},
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		_ = client.Close()
		return nil, fmt.Errorf("Stalwart did not advertise STARTTLS")
	}
	if err := client.StartTLS(probeConfig); err != nil {
		_ = client.Close()
		return nil, err
	}
	_ = client.Close()
	if len(pinnedCertificate) == 0 {
		return nil, fmt.Errorf("Stalwart test certificate was not pinned")
	}
	// The integration stack uses a generated self-signed certificate. Pinning
	// its exact DER certificate keeps this test local without weakening the
	// production SMTP verifier.
	return &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, pinnedCertificate) {
				return fmt.Errorf("Stalwart test certificate changed")
			}
			if err := state.PeerCertificates[0].VerifyHostname(host); err != nil {
				return fmt.Errorf("Stalwart test certificate hostname: %w", err)
			}
			return nil
		},
	}, nil
}

type stalwartEmailAddress struct {
	Email string `json:"email"`
}

type stalwartEmail struct {
	Subject string                 `json:"subject"`
	To      []stalwartEmailAddress `json:"to"`
}

func (e stalwartEmail) Recipient() string {
	for _, address := range e.To {
		if address.Email != "" {
			return strings.ToLower(address.Email)
		}
	}
	return ""
}

func findStalwartMessage(client *http.Client, baseURL, username, password string) (stalwartEmail, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/jmap/session", nil)
	if err != nil {
		return stalwartEmail{}, err
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		return stalwartEmail{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return stalwartEmail{}, fmt.Errorf("JMAP session returned HTTP %d", response.StatusCode)
	}
	var session struct {
		APIURL          string            `json:"apiUrl"`
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		return stalwartEmail{}, err
	}
	accountID := session.PrimaryAccounts["urn:ietf:params:jmap:mail"]
	if accountID == "" {
		return stalwartEmail{}, fmt.Errorf("JMAP session has no mail account")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return stalwartEmail{}, err
	}
	api, err := url.Parse(session.APIURL)
	if err != nil {
		return stalwartEmail{}, err
	}
	if api.IsAbs() {
		api.Scheme = base.Scheme
		api.Host = base.Host
	}
	if !api.IsAbs() {
		api = base.ResolveReference(api)
	}
	apiURL := api.String()

	call := func(method string, args map[string]any) (json.RawMessage, error) {
		args["accountId"] = accountID
		body, err := json.Marshal(map[string]any{
			"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
			"methodCalls": [][]any{{method, args, "sendwise"}},
		})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(username, password)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("JMAP %s returned HTTP %d", method, resp.StatusCode)
		}
		var result struct {
			MethodResponses [][]json.RawMessage `json:"methodResponses"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}
		if len(result.MethodResponses) == 0 || len(result.MethodResponses[0]) < 2 {
			return nil, fmt.Errorf("JMAP %s returned no method response", method)
		}
		if string(result.MethodResponses[0][0]) == `"error"` {
			return nil, fmt.Errorf("JMAP %s returned an error", method)
		}
		return result.MethodResponses[0][1], nil
	}

	mailboxesJSON, err := call("Mailbox/get", map[string]any{"ids": nil})
	if err != nil {
		return stalwartEmail{}, err
	}
	var mailboxes struct {
		List []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"list"`
	}
	if err := json.Unmarshal(mailboxesJSON, &mailboxes); err != nil {
		return stalwartEmail{}, err
	}
	inboxID := ""
	for _, mailbox := range mailboxes.List {
		if mailbox.Role == "inbox" {
			inboxID = mailbox.ID
			break
		}
	}
	if inboxID == "" {
		return stalwartEmail{}, fmt.Errorf("Stalwart mailbox has no inbox")
	}

	queryJSON, err := call("Email/query", map[string]any{
		"filter": map[string]any{"inMailbox": inboxID},
		"sort":   []map[string]any{{"property": "receivedAt", "isAscending": false}},
		"limit":  20,
	})
	if err != nil {
		return stalwartEmail{}, err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(queryJSON, &query); err != nil {
		return stalwartEmail{}, err
	}
	if len(query.IDs) == 0 {
		return stalwartEmail{}, fmt.Errorf("Stalwart inbox has no messages yet")
	}
	emailsJSON, err := call("Email/get", map[string]any{
		"ids":        query.IDs,
		"properties": []string{"subject", "to"},
	})
	if err != nil {
		return stalwartEmail{}, err
	}
	var emails struct {
		List []stalwartEmail `json:"list"`
	}
	if err := json.Unmarshal(emailsJSON, &emails); err != nil {
		return stalwartEmail{}, err
	}
	for _, email := range emails.List {
		if email.Subject == "Verify your email address" && email.Recipient() == strings.ToLower(username) {
			return email, nil
		}
	}
	return stalwartEmail{}, fmt.Errorf("verification message not found")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
