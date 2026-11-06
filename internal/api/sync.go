package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/httpx"
)

func (s *Server) ensureActorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims := s.claims(r); claims != nil {
			if err := s.ensureActorRows(r.Context(), claims); err != nil {
				s.logger.Error("ensure legacy actor projection", "user_id", claims.Subject,
					"organization_id", claims.OrganizationID, "error", err)
				httpx.Error(w, http.StatusInternalServerError, "actor_sync_failed",
					"Your account could not be synchronized. Please try again.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ensureActorRows(ctx context.Context, claims *auth.Claims) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin actor projection: %w", err)
	}
	defer tx.Rollback(ctx)

	if claims.Subject != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, name, status)
			VALUES ($1::uuid, COALESCE(NULLIF($2, ''), $1::uuid::text || '@synced.local'), 'identity-managed', 'Synced user', 'active')
			ON CONFLICT (id) DO UPDATE
			SET email = CASE
				WHEN users.email LIKE '%@synced.local' THEN EXCLUDED.email
				ELSE users.email
			END
		`, claims.Subject, claims.Email); err != nil {
			return fmt.Errorf("ensure legacy user row: %w", err)
		}
	}
	if claims.OrganizationID != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO organizations (id, name, slug)
			VALUES ($1, $2, $3)
			ON CONFLICT (id) DO UPDATE
			SET name = CASE WHEN organizations.name = 'Synced organization' THEN EXCLUDED.name ELSE organizations.name END
		`, claims.OrganizationID, "Synced organization", "synced-"+claims.OrganizationID); err != nil {
			return fmt.Errorf("ensure legacy organization row: %w", err)
		}
	}
	if claims.Subject != "" && claims.OrganizationID != "" {
		role := claims.Role
		switch role {
		case auth.RoleOwner, auth.RoleAdmin, auth.RoleManager, auth.RoleAuthor, auth.RoleAnalyst, auth.RoleViewer:
		default:
			role = auth.RoleViewer
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO organization_members (organization_id, user_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, user_id) DO UPDATE SET role = EXCLUDED.role
		`, claims.OrganizationID, claims.Subject, role); err != nil {
			return fmt.Errorf("ensure legacy membership row: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit actor projection: %w", err)
	}
	return nil
}
