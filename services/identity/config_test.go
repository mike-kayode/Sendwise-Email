package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

func TestLoadConfigRequiresJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	t.Setenv("APP_ENV", "test")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want failure when JWT_SECRET is missing")
	}
}

func TestLoadConfigValid(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "test")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("IDENTITY_ACCESS_TOKEN_TTL", "10m")
	t.Setenv("IDENTITY_REFRESH_TOKEN_TTL", "48h")
	t.Setenv("APP_BASE_URL", "https://app.example.com/")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.ServiceName != "identity" {
		t.Errorf("ServiceName = %q, want identity", cfg.ServiceName)
	}
	if cfg.JWTSecret != "test-secret" {
		t.Errorf("JWTSecret = %q", cfg.JWTSecret)
	}
	if cfg.AccessTokenTTL != 10*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want 10m", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 48*time.Hour {
		t.Errorf("RefreshTokenTTL = %v, want 48h", cfg.RefreshTokenTTL)
	}
	if cfg.AppBaseURL != "https://app.example.com" {
		t.Errorf("AppBaseURL = %q, want trailing slash trimmed", cfg.AppBaseURL)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true in test environment without COOKIE_SECURE")
	}
	if cfg.SMTPTLSMode != "none" {
		t.Errorf("SMTPTLSMode = %q, want none in test environment", cfg.SMTPTLSMode)
	}
}

func TestLoadConfigAlwaysSecuresCookiesInProduction(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("MAIL_PROVIDER", "smtp")
	t.Setenv("SMTP_ADDR", "smtp.example.test:587")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "smtp-secret")
	t.Setenv("SMTP_FROM", "hello@example.test")
	t.Setenv("SMTP_TLS_MODE", "starttls")
	t.Setenv("DKIM_ENCRYPTION_KEY", "test-mail-outbox-key")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false in production")
	}
	if cfg.SMTPTLSMode != "starttls" {
		t.Errorf("SMTPTLSMode = %q, want starttls", cfg.SMTPTLSMode)
	}
}

func TestLoadConfigValidatesSMTPTLSMode(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "test")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("SMTP_TLS_MODE", "opportunistic")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "SMTP_TLS_MODE") {
		t.Fatalf("LoadConfig() error = %v, want invalid SMTP_TLS_MODE failure", err)
	}
}

func TestLoadProductionSMTPRequiresTLS(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("DKIM_ENCRYPTION_KEY", "test-mail-outbox-key")
	t.Setenv("MAIL_PROVIDER", "smtp")
	t.Setenv("SMTP_ADDR", "smtp.example.test:587")
	t.Setenv("SMTP_HOST", "smtp.example.test")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "smtp-secret")
	t.Setenv("SMTP_FROM", "hello@example.test")
	t.Setenv("SMTP_TLS_MODE", "none")
	t.Setenv("PLATFORM_SMTP_ADDR", "relay.example.test:587")
	t.Setenv("PLATFORM_SMTP_HOST", "relay.example.test")
	t.Setenv("PLATFORM_SMTP_TLS_MODE", "none")
	t.Setenv("DKIM_ENCRYPTION_KEY", "test-mail-outbox-key")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "PLATFORM_SMTP_TLS_MODE") {
		t.Fatalf("LoadConfig() error = %v, want production SMTP TLS failure", err)
	}
}

func TestLoadProductionAllowsRegistrationWithMailDegraded(t *testing.T) {
	for key, value := range map[string]string{
		"JWT_SECRET": "test-secret", "APP_ENV": "production", "SERVICE_TOKEN": "test-service-token",
		"DKIM_ENCRYPTION_KEY": "test-mail-outbox-key",
	} {
		t.Setenv(key, value)
	}
	t.Setenv("MAIL_PROVIDER", "auto")
	t.Setenv("SMTP_FROM", "hello@example.test")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() should allow registration with degraded mail: %v", err)
	}
	if got := mailSenderDegradation(cfg); got != "sender_not_configured" {
		t.Fatalf("mailSenderDegradation() = %q, want sender_not_configured", got)
	}

	t.Setenv("MAIL_PROVIDER", "resend")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() accepted the removed resend MAIL_PROVIDER")
	}

	t.Setenv("MAIL_PROVIDER", "log")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() accepted the log mailer in production")
	}
}

func TestLoadConfigRejectsUnknownMailProvider(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("MAIL_PROVIDER", "carrier-pigeon")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want failure for unknown MAIL_PROVIDER")
	}
}

func TestLoadProductionRejectsDevelopmentMailRelay(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "smtp-secret")
	t.Setenv("SMTP_FROM", "hello@example.test")
	t.Setenv("DKIM_ENCRYPTION_KEY", "test-mail-outbox-key")
	t.Setenv("SMTP_ADDR", "smtp.campaign.example.test:587")
	t.Setenv("SMTP_HOST", "smtp.campaign.example.test")

	for _, test := range []struct {
		name     string
		provider string
		addr     string
		host     string
		want     string
	}{
		{name: "mailpit relay", provider: "smtp", addr: "mailpit:1025", host: "mailpit", want: "development-only relay"},
		{name: "localhost relay", provider: "smtp", addr: "localhost:587", host: "localhost", want: "development-only relay"},
		{name: "loopback ip relay", provider: "smtp", addr: "127.0.0.1:587", host: "127.0.0.1", want: "development-only relay"},
		{name: "mailpit with auto provider", provider: "auto", addr: "mailpit:1025", host: "mailpit", want: "development-only relay"},
		{name: "log mailer", provider: "log", addr: "smtp.example.test:587", host: "smtp.example.test", want: "MAIL_PROVIDER=log is not allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MAIL_PROVIDER", test.provider)
			t.Setenv("PLATFORM_SMTP_ADDR", test.addr)
			t.Setenv("PLATFORM_SMTP_HOST", test.host)

			_, err := LoadConfig()
			if err == nil {
				t.Fatalf("LoadConfig() accepted production mail configuration %s/%s/%s", test.provider, test.addr, test.host)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConfig() error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestLoadDevelopmentAllowsDevelopmentMailRelay(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("MAIL_PROVIDER", "log")
	t.Setenv("SMTP_ADDR", "mailpit:1025")
	t.Setenv("SMTP_HOST", "mailpit")

	if _, err := LoadConfig(); err != nil {
		t.Fatalf("LoadConfig() rejected development Mailpit/log configuration: %v", err)
	}
}

func TestMailSenderDegradationValidatesAllowlistedDomains(t *testing.T) {
	cfg := Config{
		BaseConfig:                    kit.BaseConfig{Env: "production"},
		MailProvider:                  "auto",
		SMTPFrom:                      "campaign@example.test",
		PlatformFromEmail:             "platform@example.test",
		PlatformVerifiedSenderDomains: "example.test",
		PlatformSMTPAddr:              "smtp.example.test:587",
	}
	if got := mailSenderDegradation(cfg); got != "" {
		t.Fatalf("valid mail config degradation = %q, want empty", got)
	}
	cfg.PlatformFromEmail = "platform@foreign.test"
	if got := mailSenderDegradation(cfg); got != "sender_domain_not_allowlisted" {
		t.Fatalf("foreign sender degradation = %q, want sender_domain_not_allowlisted", got)
	}
}
