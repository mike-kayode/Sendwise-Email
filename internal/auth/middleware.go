package auth

import (
	"context"
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
