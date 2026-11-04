package gateway

import (
	"fmt"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

// Config holds Gateway service configuration.
type Config struct {
	kit.BaseConfig
	IdentityURL  string
	WorkspaceURL string
	LegacyAPIURL string
	ServiceToken string
	CORSOrigins  []string

	AuthRatePerMinute int
	AuthBurst         int
}

// LoadConfig reads Gateway configuration from environment variables.
// Identity, Workspace, and LegacyAPI URLs plus the service token are required
// in production. Use APP_ENV=production to enforce presence; defaults allow
// local dev.
func LoadConfig() (Config, error) {
	base := kit.LoadBaseConfig("gateway")

	cfg := Config{
		BaseConfig:        base,
		IdentityURL:       strings.TrimRight(kit.Getenv("IDENTITY_URL", ""), "/"),
		WorkspaceURL:      strings.TrimRight(kit.Getenv("WORKSPACE_URL", ""), "/"),
		LegacyAPIURL:      strings.TrimRight(kit.Getenv("LEGACY_API_URL", ""), "/"),
		ServiceToken:      strings.TrimSpace(kit.Getenv("SERVICE_TOKEN", "")),
		CORSOrigins:       splitCSV(kit.Getenv("CORS_ORIGINS", "http://localhost:3000")),
		AuthRatePerMinute: kit.GetenvInt("GATEWAY_AUTH_RATE_PER_MINUTE", 60),
		AuthBurst:         kit.GetenvInt("GATEWAY_AUTH_BURST", 20),
	}

	if cfg.AuthRatePerMinute < 1 || cfg.AuthBurst < 1 {
		return Config{}, fmt.Errorf("gateway rate limits must be positive")
	}

	// Fail fast in production when required configuration is absent.
	if base.Env == "production" {
		if cfg.IdentityURL == "" {
			return Config{}, fmt.Errorf("IDENTITY_URL is required in production")
		}
		if cfg.WorkspaceURL == "" {
			return Config{}, fmt.Errorf("WORKSPACE_URL is required in production")
		}
		if cfg.LegacyAPIURL == "" {
			return Config{}, fmt.Errorf("LEGACY_API_URL is required in production")
		}
		if cfg.ServiceToken == "" {
			return Config{}, fmt.Errorf("SERVICE_TOKEN is required in production")
		}
	}
	return cfg, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// idle timeout used for proxied upstreams.
const upstreamTimeout = 30 * time.Second
