package api

import (
	"context"
	"net/http"

	"github.com/khayode/email-platform/internal/auth"
)

func (s *Server) ensureActorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims := s.claims(r); claims != nil {
			s.ensureActorRows(r.Context(), claims)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ensureActorRows(ctx context.Context, claims *auth.Claims) {
	if claims.Subject != "" {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, name, status)
			VALUES ($1, $2, 'identity-managed', 'Synced user', 'active')
			ON CONFLICT (id) DO NOTHING
		`, claims.Subject, claims.Subject+"@synced.local"); err != nil {
			s.logger.Warn("ensure legacy user row", "user_id", claims.Subject, "error", err)
		}
	}
	if claims.OrganizationID != "" {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO organizations (id, name, slug)
			VALUES ($1, $2, $3)
			ON CONFLICT (id) DO NOTHING
		`, claims.OrganizationID, "Synced organization", "synced-"+claims.OrganizationID); err != nil {
			s.logger.Warn("ensure legacy organization row", "organization_id", claims.OrganizationID, "error", err)
		}
	}
}
