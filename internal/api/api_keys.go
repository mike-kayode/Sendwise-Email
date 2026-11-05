package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/httpx"
)

type apiKeyInput struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

type apiKeyResponse struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organization_id"`
	Name           string   `json:"name"`
	KeyPrefix      string   `json:"key_prefix"`
	Scopes         []string `json:"scopes"`
	LastUsedAt     *string  `json:"last_used_at,omitempty"`
	ExpiresAt      *string  `json:"expires_at,omitempty"`
	RevokedAt      *string  `json:"revoked_at,omitempty"`
	CreatedAt      string   `json:"created_at"`
	PlainKey       string   `json:"plain_key,omitempty"`
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)

	keys, err := s.apiKeys.List(r.Context(), orgID)
	if err != nil {
		s.logger.Error("list api keys", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list API keys.")
		return
	}

	resp := make([]apiKeyResponse, 0, len(keys))
	for _, k := range keys {
		item := apiKeyResponse{
			ID:             k.ID,
			OrganizationID: k.OrganizationID,
			Name:           k.Name,
			KeyPrefix:      k.KeyPrefix,
			Scopes:         k.Scopes,
			LastUsedAt:     ptrTime(k.LastUsedAt),
			ExpiresAt:      ptrTime(k.ExpiresAt),
			RevokedAt:      ptrTime(k.RevokedAt),
			CreatedAt:      k.CreatedAt.Format(time.RFC3339),
		}
		resp = append(resp, item)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": resp})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)

	var input apiKeyInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "API key name is required.")
		return
	}
	if len(input.Scopes) == 0 {
		input.Scopes = []string{"read"}
	}

	var expiresAt *time.Time
	if input.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_expires_at", "Expiration must be RFC3339.")
			return
		}
		expiresAt = &t
	}

	key, plain, err := s.apiKeys.Create(r.Context(), orgID, input.Name, input.Scopes, expiresAt)
	if err != nil {
		s.logger.Error("create api key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create API key.")
		return
	}

	key.PlainKey = plain
	resp := apiKeyResponse{
		ID:             key.ID,
		OrganizationID: key.OrganizationID,
		Name:           key.Name,
		KeyPrefix:      key.KeyPrefix,
		Scopes:         key.Scopes,
		LastUsedAt:     ptrTime(key.LastUsedAt),
		ExpiresAt:      ptrTime(key.ExpiresAt),
		RevokedAt:      ptrTime(key.RevokedAt),
		CreatedAt:      key.CreatedAt.Format(time.RFC3339),
		PlainKey:       key.PlainKey,
	}

	httpx.JSON(w, http.StatusCreated, resp)
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	id := r.PathValue("id")

	if err := s.apiKeys.Delete(r.Context(), orgID, id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "api_key_not_found", "The API key could not be found.")
			return
		}
		s.logger.Error("delete api key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete API key.")
		return
	}

	httpx.NoContent(w)
}

func ptrTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}
