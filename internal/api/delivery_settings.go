package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/httpx"
)

type deliverySettings struct {
	Provider          string  `json:"provider"`
	FallbackProvider  *string `json:"fallback_provider"`
	ResendAPIKeySet   bool    `json:"resend_api_key_set"`
	ResendAPIKeyLast4 string  `json:"resend_api_key_last4,omitempty"`
}

type deliverySettingsInput struct {
	Provider         string  `json:"provider"`
	FallbackProvider *string `json:"fallback_provider"`
	ResendAPIKey     string  `json:"resend_api_key"`
}

func (s *Server) getDeliverySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readDeliverySettings(r)
	if err != nil {
		s.logger.Error("read workspace delivery settings", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load delivery settings.")
		return
	}
	httpx.JSON(w, http.StatusOK, settings)
}

func (s *Server) updateDeliverySettings(w http.ResponseWriter, r *http.Request) {
	var input deliverySettingsInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider == "" {
		provider = delivery.ProviderStalwart
	}
	if provider != delivery.ProviderStalwart {
		httpx.Error(w, http.StatusBadRequest, "campaign_delivery_stalwart_only", "Campaign delivery is Stalwart-only. Resend is disabled for workspace campaigns.")
		return
	}
	fallback := normalizeOptionalProvider(input.FallbackProvider)
	if fallback != nil && *fallback != delivery.ProviderStalwart {
		httpx.Error(w, http.StatusBadRequest, "invalid_fallback", "Campaign delivery does not allow a Resend fallback.")
		return
	}

	var encryptedKey []byte
	key := strings.TrimSpace(input.ResendAPIKey)
	if key != "" {
		httpx.Error(w, http.StatusBadRequest, "resend_disabled_for_campaigns", "Resend API keys are disabled for campaign delivery settings.")
		return
	}

	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO workspace_delivery_settings (workspace_id, provider, fallback_provider, resend_api_key_enc)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE
		SET provider = EXCLUDED.provider,
		    fallback_provider = EXCLUDED.fallback_provider,
		    resend_api_key_enc = COALESCE(EXCLUDED.resend_api_key_enc, workspace_delivery_settings.resend_api_key_enc),
		    updated_at = now()
	`, s.organizationID(r), provider, fallback, nullableEncryptedKey(encryptedKey))
	if err != nil {
		s.logger.Error("update workspace delivery settings", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save delivery settings.")
		return
	}
	settings, err := s.readDeliverySettings(r)
	if err != nil {
		s.logger.Error("read saved workspace delivery settings", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Delivery settings were saved but could not be reloaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, settings)
}

func (s *Server) testDeliveryConnection(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, http.StatusBadRequest, "campaign_delivery_stalwart_only", "Campaign delivery is Stalwart-only. Resend connection tests are disabled for workspace campaigns.")
}

func (s *Server) readDeliverySettings(r *http.Request) (deliverySettings, error) {
	var provider string
	var fallback *string
	var encrypted []byte
	err := s.pool.QueryRow(r.Context(), `
		SELECT provider, fallback_provider, resend_api_key_enc
		FROM workspace_delivery_settings
		WHERE workspace_id = $1
	`, s.organizationID(r)).Scan(&provider, &fallback, &encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return deliverySettings{Provider: delivery.ProviderStalwart}, nil
	}
	if err != nil {
		return deliverySettings{}, err
	}
	if strings.EqualFold(provider, delivery.ProviderResend) {
		provider = delivery.ProviderStalwart
	}
	settings := deliverySettings{Provider: provider, FallbackProvider: fallback}
	if fallback != nil && strings.EqualFold(*fallback, delivery.ProviderResend) {
		fallback = nil
		settings.FallbackProvider = nil
	}
	if len(encrypted) == 0 {
		return settings, nil
	}
	if s.cfg.DKIMEncryptionKey == "" {
		return deliverySettings{}, errors.New("DKIM_ENCRYPTION_KEY is not configured")
	}
	apiKey, err := delivery.DecryptSecret(s.cfg.DKIMEncryptionKey, encrypted)
	if err != nil {
		return deliverySettings{}, err
	}
	settings.ResendAPIKeySet = true
	if len(apiKey) >= 4 {
		settings.ResendAPIKeyLast4 = apiKey[len(apiKey)-4:]
	} else {
		settings.ResendAPIKeyLast4 = apiKey
	}
	settings.ResendAPIKeySet = false
	settings.ResendAPIKeyLast4 = ""
	return settings, nil
}

func (s *Server) workspaceResendAPIKey(r *http.Request, workspaceID string) (string, error) {
	var encrypted []byte
	err := s.pool.QueryRow(r.Context(), `
		SELECT resend_api_key_enc
		FROM workspace_delivery_settings
		WHERE workspace_id = $1
	`, workspaceID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) || len(encrypted) == 0 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if s.cfg.DKIMEncryptionKey == "" {
		return "", errors.New("DKIM_ENCRYPTION_KEY is not configured")
	}
	return delivery.DecryptSecret(s.cfg.DKIMEncryptionKey, encrypted)
}

func normalizeOptionalProvider(provider *string) *string {
	if provider == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(*provider))
	if normalized == "" {
		return nil
	}
	return &normalized
}

func nullableEncryptedKey(encrypted []byte) any {
	if len(encrypted) == 0 {
		return nil
	}
	return encrypted
}
