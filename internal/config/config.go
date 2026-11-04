package config

import (
	"fmt"
	"net"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env                   string
	HTTPAddr              string
	WorkerMetricsAddr     string
	AppBaseURL            string
	APIBaseURL            string
	TrackingBaseURL       string
	DatabaseURL           string
	RedisURL              string
	NATSURL               string
	MigrationsDir         string
	AutoMigrate           bool
	JWTSecret             string
	JWTIssuer             string
	AccessTokenTTL        time.Duration
	SMTPAddr              string
	SMTPHost              string
	SMTPUsername          string
	SMTPPassword          string
	SMTPFrom              string
	DKIMSelector          string
	SendingRate           int
	MaxDeliveryAttempts   int
	SchedulerInterval     time.Duration
	MaxMessageAge         time.Duration
	MaxDailyEmails        int
	MaxBounceRate         float64
	MaxComplaintRate      float64
	DomainConcurrencyLimit int
	CORSOrigins           []string
	RequireVerifiedDomain bool
	ResendAPIKey          string
	ResendFromEmail       string
	ResendFromName        string
	SuperAdminEmails      []string
}

func Load() (Config, error) {
	return load(os.Getenv)
}

func load(lookup func(string) string) (Config, error) {
	env := getenv(lookup, "APP_ENV", "development")
	switch env {
	case "development", "test", "staging", "production":
	default:
		return Config{}, fmt.Errorf("APP_ENV must be development, test, staging, or production")
	}
	secureEnv := env == "staging" || env == "production"

	autoMigrate, err := getenvBool(lookup, "AUTO_MIGRATE", false)
	if err != nil {
		return Config{}, err
	}
	accessTokenTTL, err := getenvDuration(lookup, "ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	sendingRate, err := getenvInt(lookup, "SENDING_RATE_PER_SECOND", 20)
	if err != nil {
		return Config{}, err
	}
	maxDeliveryAttempts, err := getenvInt(lookup, "MAX_DELIVERY_ATTEMPTS", 5)
	if err != nil {
		return Config{}, err
	}
	schedulerInterval, err := getenvDuration(lookup, "SCHEDULER_INTERVAL", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxMessageAge, err := getenvDuration(lookup, "MAX_MESSAGE_AGE", 48*time.Hour)
	if err != nil {
		return Config{}, err
	}
	maxDailyEmails, err := getenvInt(lookup, "MAX_DAILY_EMAILS", 10000)
	if err != nil {
		return Config{}, err
	}
	maxBounceRate, err := getenvFloat(lookup, "MAX_BOUNCE_RATE", 0.05)
	if err != nil {
		return Config{}, err
	}
	maxComplaintRate, err := getenvFloat(lookup, "MAX_COMPLAINT_RATE", 0.001)
	if err != nil {
		return Config{}, err
	}
	domainConcurrencyLimit, err := getenvInt(lookup, "DOMAIN_CONCURRENCY_LIMIT", 10)
	if err != nil {
		return Config{}, err
	}
	requireVerifiedDomain, err := getenvBool(lookup, "REQUIRE_VERIFIED_DOMAIN", secureEnv)
	if err != nil {
		return Config{}, err
	}

	resendAPIKey := getenv(lookup, "RESEND_API_KEY", "")
	resendFromEmail := getenv(lookup, "RESEND_FROM_EMAIL", "activate@pipsleague.com")
	resendFromName := getenv(lookup, "RESEND_FROM_NAME", "Sendwise")
	superAdminEmails := splitCSV(getenv(lookup, "SUPER_ADMIN_EMAILS", ""))

	cfg := Config{
		Env:                   env,
		HTTPAddr:              getenv(lookup, "HTTP_ADDR", ":8080"),
		WorkerMetricsAddr:     getenv(lookup, "WORKER_METRICS_ADDR", ":9091"),
		AppBaseURL:            getenv(lookup, "APP_BASE_URL", "http://localhost:3000"),
		APIBaseURL:            getenv(lookup, "API_BASE_URL", "http://localhost:8080"),
		TrackingBaseURL:       getenv(lookup, "TRACKING_BASE_URL", "http://localhost:8080"),
		DatabaseURL:           getenv(lookup, "DATABASE_URL", "postgres://email:email@localhost:5432/email?sslmode=disable"),
		RedisURL:              getenv(lookup, "REDIS_URL", "redis://localhost:6379/0"),
		NATSURL:               getenv(lookup, "NATS_URL", "nats://localhost:4222"),
		MigrationsDir:         getenv(lookup, "MIGRATIONS_DIR", "./migrations"),
		AutoMigrate:           autoMigrate,
		JWTSecret:             getenv(lookup, "JWT_SECRET", ""),
		JWTIssuer:             getenv(lookup, "JWT_ISSUER", "email-platform"),
		AccessTokenTTL:        accessTokenTTL,
		SMTPAddr:              getenv(lookup, "SMTP_ADDR", "localhost:587"),
		SMTPHost:              getenv(lookup, "SMTP_HOST", "localhost"),
		SMTPUsername:          getenv(lookup, "SMTP_USERNAME", ""),
		SMTPPassword:          getenv(lookup, "SMTP_PASSWORD", ""),
		SMTPFrom:              getenv(lookup, "SMTP_FROM", "newsletter@example.com"),
		DKIMSelector:          getenv(lookup, "DKIM_SELECTOR", "mail"),
		SendingRate:           sendingRate,
		MaxDeliveryAttempts:   maxDeliveryAttempts,
		SchedulerInterval:     schedulerInterval,
		MaxMessageAge:         maxMessageAge,
		MaxDailyEmails:        maxDailyEmails,
		MaxBounceRate:         maxBounceRate,
		MaxComplaintRate:      maxComplaintRate,
		DomainConcurrencyLimit: domainConcurrencyLimit,
		CORSOrigins:           splitCSV(getenv(lookup, "CORS_ORIGINS", "http://localhost:3000")),
		RequireVerifiedDomain: requireVerifiedDomain,
		ResendAPIKey:          resendAPIKey,
		ResendFromEmail:       resendFromEmail,
		ResendFromName:        resendFromName,
		SuperAdminEmails:      superAdminEmails,
	}

	if cfg.JWTSecret == "" {
		if secureEnv {
			return cfg, fmt.Errorf("JWT_SECRET is required when APP_ENV is staging or production")
		}
		cfg.JWTSecret = "dev-only-insecure-secret-change-me"
	}
	if len(cfg.JWTSecret) < 16 {
		return cfg, fmt.Errorf("JWT_SECRET must be at least 16 characters")
	}
	if secureEnv {
		if len(cfg.JWTSecret) < 32 || strings.Contains(strings.ToLower(cfg.JWTSecret), "change-me") || strings.Contains(strings.ToLower(cfg.JWTSecret), "insecure") {
			return cfg, fmt.Errorf("JWT_SECRET must be a non-placeholder secret of at least 32 characters in staging or production")
		}
		if !cfg.RequireVerifiedDomain {
			return cfg, fmt.Errorf("REQUIRE_VERIFIED_DOMAIN must be true in staging and production")
		}
		if cfg.SMTPUsername == "" || cfg.SMTPPassword == "" {
			return cfg, fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD are required in staging and production")
		}
	}
	if cfg.AccessTokenTTL <= 0 || (secureEnv && cfg.AccessTokenTTL > time.Hour) {
		return cfg, fmt.Errorf("ACCESS_TOKEN_TTL must be positive and no longer than 1h in staging and production")
	}
	if cfg.SendingRate < 1 {
		return cfg, fmt.Errorf("SENDING_RATE_PER_SECOND must be at least 1")
	}
	if cfg.MaxDeliveryAttempts < 1 {
		return cfg, fmt.Errorf("MAX_DELIVERY_ATTEMPTS must be at least 1")
	}
	if cfg.SchedulerInterval <= 0 {
		return cfg, fmt.Errorf("SCHEDULER_INTERVAL must be positive")
	}
	if cfg.MaxMessageAge < 0 {
		return cfg, fmt.Errorf("MAX_MESSAGE_AGE must not be negative")
	}
	if cfg.MaxDailyEmails < 1 {
		return cfg, fmt.Errorf("MAX_DAILY_EMAILS must be at least 1")
	}
	if cfg.MaxBounceRate < 0 || cfg.MaxBounceRate > 1 {
		return cfg, fmt.Errorf("MAX_BOUNCE_RATE must be between 0 and 1")
	}
	if cfg.MaxComplaintRate < 0 || cfg.MaxComplaintRate > 1 {
		return cfg, fmt.Errorf("MAX_COMPLAINT_RATE must be between 0 and 1")
	}
	if cfg.DomainConcurrencyLimit < 1 {
		return cfg, fmt.Errorf("DOMAIN_CONCURRENCY_LIMIT must be at least 1")
	}
	if host, port, err := net.SplitHostPort(cfg.SMTPAddr); err != nil || host == "" || port == "" {
		return cfg, fmt.Errorf("SMTP_ADDR must be a host:port address")
	}
	if _, err := mail.ParseAddress(cfg.SMTPFrom); err != nil {
		return cfg, fmt.Errorf("SMTP_FROM must be a valid email address")
	}

	return cfg, nil
}

func getenv(lookup func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(lookup(key)); v != "" {
		return v
	}
	return fallback
}

func getenvBool(lookup func(string) string, key string, fallback bool) (bool, error) {
	v := strings.TrimSpace(lookup(key))
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return b, nil
}

func getenvInt(lookup func(string) string, key string, fallback int) (int, error) {
	v := strings.TrimSpace(lookup(key))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func getenvDuration(lookup func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(lookup(key))
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return d, nil
}

func getenvFloat(lookup func(string) string, key string, fallback float64) (float64, error) {
	v := strings.TrimSpace(lookup(key))
	if v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a float: %w", key, err)
	}
	return f, nil
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
