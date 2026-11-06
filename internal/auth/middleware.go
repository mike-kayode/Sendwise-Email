package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/khayode/email-platform/internal/httpx"
)

type contextKey string

const claimsKey contextKey = "auth.claims"

const (
	RoleOwner   = "owner"
	RoleAdmin   = "admin"
	RoleManager = "manager"
	RoleAuthor  = "author"
	RoleAnalyst = "analyst"
	RoleViewer  = "viewer"
)

// SessionStore is the interface used by the auth middleware to check whether a
// presented access-token session is still active. Only the service owning the
// session records (Identity) implements this; the legacy API implements it
// against the shared revocation store as a compatibility bridge.
type SessionStore interface {
	// Active reports whether the session identified by sessionID belongs to
	// the given user and has not been revoked.
	Active(ctx context.Context, userID, sessionID string) (bool, error)
}

// DefaultSessionStore is a no-op implementation used when session revocation
// is not configured. To force immediate revocation, configure a real store.
type DefaultSessionStore struct{}

func (d *DefaultSessionStore) Active(ctx context.Context, userID, sessionID string) (bool, error) {
	return true, nil
}

var errSessionNotFound = errors.New("session not found")

// NullSessionStore returns a SessionStore that always reports sessions active.
// Use it only when no revocation store is configured.
func NullSessionStore() SessionStore { return &DefaultSessionStore{} }

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "A bearer token is required.")
			return
		}

		raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		claims, err := m.Parse(raw)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, "invalid_token", "The access token is invalid or expired.")
			return
		}
		if claims.OrganizationID == "" {
			httpx.Error(w, http.StatusForbidden, "missing_organization", "The access token is not associated with an organization.")
			return
		}

		// Check session revocation immediately after token validation.
		if m.sessionStore != nil {
			if claims.SessionID == "" {
				httpx.Error(w, http.StatusUnauthorized, "session_revoked", "The session has been revoked. Sign in again.")
				return
			}
			active, err := m.sessionStore.Active(r.Context(), claims.Subject, claims.SessionID)
			if err != nil {
				m.logger.Error("session check failed", "error", err)
				httpx.Error(w, http.StatusServiceUnavailable, "session_check_unavailable", "The session could not be verified.")
				return
			}
			if !active {
				httpx.Error(w, http.StatusUnauthorized, "session_revoked", "The session has been revoked. Sign in again.")
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	})
}

func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey).(*Claims)
	return claims, ok
}

func OrganizationID(ctx context.Context) string {
	if claims, ok := FromContext(ctx); ok {
		return claims.OrganizationID
	}
	return ""
}

func UserID(ctx context.Context) string {
	if claims, ok := FromContext(ctx); ok {
		return claims.Subject
	}
	return ""
}

func Role(ctx context.Context) string {
	if claims, ok := FromContext(ctx); ok {
		return claims.Role
	}
	return ""
}

func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := allowed[Role(r.Context())]; !ok {
				httpx.Error(w, http.StatusForbidden, "forbidden", "Your role does not permit this action.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
