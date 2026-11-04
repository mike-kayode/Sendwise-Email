package identity

import (
	"testing"
	"time"
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
}

func TestLoadConfigAlwaysSecuresCookiesInProduction(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SERVICE_TOKEN", "test-service-token")
	t.Setenv("COOKIE_SECURE", "false")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false in production")
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
