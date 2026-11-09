package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/config"
)

type stalwartDomainHoster interface {
	EnsureDomain(context.Context, string) error
}

type stalwartDomainProvisionFunc func(context.Context, string) error

func (f stalwartDomainProvisionFunc) EnsureDomain(ctx context.Context, domain string) error {
	return f(ctx, domain)
}

type stalwartAdminClient struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

func ensureStalwartDomain(ctx context.Context, cfg config.Config, domain string) error {
	if cfg.StalwartManagementURL == "" || cfg.StalwartAdminUsername == "" || cfg.StalwartAdminPassword == "" {
		return fmt.Errorf("Stalwart management API credentials are not configured")
	}
	client := stalwartAdminClient{
		baseURL:  strings.TrimRight(cfg.StalwartManagementURL, "/"),
		username: cfg.StalwartAdminUsername,
		password: cfg.StalwartAdminPassword,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	return client.EnsureDomain(ctx, domain)
}

func (c stalwartAdminClient) EnsureDomain(ctx context.Context, domain string) error {
	session, err := c.request(ctx, http.MethodGet, "/jmap/session", nil)
	if err != nil {
		return fmt.Errorf("load Stalwart management session: %w", err)
	}
	var sessionData struct {
		PrimaryAccounts map[string]string `json:"primaryAccounts"`
	}
	if err := json.Unmarshal(session, &sessionData); err != nil {
		return fmt.Errorf("decode Stalwart management session: %w", err)
	}
	accountID := sessionData.PrimaryAccounts["urn:stalwart:jmap"]
	if accountID == "" {
		return fmt.Errorf("Stalwart management session has no configuration account")
	}

	result, err := c.call(ctx, accountID, "x:Domain/get", map[string]any{"ids": nil})
	if err != nil {
		return err
	}
	var domains struct {
		List []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(result, &domains); err != nil {
		return fmt.Errorf("decode Stalwart domains: %w", err)
	}
	domainID := ""
	for _, configured := range domains.List {
		if strings.EqualFold(configured.Name, domain) {
			if configured.ID == "" {
				return fmt.Errorf("Stalwart returned no ID for hosted domain %q", domain)
			}
			domainID = configured.ID
		}
	}
	if domainID == "" {
		result, err = c.call(ctx, accountID, "x:Domain/set", map[string]any{
			"create": map[string]any{
				"#sendwise-domain": map[string]any{
					"name":                  domain,
					"certificateManagement": map[string]any{"@type": "Manual"},
					"dkimManagement":        map[string]any{"@type": "Manual"},
					"dnsManagement":         map[string]any{"@type": "Manual"},
					"subAddressing":         map[string]any{"@type": "Enabled"},
				},
			},
		})
		if err != nil {
			return err
		}
		var created struct {
			Created map[string]struct {
				ID string `json:"id"`
			} `json:"created"`
			NotCreated map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"notCreated"`
		}
		if err := json.Unmarshal(result, &created); err != nil {
			return fmt.Errorf("decode Stalwart domain creation: %w", err)
		}
		if failure, ok := created.NotCreated["#sendwise-domain"]; ok {
			detail := failure.Description
			if detail == "" {
				detail = failure.Type
			}
			return fmt.Errorf("Stalwart rejected domain %q (%s)", domain, detail)
		}
		createdDomain, ok := created.Created["#sendwise-domain"]
		if !ok || createdDomain.ID == "" {
			return fmt.Errorf("Stalwart did not confirm creation of domain %q", domain)
		}
		domainID = createdDomain.ID
		return nil
	}

	result, err = c.call(ctx, accountID, "x:Domain/set", map[string]any{
		"update": map[string]any{
			domainID: map[string]any{
				"dkimManagement": map[string]any{"@type": "Manual"},
			},
		},
	})
	if err != nil {
		return err
	}
	var updated struct {
		Updated    map[string]json.RawMessage `json:"updated"`
		NotUpdated map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"notUpdated"`
	}
	if err := json.Unmarshal(result, &updated); err != nil {
		return fmt.Errorf("decode Stalwart domain update: %w", err)
	}
	if failure, ok := updated.NotUpdated[domainID]; ok {
		detail := failure.Description
		if detail == "" {
			detail = failure.Type
		}
		return fmt.Errorf("Stalwart could not enforce app-managed DKIM for domain %q (%s)", domain, detail)
	}
	if _, ok := updated.Updated[domainID]; !ok {
		return fmt.Errorf("Stalwart did not confirm app-managed DKIM for domain %q", domain)
	}
	return nil
}

func (c stalwartAdminClient) call(
	ctx context.Context,
	accountID string,
	method string,
	args map[string]any,
) (json.RawMessage, error) {
	args["accountId"] = accountID
	body, err := json.Marshal(map[string]any{
		"using": []string{"urn:ietf:params:jmap:core", "urn:stalwart:jmap"},
		"methodCalls": [][]any{
			{method, args, "sendwise"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode Stalwart %s request: %w", method, err)
	}
	response, err := c.request(ctx, http.MethodPost, "/jmap/", body)
	if err != nil {
		return nil, fmt.Errorf("call Stalwart %s: %w", method, err)
	}
	var envelope struct {
		MethodResponses [][]json.RawMessage `json:"methodResponses"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return nil, fmt.Errorf("decode Stalwart %s response: %w", method, err)
	}
	if len(envelope.MethodResponses) == 0 || len(envelope.MethodResponses[0]) < 2 {
		return nil, fmt.Errorf("Stalwart %s returned no method response", method)
	}
	var responseMethod string
	if err := json.Unmarshal(envelope.MethodResponses[0][0], &responseMethod); err != nil {
		return nil, fmt.Errorf("decode Stalwart %s response method: %w", method, err)
	}
	result := envelope.MethodResponses[0][1]
	if responseMethod == "error" {
		var failure struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(result, &failure); err != nil {
			return nil, fmt.Errorf("Stalwart %s returned an error", method)
		}
		return nil, fmt.Errorf("Stalwart %s failed (%s)", method, failure.Type)
	}
	if responseMethod != method {
		return nil, fmt.Errorf("Stalwart returned %q for %s", responseMethod, method)
	}
	return result, nil
}

func (c stalwartAdminClient) request(
	ctx context.Context,
	method string,
	path string,
	body []byte,
) (json.RawMessage, error) {
	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Stalwart returned HTTP %d", resp.StatusCode)
	}
	return responseBody, nil
}
