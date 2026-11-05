package api

import (
	"context"
	"net/http"

	"github.com/khayode/email-platform/internal/auth"
)

// ensureActorMiddleware self-heals the legacy users/organizations rows that
// write-path foreign keys (created_by, organization_id) reference.
//
// Identity and Workspace own those records and publish projection events that
// the worker applies asynchronously. When an event is missed (worker downtime,
// recreated NATS stream, deployment on a fresh volume), every insert that
// references the missing row fails with an opaque 500 — e.g.
// "Could not create the template." Running a cheap idempotent upsert before
// each authenticated write makes the API converge regardless of event
// delivery, without weakening the projections (they use ON CONFLICT DO
// NOTHING, so a later real event never collides with the placeholder).
func (s *Server) ensureActorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			// Reads never touch insert foreign keys.
		default:
			if claims := s.claims(r); claims != nil {
				s.ensureActorRows(r.Context(), claims)
			}
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
