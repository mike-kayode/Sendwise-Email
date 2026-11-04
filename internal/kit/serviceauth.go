package kit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// ServiceTokenHeader carries the service-to-service shared secret.
const ServiceTokenHeader = "X-Service-Token"

// ServiceAuth validates the shared service token on internal endpoints.
// It fails closed when no secret is configured. Internal endpoints must only
// be reachable on the private service network.
func ServiceAuth(secret string) Middleware {
	expected := []byte(strings.TrimSpace(secret))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(expected) == 0 {
				Error(w, http.StatusServiceUnavailable, "service_auth_disabled", "Service authentication is not configured.")
				return
			}
			provided := []byte(strings.TrimSpace(r.Header.Get(ServiceTokenHeader)))
			if len(provided) == 0 || !hmac.Equal(provided, expected) {
				Error(w, http.StatusUnauthorized, "invalid_service_token", "A valid service token is required.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WithServiceToken attaches the shared service token to an outgoing request.
func WithServiceToken(r *http.Request, secret string) {
	r.Header.Set(ServiceTokenHeader, strings.TrimSpace(secret))
}

// SignServiceRequest is a helper for tests and clients that builds a request
// with the service token already attached.
func SignServiceRequest(r *http.Request, secret string) *http.Request {
	WithServiceToken(r, secret)
	return r
}

// HashSecret returns a hex-encoded SHA-256 of a secret, useful for storing
// webhook/API credentials at rest.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
