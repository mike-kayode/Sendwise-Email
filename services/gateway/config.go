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
	CORSOrigins  []string

	AuthRatePerMinute int
	AuthBurst         int
}

// LoadConfig reads Gateway configuration from environment variables.
func LoadConfig() (Config, error) {
	base := kit.LoadBaseConfig("gateway")

	cfg := Config{
		BaseConfig:        base,
		IdentityURL:       strings.TrimRight(kit.Getenv("IDENTITY_URL", "http://localhost:8081"), "/"),
		WorkspaceURL:      strings.TrimRight(kit.Getenv("WORKSPACE_URL", "http://localhost:8082"), "/"),
		LegacyAPIURL:      strings.TrimRight(kit.Getenv("LEGACY_API_URL", "http://localhost:8090"), "/"),
		CORSOrigins:       splitCSV(kit.Getenv("CORS_ORIGINS", "http://localhost:3000")),
		AuthRatePerMinute: kit.GetenvInt("GATEWAY_AUTH_RATE_PER_MINUTE", 60),
		AuthBurst:         kit.GetenvInt("GATEWAY_AUTH_BURST", 20),
	}

	if cfg.AuthRatePerMinute < 1 || cfg.AuthBurst < 1 {
		return Config{}, fmt.Errorf("gateway rate limits must be positive")
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
