package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDevelopmentDefaults(t *testing.T) {
	cfg, err := load(configLookup(nil))
	if err != nil {
		t.Fatalf("load development defaults: %v", err)
	}
	if cfg.Env != "development" {
		t.Fatalf("Env = %q, want development", cfg.Env)
	}
	if cfg.JWTSecret == "" {
		t.Fatal("development JWT secret must not be empty")
	}
	if cfg.SendingRate != 20 || cfg.MaxDeliveryAttempts != 5 {
		t.Fatalf("unexpected delivery defaults: rate=%d attempts=%d", cfg.SendingRate, cfg.MaxDeliveryAttempts)
	}
	if cfg.SchedulerInterval != 15*time.Second {
		t.Fatalf("SchedulerInterval = %s, want 15s", cfg.SchedulerInterval)
	}
	if cfg.WarmupStartPerDay != 50 || cfg.WarmupGrowthFactor != 1.5 {
		t.Fatalf("unexpected warm-up defaults: start=%d growth=%v", cfg.WarmupStartPerDay, cfg.WarmupGrowthFactor)
	}
	if cfg.SMTPPoolSize != 10 || cfg.MaxBounceRate != 0.02 || cfg.MaxComplaintRate != 0.0005 {
		t.Fatalf("unexpected delivery feedback defaults: pool=%d bounce=%v complaint=%v",
			cfg.SMTPPoolSize, cfg.MaxBounceRate, cfg.MaxComplaintRate)
	}
	if cfg.RecipientDomainRates["gmail.com"] != 0 || cfg.RecipientDomainDefault != 10 {
		t.Fatalf("unexpected recipient rate defaults: %+v default=%d", cfg.RecipientDomainRates, cfg.RecipientDomainDefault)
	}
}

func TestLoadRejectsMalformedEnvironmentValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "auto migrate boolean", key: "AUTO_MIGRATE", value: "sometimes"},
		{name: "domain verification boolean", key: "REQUIRE_VERIFIED_DOMAIN", value: "enabled"},
		{name: "access token duration", key: "ACCESS_TOKEN_TTL", value: "soon"},
		{name: "sending rate integer", key: "SENDING_RATE_PER_SECOND", value: "fast"},
		{name: "delivery attempts integer", key: "MAX_DELIVERY_ATTEMPTS", value: "many"},
		{name: "scheduler duration", key: "SCHEDULER_INTERVAL", value: "often"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(configLookup(map[string]string{test.key: test.value}))
			if err == nil {
				t.Fatalf("load accepted invalid %s=%q", test.key, test.value)
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Fatalf("error %q does not identify %s", err, test.key)
			}
		})
	}
}

func TestLoadRejectsUnknownEnvironment(t *testing.T) {
	for _, env := range []string{"prod", "PRODUCTION"} {
		t.Run(env, func(t *testing.T) {
			if _, err := load(configLookup(map[string]string{"APP_ENV": env})); err == nil {
				t.Fatalf("load accepted unknown APP_ENV %q", env)
			}
		})
	}
}

func TestLoadRejectsInvalidRuntimeLimits(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "zero sending rate", key: "SENDING_RATE_PER_SECOND", value: "0"},
		{name: "zero delivery attempts", key: "MAX_DELIVERY_ATTEMPTS", value: "0"},
		{name: "zero scheduler interval", key: "SCHEDULER_INTERVAL", value: "0s"},
		{name: "zero access token ttl", key: "ACCESS_TOKEN_TTL", value: "0s"},
		{name: "warm-up growth below one", key: "WARMUP_GROWTH_FACTOR", value: "0.5"},
		{name: "recipient rate missing default", key: "RECIPIENT_DOMAIN_RATE_LIMITS", value: "gmail.com=4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(configLookup(map[string]string{test.key: test.value}))
			if err == nil {
				t.Fatalf("load accepted invalid %s=%q", test.key, test.value)
			}
		})
	}
}

func TestLoadProductionRequiresSecureConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{
			name: "missing jwt secret",
			mutate: func(values map[string]string) {
				delete(values, "JWT_SECRET")
			},
		},
		{
			name: "placeholder jwt secret",
			mutate: func(values map[string]string) {
				values["JWT_SECRET"] = "change-me-in-production-please-use-a-long-random-value"
			},
		},
		{
			name: "domain checks disabled",
			mutate: func(values map[string]string) {
				values["REQUIRE_VERIFIED_DOMAIN"] = "false"
			},
		},
		{
			name: "missing smtp password",
			mutate: func(values map[string]string) {
				values["SMTP_PASSWORD"] = ""
			},
		},
		{
			name: "missing webhook secret",
			mutate: func(values map[string]string) {
				delete(values, "WEBHOOK_SECRET")
			},
		},
		{
			name: "missing identity session check",
			mutate: func(values map[string]string) {
				delete(values, "IDENTITY_URL")
			},
		},
		{
			name: "long token lifetime",
			mutate: func(values map[string]string) {
				values["ACCESS_TOKEN_TTL"] = "2h"
			},
		},
		{
			name: "smtp relay is mailpit",
			mutate: func(values map[string]string) {
				values["SMTP_ADDR"] = "mailpit:1025"
				values["SMTP_HOST"] = "mailpit"
			},
		},
		{
			name: "smtp relay is localhost",
			mutate: func(values map[string]string) {
				values["SMTP_ADDR"] = "localhost:587"
				values["SMTP_HOST"] = "localhost"
			},
		},
		{
			name: "mail provider is log",
			mutate: func(values map[string]string) {
				values["MAIL_PROVIDER"] = "log"
			},
		},
		{
			name: "unknown mail provider",
			mutate: func(values map[string]string) {
				values["MAIL_PROVIDER"] = "carrier-pigeon"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := productionConfig()
			test.mutate(values)
			if _, err := load(configLookup(values)); err == nil {
				t.Fatal("load accepted unsafe production configuration")
			}
		})
	}
}

func TestLoadAcceptsValidProductionConfiguration(t *testing.T) {
	cfg, err := load(configLookup(productionConfig()))
	if err != nil {
		t.Fatalf("load valid production config: %v", err)
	}
	if !cfg.RequireVerifiedDomain {
		t.Fatal("production must require verified domains")
	}
	if cfg.MailProvider != "auto" {
		t.Fatalf("MailProvider = %q, want auto default", cfg.MailProvider)
	}
}

func TestLoadProductionRejectsResendMailProvider(t *testing.T) {
	values := productionConfig()
	values["MAIL_PROVIDER"] = "resend"
	if _, err := load(configLookup(values)); err == nil {
		t.Fatal("load accepted MAIL_PROVIDER=resend; campaigns send through Stalwart only")
	}
}

func TestLoadDevelopmentAllowsDevelopmentMailRelay(t *testing.T) {
	values := map[string]string{
		"MAIL_PROVIDER": "log",
		"SMTP_ADDR":     "mailpit:1025",
		"SMTP_HOST":     "mailpit",
	}
	if _, err := load(configLookup(values)); err != nil {
		t.Fatalf("development load rejected Mailpit/log configuration: %v", err)
	}
}

func TestLoadAppliesProductionRulesToStaging(t *testing.T) {
	values := productionConfig()
	values["APP_ENV"] = "staging"
	if _, err := load(configLookup(values)); err != nil {
		t.Fatalf("load valid staging config: %v", err)
	}
	values["REQUIRE_VERIFIED_DOMAIN"] = "false"
	if _, err := load(configLookup(values)); err == nil {
		t.Fatal("load accepted staging with unverified domains enabled")
	}
}

func TestLoadProductionRequiresStalwartManagementCredentials(t *testing.T) {
	values := productionConfig()
	delete(values, "STALWART_ADMIN_PASSWORD")
	if _, err := load(configLookup(values)); err == nil {
		t.Fatal("load accepted production config without Stalwart management credentials")
	}
}

func productionConfig() map[string]string {
	return map[string]string{
		"APP_ENV":                 "production",
		"JWT_SECRET":              strings.Repeat("a", 48),
		"SMTP_ADDR":               "smtp.example.test:587",
		"BOUNCE_DOMAIN":           "bounces.example.test",
		"SMTP_HOST":               "smtp.example.test",
		"SMTP_USERNAME":           "mailer",
		"SMTP_PASSWORD":           "smtp-secret",
		"IDENTITY_URL":            "http://identity:8080",
		"SERVICE_TOKEN":           "service-token",
		"REQUIRE_VERIFIED_DOMAIN": "true",
		"WEBHOOK_SECRET":          strings.Repeat("b", 32),
		"STALWART_ADMIN_USERNAME": "admin",
		"STALWART_ADMIN_PASSWORD": "stalwart-admin-test-password",
		"STALWART_DOMAIN":         "sendwise.test",
	}
}

func configLookup(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
