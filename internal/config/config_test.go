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
			name: "long token lifetime",
			mutate: func(values map[string]string) {
				values["ACCESS_TOKEN_TTL"] = "2h"
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

func productionConfig() map[string]string {
	return map[string]string{
		"APP_ENV":                 "production",
		"JWT_SECRET":              strings.Repeat("a", 48),
		"SMTP_USERNAME":           "mailer",
		"SMTP_PASSWORD":           "smtp-secret",
		"REQUIRE_VERIFIED_DOMAIN": "true",
		"WEBHOOK_SECRET":          strings.Repeat("b", 32),
	}
}

func configLookup(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
