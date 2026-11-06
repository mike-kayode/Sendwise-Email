package workspace

import (
	"fmt"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

// Config holds Workspace service configuration.
type Config struct {
	kit.BaseConfig
	AutoMigrate    bool
	MigrationsDir  string
	InvitationTTL  time.Duration
	AppBaseURL     string
	AccessTokenTTL time.Duration
	ServiceToken   string
	IdentityURL    string
}

// LoadConfig reads Workspace service configuration from environment variables.
func LoadConfig() (Config, error) {
	base := kit.LoadBaseConfig("workspace")

	cfg := Config{
		BaseConfig:     base,
		AutoMigrate:    kit.GetenvBool("AUTO_MIGRATE", true),
		MigrationsDir:  kit.Getenv("MIGRATIONS_DIR", "services/workspace/migrations"),
		InvitationTTL:  kit.GetenvDuration("WORKSPACE_INVITATION_TTL", 7*24*time.Hour),
		AppBaseURL:     strings.TrimRight(kit.Getenv("APP_BASE_URL", "http://localhost:3000"), "/"),
		AccessTokenTTL: kit.GetenvDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		ServiceToken:   kit.Getenv("SERVICE_TOKEN", ""),
		IdentityURL:    strings.TrimRight(kit.Getenv("IDENTITY_URL", "http://localhost:8082"), "/"),
	}

	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.ServiceToken == "" {
		return Config{}, fmt.Errorf("SERVICE_TOKEN is required")
	}
	return cfg, nil
}
