package identity

import (
	"fmt"
	"net/mail"
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
	MailProvider                  string // "smtp", "log", or "auto"
	SMTPAddr                      string
	SMTPHost                      string
	SMTPUsername                  string
	SMTPPassword                  string
	SMTPFrom                      string
	SMTPTLSMode                   string
	MailOutboxEncryptionKey       string
	PlatformFromEmail             string
	PlatformVerifiedSenderDomains string
	PlatformSMTPAddr              string
	PlatformSMTPHost              string
	PlatformSMTPUsername          string
	PlatformSMTPPassword          string
	PlatformSMTPTLSMode           string
	PlatformMailAPIKey            string
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

		MailProvider:                  strings.ToLower(strings.TrimSpace(kit.Getenv("MAIL_PROVIDER", "auto"))),
		SMTPAddr:                      kit.Getenv("SMTP_ADDR", ""),
		SMTPHost:                      kit.Getenv("SMTP_HOST", ""),
		SMTPUsername:                  kit.Getenv("SMTP_USERNAME", ""),
		SMTPPassword:                  kit.Getenv("SMTP_PASSWORD", ""),
		SMTPFrom:                      kit.Getenv("SMTP_FROM", ""),
		SMTPTLSMode:                   strings.ToLower(strings.TrimSpace(kit.Getenv("SMTP_TLS_MODE", defaultSMTPTLSMode(base.Env)))),
		MailOutboxEncryptionKey:       kit.Getenv("DKIM_ENCRYPTION_KEY", ""),
		PlatformFromEmail:             kit.Getenv("PLATFORM_FROM_EMAIL", ""),
		PlatformVerifiedSenderDomains: kit.Getenv("PLATFORM_VERIFIED_SENDER_DOMAINS", ""),
		PlatformSMTPAddr:              kit.Getenv("PLATFORM_SMTP_ADDR", ""),
		PlatformSMTPHost:              kit.Getenv("PLATFORM_SMTP_HOST", ""),
		PlatformSMTPUsername:          kit.Getenv("PLATFORM_SMTP_USERNAME", ""),
		PlatformSMTPPassword:          kit.Getenv("PLATFORM_SMTP_PASSWORD", ""),
		PlatformSMTPTLSMode:           strings.ToLower(strings.TrimSpace(kit.Getenv("PLATFORM_SMTP_TLS_MODE", defaultSMTPTLSMode(base.Env)))),
		PlatformMailAPIKey:            kit.Getenv("PLATFORM_MAIL_API_KEY", ""),
	}

	if cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.ServiceToken == "" {
		return Config{}, fmt.Errorf("SERVICE_TOKEN is required")
	}
	switch cfg.MailProvider {
	case "auto", "smtp", "log":
	default:
		return Config{}, fmt.Errorf("MAIL_PROVIDER must be auto, smtp, or log")
	}
	switch cfg.SMTPTLSMode {
	case "none", "starttls", "tls":
	default:
		return Config{}, fmt.Errorf("SMTP_TLS_MODE must be none, starttls, or tls")
	}
	if base.Env == "production" {
		if cfg.MailOutboxEncryptionKey == "" {
			return Config{}, fmt.Errorf("DKIM_ENCRYPTION_KEY is required for encrypted transactional mail retries")
		}
		if cfg.MailProvider == "log" {
			return Config{}, fmt.Errorf("MAIL_PROVIDER=log is not allowed in production")
		}
		if cfg.PlatformSMTPAddr != "" && cfg.PlatformSMTPTLSMode != "starttls" && cfg.PlatformSMTPTLSMode != "tls" {
			return Config{}, fmt.Errorf("PLATFORM_SMTP_TLS_MODE must be starttls or tls in production")
		}
	}
	// Fail fast when a secure environment would deliver mail to Mailpit or the
	// loopback interface, or when the log mailer is configured: both accept
	// the message and never hand it to a real recipient.
	if base.Env == "staging" || base.Env == "production" {
		if cfg.MailProvider == "log" {
			return Config{}, fmt.Errorf("MAIL_PROVIDER=log is not allowed in %s", base.Env)
		}
		if cfg.PlatformSMTPAddr != "" {
			if err := kit.ValidateProductionMail(base.Env, "smtp", cfg.PlatformSMTPAddr, cfg.PlatformSMTPHost); err != nil {
				return Config{}, err
			}
		}
	}
	return cfg, nil
}

func mailSenderDegradation(cfg Config) string {
	if cfg.SMTPFrom == "" || cfg.PlatformFromEmail == "" {
		return "sender_not_configured"
	}

	allowed := make(map[string]struct{})
	for _, domain := range strings.Split(cfg.PlatformVerifiedSenderDomains, ",") {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain != "" {
			allowed[domain] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return "sender_allowlist_missing"
	}

	for _, sender := range []string{cfg.SMTPFrom, cfg.PlatformFromEmail} {
		address, err := mail.ParseAddress(sender)
		if err != nil {
			return "sender_address_invalid"
		}
		_, domain, ok := strings.Cut(strings.ToLower(address.Address), "@")
		if !ok {
			return "sender_address_invalid"
		}
		if _, ok := allowed[strings.TrimSuffix(domain, ".")]; !ok {
			return "sender_domain_not_allowlisted"
		}
	}

	switch cfg.MailProvider {
	case "log":
		return "mail_delivery_disabled"
	case "resend":
		if cfg.PlatformMailAPIKey == "" {
			return "platform_mail_api_key_missing"
		}
	case "smtp":
		if cfg.PlatformSMTPAddr == "" && (cfg.Env == "production" || cfg.Env == "staging") {
			return "platform_smtp_not_configured"
		}
		if cfg.PlatformSMTPAddr == "" && cfg.Env != "production" && cfg.Env != "staging" && cfg.SMTPAddr == "" {
			return "platform_smtp_not_configured"
		}
	case "", "auto":
		if cfg.PlatformSMTPAddr == "" && cfg.PlatformMailAPIKey == "" &&
			(cfg.Env == "production" || cfg.Env == "staging" || cfg.SMTPAddr == "") {
			return "platform_mail_transport_not_configured"
		}
	default:
		return "mail_provider_invalid"
	}
	return ""
}

func defaultSMTPTLSMode(env string) string {
	if env == "production" || env == "staging" {
		return "starttls"
	}
	return "none"
}
