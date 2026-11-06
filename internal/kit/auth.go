package kit

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims are the JWT claims issued by the Identity service. The JSON tags are
// intentionally compatible with the legacy internal/auth.Claims so tokens are
// interchangeable between the monolith and the extracted services.
type Claims struct {
	Subject        string `json:"sub"`
	Email          string `json:"email,omitempty"`
	OrganizationID string `json:"org,omitempty"`
	Role           string `json:"role,omitempty"`
	TokenType      string `json:"typ,omitempty"` // "access" or "refresh"
	SessionID      string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// Manager issues and validates JWTs.
type Manager struct {
	secret       []byte
	issuer       string
	ttl          time.Duration
	sessionStore SessionStore
}

// SessionStore checks whether an access-token session is still active.
type SessionStore interface {
	Active(context.Context, string, string) (bool, error)
}

// NewManager creates a token manager for the given shared secret and issuer.
func NewManager(secret, issuer string, ttl time.Duration) *Manager {
	return &Manager{secret: []byte(secret), issuer: issuer, ttl: ttl}
}

// WithSessionStore returns a manager that checks session revocation in middleware.
func (m *Manager) WithSessionStore(store SessionStore) *Manager {
	return &Manager{secret: m.secret, issuer: m.issuer, ttl: m.ttl, sessionStore: store}
}

// TTL returns the configured access-token lifetime.
func (m *Manager) TTL() time.Duration { return m.ttl }

// IssueAccess creates a short-lived org-scoped access token bound to a session.
func (m *Manager) IssueAccess(subject, email, orgID, role, sessionID string) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Subject:        subject,
		Email:          email,
		OrganizationID: orgID,
		Role:           role,
		TokenType:      "access",
		SessionID:      sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
		},
	})
	return token.SignedString(m.secret)
}

// Parse validates and parses a JWT token string. It restricts the algorithm,
// requires the configured issuer and a non-zero expiry.
func (m *Manager) Parse(raw string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", token.Header["alg"])
		}
		return m.secret, nil
	},
		jwt.WithIssuer(m.issuer),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, err
	}
	return claims, nil
}

// Middleware enforces bearer JWT authentication and attaches claims to context.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			Error(w, http.StatusUnauthorized, "unauthorized", "A bearer token is required.")
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		claims, err := m.Parse(raw)
		if err != nil {
			Error(w, http.StatusUnauthorized, "invalid_token", "The access token is invalid or expired.")
			return
		}
		if claims.TokenType != "" && claims.TokenType != "access" {
			Error(w, http.StatusUnauthorized, "invalid_token", "The access token is invalid or expired.")
			return
		}
		if m.sessionStore != nil {
			if claims.SessionID == "" {
				Error(w, http.StatusUnauthorized, "session_revoked", "The session has been revoked. Sign in again.")
				return
			}
			active, err := m.sessionStore.Active(r.Context(), claims.Subject, claims.SessionID)
			if err != nil {
				Error(w, http.StatusServiceUnavailable, "session_check_unavailable", "The session could not be verified.")
				return
			}
			if !active {
				Error(w, http.StatusUnauthorized, "session_revoked", "The session has been revoked. Sign in again.")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}

// OptionalSessionMiddleware validates any bearer token present on a proxied
// request. Requests without a bearer token continue to the owning service.
func (m *Manager) OptionalSessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if header == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(header, "Bearer ") {
			Error(w, http.StatusUnauthorized, "invalid_token", "The access token is invalid or expired.")
			return
		}
		claims, err := m.Parse(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
		if err != nil || (claims.TokenType != "" && claims.TokenType != "access") {
			Error(w, http.StatusUnauthorized, "invalid_token", "The access token is invalid or expired.")
			return
		}
		if m.sessionStore != nil {
			if claims.SessionID == "" {
				Error(w, http.StatusUnauthorized, "session_revoked", "The session has been revoked. Sign in again.")
				return
			}
			active, err := m.sessionStore.Active(r.Context(), claims.Subject, claims.SessionID)
			if err != nil {
				Error(w, http.StatusServiceUnavailable, "session_check_unavailable", "The session could not be verified.")
				return
			}
			if !active {
				Error(w, http.StatusUnauthorized, "session_revoked", "The session has been revoked. Sign in again.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// OrgRequired rejects tokens that are not scoped to an organization.
func OrgRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := FromContext(r.Context())
		if !ok || claims.OrganizationID == "" {
			Error(w, http.StatusForbidden, "missing_organization", "The access token is not associated with an organization.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type claimsKey struct{}

// FromContext extracts Claims from a request context.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(*Claims)
	return claims, ok
}

// RequireRoles returns middleware that requires one of the given roles.
func RequireRoles(roles ...string) Middleware {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok || claims.Role == "" {
				Error(w, http.StatusForbidden, "forbidden", "Your role does not permit this action.")
				return
			}
			if _, ok := allowed[claims.Role]; !ok {
				Error(w, http.StatusForbidden, "forbidden", "Your role does not permit this action.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
