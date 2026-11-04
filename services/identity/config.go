package identity

import (
	"fmt"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

// Config holds Identity service configuration.
type Config struct {
	kit.BaseConfig
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	CookieSecure    bool
	AppBaseURL      string
	AutoMigrate     bool
	MigrationsDir   string

	// Workspace service integration (service-to-service).
	WorkspaceURL string
	ServiceToken string

	// Mail delivery for verification and reset messages.
	MailProvider    string // "resend", "smtp", "log", or "auto"
	SMTPAddr        string
	SMTPHost        string
	SMTPUsername    string
	SMTPPassword    string
	SMTPFrom        string
	ResendAPIKey    string
	ResendFromEmail string
	ResendFromName  string
}

// LoadConfig reads Identity service configuration from environment variables.
func LoadConfig() (Config, error) {
	base := kit.LoadBaseConfig("identity")

	cfg := Config{
		BaseConfig:      base,
		AccessTokenTTL:  kit.GetenvDuration("IDENTITY_ACCESS_TOKEN_TTL", kit.GetenvDuration("ACCESS_TOKEN_TTL", 15*time.Minute)),
		RefreshTokenTTL: kit.GetenvDuration("IDENTITY_REFRESH_TOKEN_TTL", 7*24*time.Hour),
		CookieSecure:    base.Env == "production" || kit.GetenvBool("COOKIE_SECURE", false),
		AppBaseURL:      strings.TrimRight(kit.Getenv("APP_BASE_URL", "http://localhost:3000"), "/"),
		AutoMigrate:     kit.GetenvBool("AUTO_MIGRATE", true),
		MigrationsDir:   kit.Getenv("MIGRATIONS_DIR", "services/identity/migrations"),

		WorkspaceURL: strings.TrimRight(kit.Getenv("WORKSPACE_URL", "http://localhost:8081"), "/"),
		ServiceToken: kit.Getenv("SERVICE_TOKEN", ""),

		MailProvider:    strings.ToLower(strings.TrimSpace(kit.Getenv("MAIL_PROVIDER", "auto"))),
		SMTPAddr:        kit.Getenv("SMTP_ADDR", "localhost:587"),
		SMTPHost:        kit.Getenv("SMTP_HOST", "localhost"),
		SMTPUsername:    kit.Getenv("SMTP_USERNAME", ""),
		SMTPPassword:    kit.Getenv("SMTP_PASSWORD", ""),
		SMTPFrom:        kit.Getenv("SMTP_FROM", "newsletter@example.com"),
		ResendAPIKey:    kit.Getenv("RESEND_API_KEY", ""),
		ResendFromEmail: kit.Getenv("RESEND_FROM_EMAIL", ""),
		ResendFromName:  kit.Getenv("RESEND_FROM_NAME", "Sendwise"),
	}

	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.ServiceToken == "" {
		return Config{}, fmt.Errorf("SERVICE_TOKEN is required")
	}
	switch cfg.MailProvider {
	case "auto", "resend", "smtp", "log":
	default:
		return Config{}, fmt.Errorf("MAIL_PROVIDER must be auto, resend, smtp, or log")
	}
	return cfg, nil
}
