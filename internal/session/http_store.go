package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Store checks whether a user's session is still active.
type Store interface {
	Active(context.Context, string, string) (bool, error)
}

// HTTPStore delegates session checks to the Identity service.
type HTTPStore struct {
	baseURL      string
	serviceToken string
	client       *http.Client
}

func NewHTTPStore(baseURL, serviceToken string) *HTTPStore {
	return &HTTPStore{
		baseURL:      strings.TrimRight(baseURL, "/"),
		serviceToken: serviceToken,
		client:       &http.Client{Timeout: 3 * time.Second},
	}
}

func (s *HTTPStore) Active(ctx context.Context, userID, sessionID string) (bool, error) {
	if s.baseURL == "" || s.serviceToken == "" || userID == "" || sessionID == "" {
		return false, fmt.Errorf("session check is not configured")
	}
	endpoint := s.baseURL + "/internal/sessions/" + url.PathEscape(sessionID) +
		"?user_id=" + url.QueryEscape(userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("build session check request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Service-Token", s.serviceToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("request session status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("identity session check returned %d", resp.StatusCode)
	}
	var result struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode session status: %w", err)
	}
	return result.Active, nil
}
