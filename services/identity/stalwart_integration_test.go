package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/khayode/email-platform/internal/delivery"
)

func TestStalwartAcceptsAppSignedTenantCampaign(t *testing.T) {
	if os.Getenv("RUN_STALWART_INTEGRATION") != "1" {
		t.Skip("set RUN_STALWART_INTEGRATION=1 to run against the local Stalwart container")
	}

	baseURL := strings.TrimRight(envOr("STALWART_URL", "http://127.0.0.1:8081"), "/")
	domain := os.Getenv("STALWART_DOMAIN")
	adminUsername := os.Getenv("STALWART_ADMIN_USERNAME")
	adminPassword := os.Getenv("STALWART_ADMIN_PASSWORD")
	submissionUsername := os.Getenv("STALWART_SUBMIT_USERNAME")
	submissionPassword := os.Getenv("STALWART_SUBMIT_PASSWORD")
	if domain == "" || adminUsername == "" || adminPassword == "" || submissionUsername == "" || submissionPassword == "" {
		t.Fatal("STALWART_DOMAIN, STALWART_ADMIN_USERNAME, STALWART_ADMIN_PASSWORD, STALWART_SUBMIT_USERNAME, and STALWART_SUBMIT_PASSWORD are required")
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tlsConfig, err := localStalwartTLSConfig(ctx, smtpAddress, smtpHost)
	if err != nil {
		t.Fatalf("trust local Stalwart test certificate: %v", err)
	}
	mailer.tlsConfig = tlsConfig
	if err := mailer.Probe(ctx); err != nil {
		t.Fatalf("Stalwart SMTP EHLO/STARTTLS/AUTH probe failed: %v", err)
	}
	implicitTLS := *mailer
	implicitTLS.addr = envOr("STALWART_SMTP_IMPLICIT_ADDR", "127.0.0.1:465")
	implicitTLS.tlsMode = "tls"
	if err := implicitTLS.Probe(ctx); err != nil {
		t.Fatalf("Stalwart SMTP implicit TLS/AUTH probe failed: %v", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	const workspaceDomain = "hosted-tenant.example.test"
	const workspaceSelector = "sendwise-test"
	if err := ensureStalwartTestDomain(
		ctx, client, baseURL, adminUsername, adminPassword, workspaceDomain,
	); err != nil {
		t.Fatalf("host tenant sending domain in Stalwart: %v", err)
	}
	publicKey, encryptedPrivateKey, err := delivery.GenerateDKIMKeyPair("stalwart-workspace-dkim-test-key")
	if err != nil {
		t.Fatalf("generate workspace DKIM key: %v", err)
	}
	privateKey, err := delivery.DecryptDKIMPrivateKey("stalwart-workspace-dkim-test-key", encryptedPrivateKey)
	if err != nil {
		t.Fatalf("decrypt workspace DKIM key: %v", err)
	}
	workspaceDKIM := &delivery.DKIMConfig{
		Domain: workspaceDomain, Selector: workspaceSelector, PublicKey: publicKey, PrivateKey: privateKey,
	}
	workspaceSubject := fmt.Sprintf("Workspace DKIM single-signer test %d", time.Now().UnixNano())
	smtpPool := delivery.NewSMTPPool(smtpAddress, smtpHost, sender, submissionPassword, true, 2)
	_, sendErr := smtpPool.Send(ctx, delivery.Message{
		ID:           "workspace-dkim-test@" + workspaceDomain,
		From:         "Campaign Sender <sender@" + workspaceDomain + ">",
		EnvelopeFrom: "sender@" + workspaceDomain,
		To:           sender, Subject: workspaceSubject,
		Text: "Authenticated submission from a hosted workspace domain.",
		DKIM: workspaceDKIM,
	})
	closeErr := smtpPool.Close()
	if sendErr != nil {
		t.Fatalf("submit app-signed message from a hosted workspace domain: %v", sendErr)
	}
	if closeErr != nil {
		t.Fatalf("close authenticated SMTP pool: %v", closeErr)
	}
	workspaceTXT := "v=DKIM1; k=rsa; p=" + publicKey
	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, rawMessage, err := findStalwartMessage(
			client, baseURL, sender, submissionPassword, workspaceSubject,
		)
		if err == nil {
			if err := verifySingleDKIMSignature(
				rawMessage, workspaceSelector+"._domainkey."+workspaceDomain, workspaceTXT,
			); err != nil {
				t.Fatalf("verify app-signed workspace message: %v", err)
			}
			t.Logf("verified one app DKIM signature for authenticated submission from hosted domain %s", workspaceDomain)
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("workspace DKIM message was not readable from Stalwart: %v", lastErr)
}

func ensureStalwartTestDomain(
	ctx context.Context,
	client *http.Client,
	baseURL, username, password, domain string,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/jmap/session", nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("management JMAP session returned HTTP %d", response.StatusCode)
	}
	var session struct {
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		return err
	}
	accountID := session.PrimaryAccounts["urn:stalwart:jmap"]
	if accountID == "" {
		return fmt.Errorf("management JMAP session has no configuration account")
	}
	domainsJSON, err := stalwartManagementCall(
		ctx, client, baseURL, username, password, accountID, "x:Domain/get", map[string]any{"ids": nil},
	)
	if err != nil {
		return err
	}
	var domains struct {
		List []struct {
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(domainsJSON, &domains); err != nil {
		return err
	}
	for _, configured := range domains.List {
		if strings.EqualFold(configured.Name, domain) {
			return nil
		}
	}
	createdJSON, err := stalwartManagementCall(
		ctx, client, baseURL, username, password, accountID, "x:Domain/set", map[string]any{
			"create": map[string]any{
				"#sendwise-domain": map[string]any{
					"name":                  domain,
					"certificateManagement": map[string]any{"@type": "Manual"},
					"dkimManagement":        map[string]any{"@type": "Manual"},
					"dnsManagement":         map[string]any{"@type": "Manual"},
					"subAddressing":         map[string]any{"@type": "Enabled"},
				},
			},
		},
	)
	if err != nil {
		return err
	}
	var result struct {
		Created map[string]struct {
			ID string `json:"id"`
		} `json:"created"`
		NotCreated map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"notCreated"`
	}
	if err := json.Unmarshal(createdJSON, &result); err != nil {
		return err
	}
	if failure, ok := result.NotCreated["#sendwise-domain"]; ok {
		return fmt.Errorf("Stalwart rejected tenant domain (%s)", failure.Type)
	}
	if _, ok := result.Created["#sendwise-domain"]; !ok {
		return fmt.Errorf("Stalwart did not confirm tenant domain creation")
	}
	return nil
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
	BlobID  string                 `json:"blobId"`
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

func findStalwartMessage(client *http.Client, baseURL, username, password, subject string) (stalwartEmail, []byte, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/jmap/session", nil)
	if err != nil {
		return stalwartEmail{}, nil, err
	}
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		return stalwartEmail{}, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return stalwartEmail{}, nil, fmt.Errorf("JMAP session returned HTTP %d", response.StatusCode)
	}
	var session struct {
		APIURL          string            `json:"apiUrl"`
		DownloadURL     string            `json:"downloadUrl"`
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		return stalwartEmail{}, nil, err
	}
	accountID := session.PrimaryAccounts["urn:ietf:params:jmap:mail"]
	if accountID == "" {
		return stalwartEmail{}, nil, fmt.Errorf("JMAP session has no mail account")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return stalwartEmail{}, nil, err
	}
	api, err := url.Parse(session.APIURL)
	if err != nil {
		return stalwartEmail{}, nil, err
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
		return stalwartEmail{}, nil, err
	}
	var mailboxes struct {
		List []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"list"`
	}
	if err := json.Unmarshal(mailboxesJSON, &mailboxes); err != nil {
		return stalwartEmail{}, nil, err
	}
	inboxID := ""
	for _, mailbox := range mailboxes.List {
		if mailbox.Role == "inbox" {
			inboxID = mailbox.ID
			break
		}
	}
	if inboxID == "" {
		return stalwartEmail{}, nil, fmt.Errorf("Stalwart mailbox has no inbox")
	}

	queryJSON, err := call("Email/query", map[string]any{
		"filter": map[string]any{"inMailbox": inboxID},
		"sort":   []map[string]any{{"property": "receivedAt", "isAscending": false}},
		"limit":  20,
	})
	if err != nil {
		return stalwartEmail{}, nil, err
	}
	var query struct {
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(queryJSON, &query); err != nil {
		return stalwartEmail{}, nil, err
	}
	if len(query.IDs) == 0 {
		return stalwartEmail{}, nil, fmt.Errorf("Stalwart inbox has no messages yet")
	}
	emailsJSON, err := call("Email/get", map[string]any{
		"ids":        query.IDs,
		"properties": []string{"blobId", "subject", "to"},
	})
	if err != nil {
		return stalwartEmail{}, nil, err
	}
	var emails struct {
		List []stalwartEmail `json:"list"`
	}
	if err := json.Unmarshal(emailsJSON, &emails); err != nil {
		return stalwartEmail{}, nil, err
	}
	for _, email := range emails.List {
		if email.Subject == subject && email.Recipient() == strings.ToLower(username) {
			if email.BlobID == "" || session.DownloadURL == "" {
				return stalwartEmail{}, nil, fmt.Errorf("JMAP email has no downloadable raw message")
			}
			downloadTemplate := strings.NewReplacer(
				"{accountId}", url.PathEscape(accountID),
				"{blobId}", url.PathEscape(email.BlobID),
				"{name}", url.PathEscape("message.eml"),
				"{type}", url.QueryEscape("message/rfc822"),
			).Replace(session.DownloadURL)
			downloadURL, err := url.Parse(downloadTemplate)
			if err != nil {
				return stalwartEmail{}, nil, err
			}
			if downloadURL.IsAbs() {
				downloadURL.Scheme = base.Scheme
				downloadURL.Host = base.Host
			} else {
				downloadURL = base.ResolveReference(downloadURL)
			}
			downloadRequest, err := http.NewRequest(http.MethodGet, downloadURL.String(), nil)
			if err != nil {
				return stalwartEmail{}, nil, err
			}
			downloadRequest.SetBasicAuth(username, password)
			downloadResponse, err := client.Do(downloadRequest)
			if err != nil {
				return stalwartEmail{}, nil, err
			}
			defer downloadResponse.Body.Close()
			if downloadResponse.StatusCode != http.StatusOK {
				return stalwartEmail{}, nil, fmt.Errorf("JMAP raw-message download returned HTTP %d", downloadResponse.StatusCode)
			}
			rawMessage, err := io.ReadAll(downloadResponse.Body)
			if err != nil {
				return stalwartEmail{}, nil, err
			}
			return email, rawMessage, nil
		}
	}
	return stalwartEmail{}, nil, fmt.Errorf("message with subject %q not found", subject)
}

func stalwartManagementCall(
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
		return nil, fmt.Errorf("management JMAP %s returned HTTP %d", method, response.StatusCode)
	}
	var result struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.MethodResponses) == 0 || len(result.MethodResponses[0]) < 2 {
		return nil, fmt.Errorf("management JMAP %s returned no method response", method)
	}
	if string(result.MethodResponses[0][0]) == `"error"` {
		return nil, fmt.Errorf("management JMAP %s returned an error", method)
	}
	return result.MethodResponses[0][1], nil
}

func verifySingleDKIMSignature(rawMessage []byte, expectedName, txtRecord string) error {
	message, err := mail.ReadMessage(bytes.NewReader(rawMessage))
	if err != nil {
		return fmt.Errorf("parse signed message: %w", err)
	}
	signatureCount := 0
	for name, values := range message.Header {
		if strings.EqualFold(name, "DKIM-Signature") {
			signatureCount += len(values)
		}
	}
	if signatureCount != 1 {
		return fmt.Errorf("DKIM signature count = %d, want exactly one", signatureCount)
	}
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(rawMessage), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			if !strings.EqualFold(name, expectedName) {
				return nil, fmt.Errorf("unexpected DKIM TXT lookup %q", name)
			}
			return []string{txtRecord}, nil
		},
	})
	if err != nil {
		return fmt.Errorf("verify DKIM signature: %w", err)
	}
	if len(verifications) != 1 {
		return fmt.Errorf("DKIM verification count = %d, want exactly one", len(verifications))
	}
	if verifications[0].Err != nil {
		return fmt.Errorf("DKIM verification failed: %w", verifications[0].Err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
