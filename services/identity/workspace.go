package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WorkspaceMembership is a user's membership in one organization.
type WorkspaceMembership struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Role           string `json:"role"`
}

// WorkspaceOrganization is a tenant profile owned by the Workspace service.
type WorkspaceOrganization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// WorkspaceClient is the identity→workspace synchronous contract. It is used
// only for session minting; authoritative membership state stays in Workspace.
type WorkspaceClient interface {
	DefaultMembership(ctx context.Context, userID string) (*WorkspaceMembership, error)
	Organization(ctx context.Context, organizationID string) (*WorkspaceOrganization, error)
}

// ErrWorkspaceUnavailable indicates the workspace service could not be reached.
var ErrWorkspaceUnavailable = errors.New("workspace service unavailable")

type httpWorkspaceClient struct {
	baseURL      string
	serviceToken string
	http         *http.Client
}

// NewHTTPWorkspaceClient builds a service-to-service client for Workspace.
func NewHTTPWorkspaceClient(baseURL, serviceToken string) WorkspaceClient {
	return &httpWorkspaceClient{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		http:         &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *httpWorkspaceClient) DefaultMembership(ctx context.Context, userID string) (*WorkspaceMembership, error) {
	var resp struct {
		Memberships []WorkspaceMembership `json:"memberships"`
	}
	if err := c.get(ctx, "/internal/users/"+userID+"/memberships", &resp); err != nil {
		return nil, err
	}
	if len(resp.Memberships) == 0 {
		return nil, ErrNoOrganization
	}
	return &resp.Memberships[0], nil
}

func (c *httpWorkspaceClient) Organization(ctx context.Context, organizationID string) (*WorkspaceOrganization, error) {
	var org WorkspaceOrganization
	if err := c.get(ctx, "/internal/organizations/"+organizationID, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

func (c *httpWorkspaceClient) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build workspace request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.serviceToken != "" {
		req.Header.Set("X-Service-Token", c.serviceToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWorkspaceUnavailable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fmt.Errorf("%w: read response: %v", ErrWorkspaceUnavailable, err)
		}
		if err := json.Unmarshal(body, dst); err != nil {
			return fmt.Errorf("%w: decode response: %v", ErrWorkspaceUnavailable, err)
		}
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusServiceUnavailable:
		return fmt.Errorf("%w: workspace returned %d", ErrWorkspaceUnavailable, resp.StatusCode)
	default:
		return fmt.Errorf("%w: workspace returned %d", ErrWorkspaceUnavailable, resp.StatusCode)
	}
}
