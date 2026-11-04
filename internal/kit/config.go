package kit

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Getenv returns the value of the environment variable key, or fallback if unset or empty.
func Getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetenvInt returns the integer value of the environment variable key, or fallback if unset or invalid.
func GetenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// GetenvDuration returns the duration value of the environment variable key, or fallback if unset or invalid.
func GetenvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// GetenvBool returns the boolean value of the environment variable key, or fallback if unset or unrecognized.
func GetenvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

// Required returns the value of the environment variable key, or an error if unset or empty.
func Required(key string) (string, error) {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v, nil
	}
	return "", fmt.Errorf("required environment variable %q is not set", key)
}

// BaseConfig holds configuration common to every Sendwise microservice.
type BaseConfig struct {
	ServiceName string
	Env         string
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
	RedisURL    string
	JWTSecret   string
	JWTIssuer   string
	LogLevel    string
}

// LoadBaseConfig reads common service configuration from environment variables.
func LoadBaseConfig(serviceName string) BaseConfig {
	return BaseConfig{
		ServiceName: serviceName,
		Env:         Getenv("APP_ENV", "development"),
		HTTPAddr:    Getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: Getenv("DATABASE_URL", "postgres://email:email@localhost:5432/email?sslmode=disable"),
		NATSURL:     Getenv("NATS_URL", "nats://localhost:4222"),
		RedisURL:    Getenv("REDIS_URL", "redis://localhost:6379/0"),
		JWTSecret:   Getenv("JWT_SECRET", ""),
		JWTIssuer:   Getenv("JWT_ISSUER", "email-platform"),
		LogLevel:    Getenv("LOG_LEVEL", "info"),
	}
}
