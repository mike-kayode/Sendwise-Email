package config

import (
	"fmt"
	"net"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

type Config struct {
	Env                     string
	HTTPAddr                string
	WorkerMetricsAddr       string
	AppBaseURL              string
	APIBaseURL              string
	TrackingBaseURL         string
	DatabaseURL             string
	RedisURL                string
	NATSURL                 string
	MigrationsDir           string
	AutoMigrate             bool
	JWTSecret               string
	JWTIssuer               string
	IdentityURL             string
	ServiceToken            string
	AccessTokenTTL          time.Duration
	SMTPAddr                string
	SMTPHost                string
	SMTPUsername            string
	SMTPPassword            string
	SMTPFrom                string
	DKIMSelector            string
	DKIMEncryptionKey       string
	SPFInclude              string
	DNSResolverAddr         string
	DNSResolverTimeout      time.Duration
	SendingRate             int
	MaxDeliveryAttempts     int
	SchedulerInterval       time.Duration
	MaxMessageAge           time.Duration
	MaxDailyEmails          int
	WarmupStartPerDay       int
	WarmupGrowthFactor      float64
	MaxBounceRate           float64
	MaxComplaintRate        float64
	DomainConcurrencyLimit  int
	RecipientDomainRates    map[string]int
	RecipientDomainDefault  int
	CORSOrigins             []string
	RequireVerifiedDomain   bool
	MailProvider            string
	ResendAPIKey            string
	ResendFromEmail         string
	ResendFromName          string
	PlatformFromEmail       string
	PlatformResendAPIKey    string
	ResendWebhookSecret     string
	ResendRequestsPerSecond int
	SuperAdminEmails        []string
	WebhookSecret           string
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
	resendRequestsPerSecond, err := getenvInt(lookup, "RESEND_REQUESTS_PER_SECOND", 8)
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
	dnsResolverTimeout, err := getenvDuration(lookup, "DNS_RESOLVER_TIMEOUT", 2*time.Second)
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
	warmupStartPerDay, err := getenvInt(lookup, "WARMUP_START_PER_DAY", 50)
	if err != nil {
		return Config{}, err
	}
	warmupGrowthFactor, err := getenvFloat(lookup, "WARMUP_GROWTH_FACTOR", 1.5)
	if err != nil {
		return Config{}, err
	}
	recipientDomainRates, recipientDomainDefault, err := parseRecipientDomainRates(
		getenv(lookup, "RECIPIENT_DOMAIN_RATE_LIMITS", "default=10"),
	)
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
	webhookSecret := getenv(lookup, "WEBHOOK_SECRET", "")
	resendWebhookSecret := getenv(lookup, "RESEND_WEBHOOK_SECRET", "")
	platformFromEmail := getenv(lookup, "PLATFORM_FROM_EMAIL", getenv(lookup, "SMTP_FROM", "hello@pipsleague.com"))

	cfg := Config{
		Env:                     env,
		HTTPAddr:                getenv(lookup, "HTTP_ADDR", ":8080"),
		WorkerMetricsAddr:       getenv(lookup, "WORKER_METRICS_ADDR", ":9091"),
		AppBaseURL:              getenv(lookup, "APP_BASE_URL", "http://localhost:3000"),
		APIBaseURL:              getenv(lookup, "API_BASE_URL", "http://localhost:8080"),
		TrackingBaseURL:         getenv(lookup, "TRACKING_BASE_URL", "http://localhost:8080"),
		DatabaseURL:             getenv(lookup, "DATABASE_URL", "postgres://email:email@localhost:5432/email?sslmode=disable"),
		RedisURL:                getenv(lookup, "REDIS_URL", "redis://localhost:6379/0"),
		NATSURL:                 getenv(lookup, "NATS_URL", "nats://localhost:4222"),
		MigrationsDir:           getenv(lookup, "MIGRATIONS_DIR", "./migrations"),
		AutoMigrate:             autoMigrate,
		JWTSecret:               getenv(lookup, "JWT_SECRET", ""),
		JWTIssuer:               getenv(lookup, "JWT_ISSUER", "email-platform"),
		IdentityURL:             strings.TrimRight(getenv(lookup, "IDENTITY_URL", ""), "/"),
		ServiceToken:            strings.TrimSpace(getenv(lookup, "SERVICE_TOKEN", "")),
		AccessTokenTTL:          accessTokenTTL,
		SMTPAddr:                getenv(lookup, "SMTP_ADDR", "localhost:587"),
		SMTPHost:                getenv(lookup, "SMTP_HOST", "localhost"),
		SMTPUsername:            getenv(lookup, "SMTP_USERNAME", ""),
		SMTPPassword:            getenv(lookup, "SMTP_PASSWORD", ""),
		SMTPFrom:                getenv(lookup, "SMTP_FROM", "newsletter@example.com"),
		DKIMSelector:            getenv(lookup, "DKIM_SELECTOR", "mail"),
		DKIMEncryptionKey:       getenv(lookup, "DKIM_ENCRYPTION_KEY", ""),
		SPFInclude:              strings.TrimSpace(getenv(lookup, "SPF_INCLUDE", "")),
		DNSResolverAddr:         getenv(lookup, "DNS_RESOLVER_ADDR", ""),
		DNSResolverTimeout:      dnsResolverTimeout,
		SendingRate:             sendingRate,
		MaxDeliveryAttempts:     maxDeliveryAttempts,
		SchedulerInterval:       schedulerInterval,
		MaxMessageAge:           maxMessageAge,
		MaxDailyEmails:          maxDailyEmails,
		WarmupStartPerDay:       warmupStartPerDay,
		WarmupGrowthFactor:      warmupGrowthFactor,
		MaxBounceRate:           maxBounceRate,
		MaxComplaintRate:        maxComplaintRate,
		DomainConcurrencyLimit:  domainConcurrencyLimit,
		RecipientDomainRates:    recipientDomainRates,
		RecipientDomainDefault:  recipientDomainDefault,
		CORSOrigins:             splitCSV(getenv(lookup, "CORS_ORIGINS", "http://localhost:3000")),
		RequireVerifiedDomain:   requireVerifiedDomain,
		MailProvider:            strings.ToLower(strings.TrimSpace(getenv(lookup, "MAIL_PROVIDER", "auto"))),
		ResendAPIKey:            resendAPIKey,
		ResendFromEmail:         resendFromEmail,
		ResendFromName:          resendFromName,
		PlatformFromEmail:       platformFromEmail,
		PlatformResendAPIKey:    getenv(lookup, "PLATFORM_RESEND_API_KEY", ""),
		ResendWebhookSecret:     resendWebhookSecret,
		ResendRequestsPerSecond: resendRequestsPerSecond,
		SuperAdminEmails:        superAdminEmails,
		WebhookSecret:           webhookSecret,
	}

	switch cfg.MailProvider {
	case "auto", "smtp", "resend", "log":
	default:
		return cfg, fmt.Errorf("MAIL_PROVIDER must be auto, smtp, resend, or log, got %q", cfg.MailProvider)
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
		if cfg.IdentityURL == "" || cfg.ServiceToken == "" {
			return cfg, fmt.Errorf("IDENTITY_URL and SERVICE_TOKEN are required in staging and production for session revocation checks")
		}
		if !cfg.RequireVerifiedDomain {
			return cfg, fmt.Errorf("REQUIRE_VERIFIED_DOMAIN must be true in staging and production")
		}
		// Campaign delivery always uses SMTP, so a secure environment that
		// still points at Mailpit or the loopback interface must fail at
		// startup instead of silently discarding production mail.
		if err := kit.ValidateProductionMail(env, cfg.MailProvider, cfg.SMTPAddr, cfg.SMTPHost); err != nil {
			return cfg, err
		}
		if cfg.SMTPUsername == "" || cfg.SMTPPassword == "" {
			return cfg, fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD are required in staging and production")
		}
		if cfg.WebhookSecret == "" {
			return cfg, fmt.Errorf("WEBHOOK_SECRET is required in staging and production")
		}
		if cfg.ResendWebhookSecret == "" {
			return cfg, fmt.Errorf("RESEND_WEBHOOK_SECRET is required in staging and production")
		}
	}
	if cfg.AccessTokenTTL <= 0 || (secureEnv && cfg.AccessTokenTTL > time.Hour) {
		return cfg, fmt.Errorf("ACCESS_TOKEN_TTL must be positive and no longer than 1h in staging and production")
	}
	if cfg.SendingRate < 1 {
		return cfg, fmt.Errorf("SENDING_RATE_PER_SECOND must be at least 1")
	}
	if cfg.ResendRequestsPerSecond < 1 {
		return cfg, fmt.Errorf("RESEND_REQUESTS_PER_SECOND must be at least 1")
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
	if cfg.WarmupStartPerDay < 1 {
		return cfg, fmt.Errorf("WARMUP_START_PER_DAY must be at least 1")
	}
	if cfg.WarmupGrowthFactor < 1 {
		return cfg, fmt.Errorf("WARMUP_GROWTH_FACTOR must be at least 1")
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
	if cfg.RecipientDomainDefault < 1 {
		return cfg, fmt.Errorf("RECIPIENT_DOMAIN_RATE_LIMITS must set a positive default rate")
	}
	if host, port, err := net.SplitHostPort(cfg.SMTPAddr); err != nil || host == "" || port == "" {
		return cfg, fmt.Errorf("SMTP_ADDR must be a host:port address")
	}
	if _, err := mail.ParseAddress(cfg.SMTPFrom); err != nil {
		return cfg, fmt.Errorf("SMTP_FROM must be a valid email address")
	}
	if _, err := mail.ParseAddress(cfg.PlatformFromEmail); err != nil {
		return cfg, fmt.Errorf("PLATFORM_FROM_EMAIL must be a valid email address")
	}
	if cfg.DNSResolverTimeout <= 0 {
		cfg.DNSResolverTimeout = 2 * time.Second
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

func parseRecipientDomainRates(value string) (map[string]int, int, error) {
	rates := make(map[string]int)
	for _, entry := range splitCSV(value) {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, 0, fmt.Errorf("RECIPIENT_DOMAIN_RATE_LIMITS entries must use domain=messages_per_second")
		}
		domain := strings.ToLower(strings.TrimSpace(parts[0]))
		rate, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || rate < 1 {
			return nil, 0, fmt.Errorf("RECIPIENT_DOMAIN_RATE_LIMITS rate for %s must be a positive integer", domain)
		}
		if domain == "default" {
			rates[domain] = rate
			continue
		}
		if strings.ContainsAny(domain, "/:@ ") {
			return nil, 0, fmt.Errorf("RECIPIENT_DOMAIN_RATE_LIMITS contains invalid domain %q", domain)
		}
		rates[domain] = rate
	}
	defaultRate := rates["default"]
	if defaultRate == 0 {
		return nil, 0, fmt.Errorf("RECIPIENT_DOMAIN_RATE_LIMITS must include default=<messages_per_second>")
	}
	return rates, defaultRate, nil
}
