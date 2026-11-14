package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

var (
	ErrAdminDomainConflict      = errors.New("admin sending domain has an ownership conflict")
	ErrAdminWorkspaceClosed     = errors.New("admin workspace is closed")
	ErrAdminWorkspaceConflict   = errors.New("admin workspace status changed")
	ErrAdminCampaignNotFound    = errors.New("admin campaign not found")
	ErrAdminCampaignConflict    = errors.New("admin campaign cannot be stopped in its current state")
	ErrAdminSuppressionConflict = errors.New("admin suppression already exists")
)

type AdminSendingDomain struct {
	ID                    string             `json:"id"`
	WorkspaceID           string             `json:"workspace_id"`
	WorkspaceName         string             `json:"workspace_name"`
	WorkspaceSlug         string             `json:"workspace_slug"`
	Domain                string             `json:"domain"`
	Status                string             `json:"status"`
	SPFStatus             string             `json:"spf_status"`
	DKIMStatus            string             `json:"dkim_status"`
	DMARCStatus           string             `json:"dmarc_status"`
	VerificationStatus    string             `json:"verification_status"`
	OwnerConflict         bool               `json:"owner_conflict"`
	StalwartAuthenticated bool               `json:"stalwart_authenticated"`
	TransactionalEnabled  bool               `json:"transactional_enabled"`
	AdminDisabledAt       *time.Time         `json:"admin_disabled_at"`
	LastCheckedAt         *time.Time         `json:"last_checked_at"`
	CreatedAt             time.Time          `json:"created_at"`
	DNSCheckDetails       json.RawMessage    `json:"dns_check_details,omitempty"`
	QuotaUsage            map[string]float64 `json:"quota_usage,omitempty"`
}

type AdminCampaign struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	WorkspaceName  string     `json:"workspace_name"`
	WorkspaceSlug  string     `json:"workspace_slug"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	RecipientCount int        `json:"recipient_count"`
	SubmittedCount int        `json:"submitted_count"`
	BouncedCount   int        `json:"bounced_count"`
	ComplaintCount int        `json:"complaint_count"`
	FailedCount    int        `json:"failed_count"`
	ScheduledAt    *time.Time `json:"scheduled_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

type AdminTransactionalMessage struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	WorkspaceID string    `json:"workspace_id"`
	Workspace   string    `json:"workspace_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type AdminOperationalMetrics struct {
	DomainCount      int       `json:"domain_count"`
	CampaignCount    int       `json:"campaign_count"`
	MessagesToday    int       `json:"messages_today"`
	Messages7Days    int       `json:"messages_7_days"`
	Messages30Days   int       `json:"messages_30_days"`
	DeliveryRate30d  float64   `json:"delivery_rate_30_days"`
	BounceRate30d    float64   `json:"bounce_rate_30_days"`
	ComplaintRate30d float64   `json:"complaint_rate_30_days"`
	QueueDepth       int       `json:"queue_depth"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type AdminSuppression struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceName string    `json:"workspace_name"`
	WorkspaceSlug string    `json:"workspace_slug"`
	Email         string    `json:"email"`
	Reason        string    `json:"reason"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
}

type LegacyAPIAdminClient interface {
	AdminDomains(ctx context.Context, search, status string, nearLimit bool, limit, offset int) ([]AdminSendingDomain, int, error)
	AdminDomain(ctx context.Context, domainID string) (AdminSendingDomain, error)
	RecheckAdminDomain(ctx context.Context, domainID string) (AdminSendingDomain, error)
	SetAdminDomainDisabled(ctx context.Context, domainID string, disabled bool) (AdminSendingDomain, error)
	DeleteAdminDomain(ctx context.Context, domainID string) error
	ResolveAdminDomainConflict(ctx context.Context, domainID, rightfulDomainID string) (AdminSendingDomain, error)
	AdminWorkspaceStatus(ctx context.Context, workspaceID string) (string, error)
	SetAdminWorkspaceStatus(ctx context.Context, workspaceID, expectedStatus, status string) (string, error)
	AdminCampaigns(ctx context.Context, search, status, workspaceID string, limit, offset int) ([]AdminCampaign, int, error)
	StopAdminCampaign(ctx context.Context, campaignID string) error
	SearchAdminTransactionalMessages(ctx context.Context, search string, limit, offset int) ([]AdminTransactionalMessage, int, error)
	AdminOperationalMetrics(ctx context.Context) (AdminOperationalMetrics, error)
	AdminDomainQuotas(ctx context.Context, domainID string) (json.RawMessage, error)
	SetAdminDomainQuotas(ctx context.Context, domainID, updatedBy, reason string, streams json.RawMessage) (json.RawMessage, error)
	AdminQuotaDefaults(ctx context.Context) (json.RawMessage, error)
	SetAdminQuotaDefaults(ctx context.Context, updatedBy, reason string, defaults json.RawMessage) (json.RawMessage, error)
	AdminQuotasNearLimit(ctx context.Context) (json.RawMessage, error)
	AdminSuppressions(ctx context.Context, search, workspaceID string, limit, offset int) ([]AdminSuppression, int, error)
	CreateAdminSuppression(ctx context.Context, workspaceID, email string) (AdminSuppression, error)
	DeleteAdminSuppression(ctx context.Context, suppressionID string) error
}

func (c *httpLegacyAPIAdminClient) adminQuotaRequest(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build legacy API admin quota request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request legacy API admin quota endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("legacy API admin quota endpoint returned %d", response.StatusCode)
	}
	var result struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode legacy API admin quota response: %w", err)
	}
	return result.Data, nil
}

func (c *httpLegacyAPIAdminClient) AdminDomainQuotas(ctx context.Context, domainID string) (json.RawMessage, error) {
	return c.adminQuotaRequest(ctx, http.MethodGet,
		"/internal/admin/domains/"+url.PathEscape(domainID)+"/quotas", nil)
}

func (c *httpLegacyAPIAdminClient) SetAdminDomainQuotas(
	ctx context.Context,
	domainID, updatedBy, reason string,
	streams json.RawMessage,
) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"streams": streams, "updated_by": updatedBy, "reason": reason,
	})
	if err != nil {
		return nil, fmt.Errorf("encode legacy API domain quota update: %w", err)
	}
	return c.adminQuotaRequest(ctx, http.MethodPut,
		"/internal/admin/domains/"+url.PathEscape(domainID)+"/quotas", body)
}

func (c *httpLegacyAPIAdminClient) AdminQuotaDefaults(ctx context.Context) (json.RawMessage, error) {
	return c.adminQuotaRequest(ctx, http.MethodGet, "/internal/admin/quota-defaults", nil)
}

func (c *httpLegacyAPIAdminClient) SetAdminQuotaDefaults(
	ctx context.Context,
	updatedBy, reason string,
	defaults json.RawMessage,
) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"defaults": defaults, "updated_by": updatedBy, "reason": reason,
	})
	if err != nil {
		return nil, fmt.Errorf("encode legacy API quota defaults update: %w", err)
	}
	return c.adminQuotaRequest(ctx, http.MethodPut, "/internal/admin/quota-defaults", body)
}

func (c *httpLegacyAPIAdminClient) AdminQuotasNearLimit(ctx context.Context) (json.RawMessage, error) {
	return c.adminQuotaRequest(ctx, http.MethodGet, "/internal/admin/quotas/near-limit", nil)
}

type httpLegacyAPIAdminClient struct {
	baseURL      string
	serviceToken string
	http         *http.Client
}

func NewHTTPLegacyAPIAdminClient(baseURL, serviceToken string) LegacyAPIAdminClient {
	return &httpLegacyAPIAdminClient{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		http:         &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *httpLegacyAPIAdminClient) AdminDomains(ctx context.Context, search, status string, nearLimit bool, limit, offset int) ([]AdminSendingDomain, int, error) {
	path := fmt.Sprintf("/internal/admin/domains?search=%s&status=%s&limit=%d&offset=%d",
		url.QueryEscape(search), url.QueryEscape(status), limit, offset)
	if nearLimit {
		path += "&near_limit=true"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build legacy API admin domain request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request legacy API admin domains: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("legacy API admin domains returned %d", response.StatusCode)
	}
	var result struct {
		Data       []AdminSendingDomain `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy API admin domains: %w", err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("decode legacy API admin domains: %w", err)
	}
	return result.Data, result.Pagination.Total, nil
}

func (c *httpLegacyAPIAdminClient) AdminDomain(ctx context.Context, domainID string) (AdminSendingDomain, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/internal/admin/domains/"+url.PathEscape(domainID), nil)
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("build legacy API admin domain request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("request legacy API admin domain: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return AdminSendingDomain{}, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return AdminSendingDomain{}, fmt.Errorf("legacy API admin domain returned %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("read legacy API admin domain: %w", err)
	}
	var result struct {
		Data AdminSendingDomain `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return AdminSendingDomain{}, fmt.Errorf("decode legacy API admin domain: %w", err)
	}
	return result.Data, nil
}

func (c *httpLegacyAPIAdminClient) RecheckAdminDomain(ctx context.Context, domainID string) (AdminSendingDomain, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/admin/domains/"+url.PathEscape(domainID)+"/verify", nil)
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("build legacy API admin domain re-check request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("request legacy API admin domain re-check: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return AdminSendingDomain{}, ErrNotFound
	}
	if response.StatusCode == http.StatusConflict {
		return AdminSendingDomain{}, ErrAdminDomainConflict
	}
	if response.StatusCode != http.StatusOK {
		return AdminSendingDomain{}, fmt.Errorf("legacy API admin domain re-check returned %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("read legacy API admin domain re-check: %w", err)
	}
	var result struct {
		Domain AdminSendingDomain `json:"domain"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return AdminSendingDomain{}, fmt.Errorf("decode legacy API admin domain re-check: %w", err)
	}
	return result.Domain, nil
}

func (c *httpLegacyAPIAdminClient) SetAdminDomainDisabled(ctx context.Context, domainID string, disabled bool) (AdminSendingDomain, error) {
	body, err := json.Marshal(map[string]bool{"disabled": disabled})
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("encode legacy API admin domain status request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		c.baseURL+"/internal/admin/domains/"+url.PathEscape(domainID)+"/disabled", bytes.NewReader(body))
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("build legacy API admin domain status request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("request legacy API admin domain status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return AdminSendingDomain{}, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return AdminSendingDomain{}, fmt.Errorf("legacy API admin domain status returned %d", response.StatusCode)
	}
	var result struct {
		Data AdminSendingDomain `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return AdminSendingDomain{}, fmt.Errorf("decode legacy API admin domain status: %w", err)
	}
	return result.Data, nil
}

func (c *httpLegacyAPIAdminClient) DeleteAdminDomain(ctx context.Context, domainID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.baseURL+"/internal/admin/domains/"+url.PathEscape(domainID), nil)
	if err != nil {
		return fmt.Errorf("build legacy API admin domain removal request: %w", err)
	}
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request legacy API admin domain removal: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode == http.StatusConflict {
		return ErrAdminDomainConflict
	}
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("legacy API admin domain removal returned %d", response.StatusCode)
	}
	return nil
}

func (c *httpLegacyAPIAdminClient) ResolveAdminDomainConflict(ctx context.Context, domainID, rightfulDomainID string) (AdminSendingDomain, error) {
	body, err := json.Marshal(map[string]string{"rightful_domain_id": rightfulDomainID})
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("encode admin domain conflict resolution: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/admin/domains/"+url.PathEscape(domainID)+"/resolve-conflict", bytes.NewReader(body))
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("build admin domain conflict resolution request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return AdminSendingDomain{}, fmt.Errorf("request admin domain conflict resolution: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return AdminSendingDomain{}, ErrNotFound
	}
	if response.StatusCode == http.StatusConflict {
		return AdminSendingDomain{}, ErrAdminDomainConflict
	}
	if response.StatusCode != http.StatusOK {
		return AdminSendingDomain{}, fmt.Errorf("admin domain conflict resolution returned %d", response.StatusCode)
	}
	var result struct {
		Data AdminSendingDomain `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return AdminSendingDomain{}, fmt.Errorf("decode admin domain conflict resolution: %w", err)
	}
	return result.Data, nil
}

func (c *httpLegacyAPIAdminClient) AdminWorkspaceStatus(ctx context.Context, workspaceID string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/internal/admin/workspaces/"+url.PathEscape(workspaceID)+"/status", nil)
	if err != nil {
		return "", fmt.Errorf("build legacy API admin workspace status request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("request legacy API admin workspace status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("legacy API admin workspace status returned %d", response.StatusCode)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode legacy API admin workspace status: %w", err)
	}
	return result.Status, nil
}

func (c *httpLegacyAPIAdminClient) SetAdminWorkspaceStatus(ctx context.Context, workspaceID, expectedStatus, status string) (string, error) {
	body, err := json.Marshal(map[string]string{"expected_status": expectedStatus, "status": status})
	if err != nil {
		return "", fmt.Errorf("encode legacy API admin workspace status request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		c.baseURL+"/internal/admin/workspaces/"+url.PathEscape(workspaceID)+"/status", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build legacy API admin workspace status request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("request legacy API admin workspace status update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if response.StatusCode == http.StatusConflict {
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return "", fmt.Errorf("read legacy API admin workspace conflict: %w", err)
		}
		if strings.Contains(string(body), "workspace_closed") {
			return "", ErrAdminWorkspaceClosed
		}
		return "", ErrAdminWorkspaceConflict
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("legacy API admin workspace status update returned %d", response.StatusCode)
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("decode legacy API admin workspace status update: %w", err)
	}
	return result.Status, nil
}

func (c *httpLegacyAPIAdminClient) AdminCampaigns(ctx context.Context, search, status, workspaceID string, limit, offset int) ([]AdminCampaign, int, error) {
	path := fmt.Sprintf("/internal/admin/campaigns?search=%s&status=%s&workspace_id=%s&limit=%d&offset=%d",
		url.QueryEscape(search), url.QueryEscape(status), url.QueryEscape(workspaceID), limit, offset)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build legacy API admin campaigns request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request legacy API admin campaigns: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("legacy API admin campaigns returned %d", response.StatusCode)
	}
	var result struct {
		Data       []AdminCampaign `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy API admin campaigns: %w", err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("decode legacy API admin campaigns: %w", err)
	}
	return result.Data, result.Pagination.Total, nil
}

func (c *httpLegacyAPIAdminClient) StopAdminCampaign(ctx context.Context, campaignID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/admin/campaigns/"+url.PathEscape(campaignID)+"/stop", nil)
	if err != nil {
		return fmt.Errorf("build legacy API admin campaign stop request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request legacy API admin campaign stop: %w", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrAdminCampaignNotFound
	case http.StatusConflict:
		return ErrAdminCampaignConflict
	default:
		return fmt.Errorf("legacy API admin campaign stop returned %d", response.StatusCode)
	}
}

func (c *httpLegacyAPIAdminClient) SearchAdminTransactionalMessages(ctx context.Context, search string, limit, offset int) ([]AdminTransactionalMessage, int, error) {
	path := fmt.Sprintf("/internal/admin/transactional-messages?search=%s&limit=%d&offset=%d",
		url.QueryEscape(search), limit, offset)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build legacy API admin transactional message search: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request legacy API admin transactional message search: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("legacy API admin transactional message search returned %d", response.StatusCode)
	}
	var result struct {
		Data       []AdminTransactionalMessage `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy API admin transactional message search: %w", err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("decode legacy API admin transactional message search: %w", err)
	}
	return result.Data, result.Pagination.Total, nil
}

func (c *httpLegacyAPIAdminClient) AdminOperationalMetrics(ctx context.Context) (AdminOperationalMetrics, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/admin/overview", nil)
	if err != nil {
		return AdminOperationalMetrics{}, fmt.Errorf("build legacy API admin overview request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return AdminOperationalMetrics{}, fmt.Errorf("request legacy API admin overview: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return AdminOperationalMetrics{}, fmt.Errorf("legacy API admin overview returned %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return AdminOperationalMetrics{}, fmt.Errorf("read legacy API admin overview: %w", err)
	}
	var result struct {
		Data AdminOperationalMetrics `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return AdminOperationalMetrics{}, fmt.Errorf("decode legacy API admin overview: %w", err)
	}
	return result.Data, nil
}

func (c *httpLegacyAPIAdminClient) AdminSuppressions(ctx context.Context, search, workspaceID string, limit, offset int) ([]AdminSuppression, int, error) {
	query := url.Values{
		"search":       []string{search},
		"workspace_id": []string{workspaceID},
		"limit":        []string{fmt.Sprint(limit)},
		"offset":       []string{fmt.Sprint(offset)},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/internal/admin/suppressions?"+query.Encode(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build legacy API admin suppressions request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("request legacy API admin suppressions: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("legacy API admin suppressions returned %d", response.StatusCode)
	}
	var result struct {
		Data       []AdminSuppression `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy API admin suppressions: %w", err)
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, fmt.Errorf("decode legacy API admin suppressions: %w", err)
	}
	return result.Data, result.Pagination.Total, nil
}

func (c *httpLegacyAPIAdminClient) CreateAdminSuppression(ctx context.Context, workspaceID, email string) (AdminSuppression, error) {
	body, err := json.Marshal(map[string]string{"workspace_id": workspaceID, "email": email})
	if err != nil {
		return AdminSuppression{}, fmt.Errorf("encode legacy API admin suppression request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/admin/suppressions", bytes.NewReader(body))
	if err != nil {
		return AdminSuppression{}, fmt.Errorf("build legacy API admin suppression request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return AdminSuppression{}, fmt.Errorf("request legacy API admin suppression: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return AdminSuppression{}, ErrAdminSuppressionConflict
	}
	if response.StatusCode != http.StatusCreated {
		return AdminSuppression{}, fmt.Errorf("legacy API admin suppression returned %d", response.StatusCode)
	}
	var result struct {
		Data AdminSuppression `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return AdminSuppression{}, fmt.Errorf("decode legacy API admin suppression: %w", err)
	}
	return result.Data, nil
}

func (c *httpLegacyAPIAdminClient) DeleteAdminSuppression(ctx context.Context, suppressionID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.baseURL+"/internal/admin/suppressions/"+url.PathEscape(suppressionID), nil)
	if err != nil {
		return fmt.Errorf("build legacy API admin suppression deletion: %w", err)
	}
	kit.WithServiceToken(request, c.serviceToken)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request legacy API admin suppression deletion: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("legacy API admin suppression deletion returned %d", response.StatusCode)
	}
	return nil
}
