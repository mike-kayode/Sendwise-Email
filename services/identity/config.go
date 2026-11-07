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
	MailProvider            string // "resend", "smtp", "log", or "auto"
	SMTPAddr                string
	SMTPHost                string
	SMTPUsername            string
	SMTPPassword            string
	SMTPFrom                string
	SMTPTLSMode             string
	ResendAPIKey            string
	ResendFromEmail         string
	ResendFromName          string
	PlatformFromEmail       string
	PlatformResendAPIKey    string
	ResendRequestsPerSecond int
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

		MailProvider:            strings.ToLower(strings.TrimSpace(kit.Getenv("MAIL_PROVIDER", "auto"))),
		SMTPAddr:                kit.Getenv("SMTP_ADDR", ""),
		SMTPHost:                kit.Getenv("SMTP_HOST", ""),
		SMTPUsername:            kit.Getenv("SMTP_USERNAME", ""),
		SMTPPassword:            kit.Getenv("SMTP_PASSWORD", ""),
		SMTPFrom:                kit.Getenv("SMTP_FROM", ""),
		SMTPTLSMode:             strings.ToLower(strings.TrimSpace(kit.Getenv("SMTP_TLS_MODE", defaultSMTPTLSMode(base.Env)))),
		ResendAPIKey:            kit.Getenv("RESEND_API_KEY", ""),
		ResendFromEmail:         kit.Getenv("RESEND_FROM_EMAIL", ""),
		ResendFromName:          kit.Getenv("RESEND_FROM_NAME", "Sendwise"),
		PlatformFromEmail:       kit.Getenv("PLATFORM_FROM_EMAIL", kit.Getenv("SMTP_FROM", "")),
		PlatformResendAPIKey:    kit.Getenv("PLATFORM_RESEND_API_KEY", ""),
		ResendRequestsPerSecond: kit.GetenvInt("RESEND_REQUESTS_PER_SECOND", 8),
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
	switch cfg.SMTPTLSMode {
	case "none", "starttls", "tls":
	default:
		return Config{}, fmt.Errorf("SMTP_TLS_MODE must be none, starttls, or tls")
	}
	if base.Env == "production" {
		provider := cfg.MailProvider
		if provider == "auto" {
			if cfg.ResendAPIKey != "" {
				provider = "resend"
			} else {
				provider = "smtp"
			}
		}
		switch provider {
		case "smtp":
			if cfg.SMTPTLSMode == "none" {
				return Config{}, fmt.Errorf("SMTP_TLS_MODE must be starttls or tls for production SMTP mail")
			}
			if cfg.SMTPAddr == "" || cfg.SMTPHost == "" || cfg.SMTPUsername == "" ||
				cfg.SMTPPassword == "" || cfg.SMTPFrom == "" {
				return Config{}, fmt.Errorf("SMTP_ADDR, SMTP_HOST, SMTP_USERNAME, SMTP_PASSWORD, and SMTP_FROM are required for production SMTP mail")
			}

		case "resend":
			if cfg.ResendAPIKey == "" || cfg.ResendFromEmail == "" {
				return Config{}, fmt.Errorf("RESEND_API_KEY and RESEND_FROM_EMAIL are required for production Resend mail")
			}
		default:
			return Config{}, fmt.Errorf("MAIL_PROVIDER=log is not allowed in production")
		}
	}
	// Fail fast when a secure environment would deliver mail to Mailpit or the
	// loopback interface, or when the log mailer is configured: both accept
	// the message and never hand it to a real recipient.
	if err := kit.ValidateProductionMail(base.Env, cfg.MailProvider, cfg.SMTPAddr, cfg.SMTPHost); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaultSMTPTLSMode(env string) string {
	if env == "production" || env == "staging" {
		return "starttls"
	}
	return "none"
}
