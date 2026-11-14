package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

type WorkspaceAdminOrganization struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Slug        string                 `json:"slug"`
	MemberCount int                    `json:"member_count"`
	CreatedAt   time.Time              `json:"created_at"`
	Members     []WorkspaceAdminMember `json:"members,omitempty"`
}

type WorkspaceAdminMember struct {
	OrganizationID string    `json:"organization_id"`
	UserID         string    `json:"user_id"`
	Role           string    `json:"role"`
	CreatedAt      time.Time `json:"created_at"`
}

// WorkspaceClient is the identity→workspace synchronous contract. It is used
// for session minting and authorized platform-admin metadata reads;
// authoritative membership state stays in Workspace.
type WorkspaceClient interface {
	DefaultMembership(ctx context.Context, userID string) (*WorkspaceMembership, error)
	Organization(ctx context.Context, organizationID string) (*WorkspaceOrganization, error)
	AdminOrganizations(ctx context.Context, search string, limit, offset int) ([]WorkspaceAdminOrganization, int, error)
	AdminOrganization(ctx context.Context, organizationID string) (*WorkspaceAdminOrganization, error)
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

func (c *httpWorkspaceClient) AdminOrganizations(ctx context.Context, search string, limit, offset int) ([]WorkspaceAdminOrganization, int, error) {
	var resp struct {
		Data       []WorkspaceAdminOrganization `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	path := fmt.Sprintf("/internal/admin/organizations?search=%s&limit=%d&offset=%d",
		url.QueryEscape(search), limit, offset)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Data, resp.Pagination.Total, nil
}

func (c *httpWorkspaceClient) AdminOrganization(ctx context.Context, organizationID string) (*WorkspaceAdminOrganization, error) {
	var resp struct {
		Workspace WorkspaceAdminOrganization `json:"workspace"`
		Members   []WorkspaceAdminMember     `json:"members"`
	}
	path := "/internal/admin/organizations/" + url.PathEscape(organizationID)
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	resp.Workspace.Members = resp.Members
	resp.Workspace.MemberCount = len(resp.Members)
	return &resp.Workspace, nil
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
