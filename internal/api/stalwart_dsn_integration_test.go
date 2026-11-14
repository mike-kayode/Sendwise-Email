package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/transactional"
)

func TestLocalStalwartDSNUpdatesTransactionalRecipient(t *testing.T) {
	if os.Getenv("RUN_STALWART_INTEGRATION") != "1" {
		t.Skip("set RUN_STALWART_INTEGRATION=1 to run against the local Stalwart container")
	}
	if integrationPool == nil {
		t.Fatal("TEST_DATABASE_URL is required for the local Stalwart DSN proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	baseURL := strings.TrimRight(stalwartEnvOr("STALWART_URL", "http://127.0.0.1:8081"), "/")
	if !strings.HasPrefix(baseURL, "http://127.0.0.1:") && !strings.HasPrefix(baseURL, "http://localhost:") {
		t.Fatal("the DSN proof only permits a loopback Stalwart management URL")
	}
	platformDomain := strings.TrimSpace(os.Getenv("STALWART_DOMAIN"))
	adminUsername := os.Getenv("STALWART_ADMIN_USERNAME")
	adminPassword := os.Getenv("STALWART_ADMIN_PASSWORD")
	submissionUsername := os.Getenv("STALWART_SUBMIT_USERNAME")
	submissionPassword := os.Getenv("STALWART_SUBMIT_PASSWORD")
	if platformDomain == "" || adminUsername == "" || adminPassword == "" ||
		submissionUsername == "" || submissionPassword == "" {
		t.Fatal("local Stalwart domain and credentials are required")
	}
	smtpAddress := stalwartEnvOr("STALWART_SMTP_ADDR", "127.0.0.1:587")
	smtpHost := stalwartEnvOr("STALWART_SMTP_HOST", "localhost")
	host, _, err := net.SplitHostPort(smtpAddress)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		t.Fatal("the DSN proof only permits a loopback Stalwart SMTP address")
	}
	domain := "dsn-" + uuid.NewString() + "." + strings.ToLower(platformDomain)

	adminHTTP := &http.Client{Timeout: 10 * time.Second}
	_, bouncePassword, _, err := transactional.NewSMTPSecret()
	if err != nil {
		t.Fatalf("create local bounce mailbox test password: %v", err)
	}
	if err := ensureLocalStalwartBounceMailbox(ctx, adminHTTP, baseURL, adminUsername,
		adminPassword, domain, bouncePassword); err != nil {
		t.Fatalf("prepare local Stalwart bounce mailbox: %v", err)
	}

	owner := seedIntegrationUser(t, "stalwart-dsn-"+uuid.NewString()+"@example.test",
		"stalwart-dsn-password", "Stalwart DSN", "Stalwart DSN Workspace")
	publicKey, encryptedPrivateKey, err := delivery.GenerateDKIMKeyPair(integrationCfg.DKIMEncryptionKey)
	if err != nil {
		t.Fatalf("generate transactional test DKIM key: %v", err)
	}
	sendingDomainID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO sending_domains
			(id, organization_id, domain, status, spf_status, dkim_status, transactional_enabled,
			 dkim_selector, dkim_public_key, dkim_private_key)
		VALUES ($1, $2, $3, 'verified', 'verified', 'verified', true, 'sendwise-test', $4, $5)
	`, sendingDomainID, owner.Organization.ID, domain, publicKey, encryptedPrivateKey); err != nil {
		t.Fatalf("seed verified transactional sending domain: %v", err)
	}
	if _, err := integrationPool.Exec(ctx, `
		UPDATE transactional_settings SET enabled = true, status = 'active'
		WHERE organization_id = $1
	`, owner.Organization.ID); err != nil {
		t.Fatalf("enable transactional workspace: %v", err)
	}

	recipient := "dsn-" + uuid.NewString() + "@sendwise.invalid"
	receipt, err := transactional.NewStore(integrationPool).Enqueue(ctx,
		transactional.EnqueueOptions{OrganizationID: owner.Organization.ID, EnforceLimits: true},
		transactional.MessageInput{
			From: strings.ToLower(submissionUsername + "@" + domain), To: []string{recipient},
			Subject: "Sendwise local Stalwart DSN proof " + uuid.NewString(),
			Text:    "This message is used only to verify local Stalwart delivery feedback.",
		})
	if err != nil {
		t.Fatalf("enqueue local Stalwart proof message: %v", err)
	}

	var job transactional.Job
	err = integrationPool.QueryRow(ctx, `
		SELECT r.id::text, m.id::text, m.organization_id::text, m.message_id, m.from_email,
		       r.email, m.subject, m.html, m.text_content, m.headers, m.attachments,
		       r.recipient_type, r.attempts + 1, m.created_at
		FROM transactional_message_recipients r
		JOIN transactional_messages m ON m.id = r.message_id
		WHERE m.id = $1
	`, receipt.ID).Scan(
		&job.RecipientID, &job.MessageUUID, &job.Organization, &job.MessageID,
		&job.From, &job.To, &job.Subject, &job.HTML, &job.Text,
		&job.Headers, &job.Attachments, &job.RecipientType, &job.Attempts, &job.CreatedAt,
	)
	if err != nil {
		t.Fatalf("load local Stalwart proof message: %v", err)
	}
	store := transactional.NewStore(integrationPool)
	if err := store.TransitionRecipient(ctx, job.RecipientID, transactional.StatusSending,
		"system", 0, "", "local Stalwart DSN proof", time.Time{}, nil); err != nil {
		t.Fatalf("mark Stalwart proof recipient sending: %v", err)
	}
	previousConfig := integrationCfg
	integrationCfg.BounceDomain = domain
	defer func() { integrationCfg = previousConfig }()
	token, returnPath, err := transactional.NewVERPForRecipient(
		integrationCfg.JWTSecret, domain, owner.Organization.ID, receipt.ID, recipient,
	)
	if err != nil {
		t.Fatalf("create local Stalwart VERP address: %v", err)
	}
	if err := store.SaveVERPToken(ctx, &job, token, recipient); err != nil {
		t.Fatalf("save local Stalwart VERP token: %v", err)
	}
	dkim, err := delivery.LoadDKIMConfig(ctx, integrationPool, owner.Organization.ID,
		job.From, integrationCfg.DKIMEncryptionKey)
	if err != nil || dkim == nil {
		t.Fatalf("load transactional test DKIM key: config=%v error=%v", dkim != nil, err)
	}

	smtpPool := delivery.NewSMTPPool(smtpAddress, smtpHost,
		strings.ToLower(submissionUsername+"@"+platformDomain), submissionPassword, true, 1)
	defer smtpPool.Close()
	sent, err := smtpPool.Send(ctx, delivery.Message{
		ID: job.MessageID, WorkspaceID: owner.Organization.ID,
		From: job.From, EnvelopeFrom: returnPath, To: job.To, Subject: job.Subject,
		Text: job.Text, DKIM: dkim, RequestDSN: true,
	})
	if err != nil {
		t.Fatalf("submit transactional proof message to local Stalwart: %v", err)
	}
	if sent == nil || !sent.Accepted {
		t.Fatal("local Stalwart did not confirm SMTP acceptance")
	}
	if err := store.MarkAccepted(ctx, &job, sent.MessageID); err != nil {
		t.Fatalf("record local Stalwart SMTP acceptance: %v", err)
	}

	rawDSN, feedback, err := waitForLocalStalwartDSN(ctx, adminHTTP, baseURL,
		"bounces@"+domain, bouncePassword, returnPath)
	if err != nil {
		t.Fatalf("wait for local Stalwart DSN delivery: %v", err)
	}
	if (feedback.Type != delivery.FeedbackHardBounce && feedback.Type != delivery.FeedbackSoftBounce) ||
		!strings.EqualFold(feedback.Recipient, recipient) ||
		!strings.EqualFold(feedback.ReturnPath, returnPath) {
		t.Fatalf("Stalwart DSN = type %q, recipient %q, return-path %q",
			feedback.Type, feedback.Recipient, feedback.ReturnPath)
	}

	server := New(integrationCfg, integrationPool, integrationTokens, nil,
		nil, nil, observability.NewLogger("test", "api"))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/stalwart-feedback", bytes.NewReader(rawDSN))
	request.Header.Set("X-Webhook-Signature",
		computeWebhookSignature(integrationCfg.WebhookSecret, time.Now().Unix(), string(rawDSN)))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("ingest local Stalwart DSN returned %d: %s", response.Code, response.Body.String())
	}
	var messageStatus, recipientStatus string
	if err := integrationPool.QueryRow(ctx, `
		SELECT m.status, r.status
		FROM transactional_messages m
		JOIN transactional_message_recipients r ON r.message_id = m.id
		WHERE m.id = $1
	`, receipt.ID).Scan(&messageStatus, &recipientStatus); err != nil {
		t.Fatalf("load status after local Stalwart DSN: %v", err)
	}
	if messageStatus != "bounced" || recipientStatus != "bounced" {
		t.Fatalf("local Stalwart DSN statuses = message %q, recipient %q; want bounced",
			messageStatus, recipientStatus)
	}
}

func ensureLocalStalwartBounceMailbox(
	ctx context.Context,
	client *http.Client,
	baseURL, adminUsername, adminPassword, domain, mailboxPassword string,
) error {
	sessionRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/jmap/session", nil)
	if err != nil {
		return err
	}
	sessionRequest.SetBasicAuth(adminUsername, adminPassword)
	sessionResponse, err := client.Do(sessionRequest)
	if err != nil {
		return err
	}
	defer sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("local Stalwart management session returned HTTP %d", sessionResponse.StatusCode)
	}
	var session struct {
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.NewDecoder(sessionResponse.Body).Decode(&session); err != nil {
		return err
	}
	accountID := session.PrimaryAccounts["urn:stalwart:jmap"]
	if accountID == "" {
		return fmt.Errorf("local Stalwart management account is unavailable")
	}
	domainsJSON, err := callLocalStalwartManagement(ctx, client, baseURL, adminUsername,
		adminPassword, accountID, "x:Domain/get", map[string]any{"ids": nil})
	if err != nil {
		return err
	}
	var domains struct {
		List []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(domainsJSON, &domains); err != nil {
		return err
	}
	domainID := ""
	for _, entry := range domains.List {
		if strings.EqualFold(entry.Name, domain) {
			domainID = entry.ID
			break
		}
	}
	if domainID == "" {
		createdJSON, err := callLocalStalwartManagement(ctx, client, baseURL, adminUsername,
			adminPassword, accountID, "x:Domain/set", map[string]any{
				"create": map[string]any{"#sendwise-dsn-domain": map[string]any{
					"name": domain, "certificateManagement": map[string]any{"@type": "Manual"},
					"dkimManagement": map[string]any{"@type": "Manual"},
					"dnsManagement":  map[string]any{"@type": "Manual"},
					"subAddressing":  map[string]any{"@type": "Enabled"},
				}},
			})
		if err != nil {
			return err
		}
		var created struct {
			Created map[string]struct {
				ID string `json:"id"`
			} `json:"created"`
		}
		if err := json.Unmarshal(createdJSON, &created); err != nil {
			return err
		}
		domainID = created.Created["#sendwise-dsn-domain"].ID
		if domainID == "" {
			return fmt.Errorf("local Stalwart did not create the DSN test domain")
		}
	} else {
		if _, err := callLocalStalwartManagement(ctx, client, baseURL, adminUsername,
			adminPassword, accountID, "x:Domain/set", map[string]any{
				"update": map[string]any{domainID: map[string]any{
					"subAddressing": map[string]any{"@type": "Enabled"},
				}},
			}); err != nil {
			return err
		}
	}
	accountsJSON, err := callLocalStalwartManagement(ctx, client, baseURL, adminUsername,
		adminPassword, accountID, "x:Account/get", map[string]any{"ids": nil})
	if err != nil {
		return err
	}
	var accounts struct {
		List []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			DomainID string `json:"domainId"`
			Type     string `json:"@type"`
		} `json:"list"`
	}
	if err := json.Unmarshal(accountsJSON, &accounts); err != nil {
		return err
	}
	accountObject := map[string]any{
		"@type": "User", "name": "bounces", "domainId": domainID,
		"credentials": map[string]any{"0": map[string]any{
			"@type": "Password", "secret": mailboxPassword,
		}},
	}
	for _, entry := range accounts.List {
		if entry.Name == "bounces" && entry.DomainID == domainID && entry.Type == "User" {
			_, err := callLocalStalwartManagement(ctx, client, baseURL, adminUsername,
				adminPassword, accountID, "x:Account/set", map[string]any{
					"update": map[string]any{entry.ID: accountObject},
				})
			return err
		}
	}
	_, err = callLocalStalwartManagement(ctx, client, baseURL, adminUsername,
		adminPassword, accountID, "x:Account/set", map[string]any{
			"create": map[string]any{"#sendwise-dsn-bounces": accountObject},
		})
	return err
}

func callLocalStalwartManagement(
	ctx context.Context,
	client *http.Client,
	baseURL, username, password, accountID, method string,
	args map[string]any,
) (json.RawMessage, error) {
	args["accountId"] = accountID
	body, err := json.Marshal(map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:stalwart:jmap"},
		"methodCalls": [][]any{{method, args, "sendwise"}},
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/jmap/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(username, password)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local Stalwart management request returned HTTP %d", response.StatusCode)
	}
	var result struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.MethodResponses) == 0 || len(result.MethodResponses[0]) < 2 ||
		string(result.MethodResponses[0][0]) == `"error"` {
		return nil, fmt.Errorf("local Stalwart rejected a management operation")
	}
	return result.MethodResponses[0][1], nil
}

func waitForLocalStalwartDSN(
	ctx context.Context,
	client *http.Client,
	baseURL, username, password, expectedReturnPath string,
) ([]byte, delivery.Feedback, error) {
	var lastErr error
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := findLocalStalwartDSN(ctx, client, baseURL, username, password, expectedReturnPath)
		if err == nil {
			feedback, parseErr := delivery.ParseFeedbackMessage(bytes.NewReader(raw))
			if parseErr == nil {
				return raw, feedback, nil
			}
			lastErr = parseErr
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, delivery.Feedback{}, fmt.Errorf("timed out waiting for local Stalwart DSN: %w", lastErr)
		case <-time.After(750 * time.Millisecond):
		}
	}
	return nil, delivery.Feedback{}, fmt.Errorf("timed out waiting for local Stalwart DSN: %w", lastErr)
}

func findLocalStalwartDSN(
	ctx context.Context,
	client *http.Client,
	baseURL, username, password, expectedReturnPath string,
) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/jmap/session", nil)
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local Stalwart mailbox session returned HTTP %d", response.StatusCode)
	}
	var session struct {
		APIURL          string            `json:"apiUrl"`
		DownloadURL     string            `json:"downloadUrl"`
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		return nil, err
	}
	accountID := session.PrimaryAccounts["urn:ietf:params:jmap:mail"]
	if accountID == "" {
		return nil, fmt.Errorf("local Stalwart bounce mailbox has no JMAP mail account")
	}
	apiURL, err := localStalwartURL(baseURL, session.APIURL)
	if err != nil {
		return nil, err
	}
	call := func(method string, args map[string]any) (json.RawMessage, error) {
		args["accountId"] = accountID
		body, err := json.Marshal(map[string]any{
			"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
			"methodCalls": [][]any{{method, args, "sendwise"}},
		})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
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
			return nil, fmt.Errorf("local Stalwart JMAP request returned HTTP %d", resp.StatusCode)
		}
		var result struct {
			MethodResponses [][]json.RawMessage `json:"methodResponses"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}
		if len(result.MethodResponses) == 0 || len(result.MethodResponses[0]) < 2 ||
			string(result.MethodResponses[0][0]) == `"error"` {
			return nil, fmt.Errorf("local Stalwart rejected a mailbox query")
		}
		return result.MethodResponses[0][1], nil
	}
	mailboxesJSON, err := call("Mailbox/get", map[string]any{"ids": nil})
	if err != nil {
		return nil, err
	}
	var mailboxes struct {
		List []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"list"`
	}
	if err := json.Unmarshal(mailboxesJSON, &mailboxes); err != nil {
		return nil, err
	}
	inboxID := ""
	for _, mailbox := range mailboxes.List {
		if mailbox.Role == "inbox" {
			inboxID = mailbox.ID
			break
		}
	}
	if inboxID == "" {
		return nil, fmt.Errorf("local Stalwart bounce mailbox has no inbox")
	}
	queryJSON, err := call("Email/query", map[string]any{
		"filter": map[string]any{"inMailbox": inboxID},
		"sort":   []map[string]any{{"property": "receivedAt", "isAscending": false}},
		"limit":  50,
	})
	if err != nil {
		return nil, err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(queryJSON, &query); err != nil {
		return nil, err
	}
	if len(query.IDs) == 0 {
		return nil, fmt.Errorf("local Stalwart bounce inbox is empty")
	}
	emailsJSON, err := call("Email/get", map[string]any{
		"ids": query.IDs, "properties": []string{"blobId", "subject"},
	})
	if err != nil {
		return nil, err
	}
	var emails struct {
		List []struct {
			BlobID  string `json:"blobId"`
			Subject string `json:"subject"`
		} `json:"list"`
	}
	if err := json.Unmarshal(emailsJSON, &emails); err != nil {
		return nil, err
	}
	observedReports := make([]string, 0, len(emails.List))
	for _, email := range emails.List {
		if email.BlobID == "" {
			continue
		}
		downloadTemplate := strings.NewReplacer(
			"{accountId}", url.PathEscape(accountID),
			"{blobId}", url.PathEscape(email.BlobID),
			"{name}", url.PathEscape("dsn.eml"),
			"{type}", url.QueryEscape("message/rfc822"),
		).Replace(session.DownloadURL)
		downloadURL, err := localStalwartURL(baseURL, downloadTemplate)
		if err != nil {
			return nil, err
		}
		downloadRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
		if err != nil {
			return nil, err
		}
		downloadRequest.SetBasicAuth(username, password)
		downloadResponse, err := client.Do(downloadRequest)
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(downloadResponse.Body, 2<<20+1))
		downloadResponse.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if downloadResponse.StatusCode != http.StatusOK {
			continue
		}
		if len(raw) > 2<<20 {
			continue
		}
		feedback, parseErr := delivery.ParseFeedbackMessage(bytes.NewReader(raw))
		if parseErr != nil {
			if len(observedReports) < 5 {
				observedReports = append(observedReports, fmt.Sprintf("%q: parse error", email.Subject))
			}
			continue
		}
		_, returnDomain, _ := strings.Cut(feedback.ReturnPath, "@")
		report := fmt.Sprintf("%q: type=%s recipient=%s return-domain=%s matches=%t",
			email.Subject, feedback.Type, feedback.Recipient, returnDomain,
			strings.EqualFold(feedback.ReturnPath, expectedReturnPath))
		if len(observedReports) < 5 {
			observedReports = append(observedReports, report)
		}
		if strings.EqualFold(feedback.ReturnPath, expectedReturnPath) {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("local Stalwart DSN for the expected VERP address is not present; observed reports: %q",
		observedReports)
}

func localStalwartURL(baseURL, raw string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	result, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if result.IsAbs() {
		result.Scheme, result.Host = base.Scheme, base.Host
		return result.String(), nil
	}
	return base.ResolveReference(result).String(), nil
}

func stalwartEnvOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
