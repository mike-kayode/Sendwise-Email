package backfill

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Summary struct {
	Users         int64
	Organizations int64
	Memberships   int64
}

// Run projects Identity and Workspace's authoritative records into the legacy
// database in one transaction. Re-running it updates the same IDs in place.
func Run(ctx context.Context, identity, workspace, legacy *pgxpool.Pool) (Summary, error) {
	tx, err := legacy.Begin(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("begin legacy projection transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var summary Summary
	users, err := identity.Query(ctx, `
		SELECT id, email, password_hash, name, status, email_verified_at,
		       last_login_at, created_at, updated_at
		FROM users ORDER BY id
	`)
	if err != nil {
		return summary, fmt.Errorf("query identity users: %w", err)
	}
	for users.Next() {
		var id, email, passwordHash, name, status string
		var verifiedAt, lastLoginAt *time.Time
		var createdAt, updatedAt time.Time
		if err := users.Scan(&id, &email, &passwordHash, &name, &status, &verifiedAt,
			&lastLoginAt, &createdAt, &updatedAt); err != nil {
			users.Close()
			return summary, fmt.Errorf("scan identity user: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO users (id, email, password_hash, name, status, email_verified_at,
			                   last_login_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (id) DO UPDATE SET
				email = EXCLUDED.email,
				password_hash = EXCLUDED.password_hash,
				name = EXCLUDED.name,
				status = EXCLUDED.status,
				email_verified_at = EXCLUDED.email_verified_at,
				last_login_at = EXCLUDED.last_login_at,
				updated_at = EXCLUDED.updated_at
		`, id, email, passwordHash, name, status, verifiedAt, lastLoginAt, createdAt, updatedAt)
		if err != nil {
			users.Close()
			return summary, fmt.Errorf("project identity user %s: %w", id, err)
		}
		summary.Users++
	}
	if err := users.Err(); err != nil {
		users.Close()
		return summary, fmt.Errorf("iterate identity users: %w", err)
	}
	users.Close()

	orgs, err := workspace.Query(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM organizations ORDER BY id
	`)
	if err != nil {
		return summary, fmt.Errorf("query workspace organizations: %w", err)
	}
	for orgs.Next() {
		var id, name, slug string
		var createdAt, updatedAt time.Time
		if err := orgs.Scan(&id, &name, &slug, &createdAt, &updatedAt); err != nil {
			orgs.Close()
			return summary, fmt.Errorf("scan workspace organization: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO organizations (id, name, slug, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				slug = EXCLUDED.slug,
				updated_at = EXCLUDED.updated_at
		`, id, name, slug, createdAt, updatedAt)
		if err != nil {
			orgs.Close()
			return summary, fmt.Errorf("project workspace organization %s: %w", id, err)
		}
		summary.Organizations++
	}
	if err := orgs.Err(); err != nil {
		orgs.Close()
		return summary, fmt.Errorf("iterate workspace organizations: %w", err)
	}
	orgs.Close()

	memberships, err := workspace.Query(ctx, `
		SELECT organization_id, user_id, role, created_at
		FROM memberships ORDER BY organization_id, user_id
	`)
	if err != nil {
		return summary, fmt.Errorf("query workspace memberships: %w", err)
	}
	for memberships.Next() {
		var organizationID, userID, role string
		var createdAt time.Time
		if err := memberships.Scan(&organizationID, &userID, &role, &createdAt); err != nil {
			memberships.Close()
			return summary, fmt.Errorf("scan workspace membership: %w", err)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO organization_members (organization_id, user_id, role, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (organization_id, user_id) DO UPDATE SET
				role = EXCLUDED.role,
				created_at = EXCLUDED.created_at
		`, organizationID, userID, role, createdAt)
		if err != nil {
			memberships.Close()
			return summary, fmt.Errorf("project membership %s/%s: %w", organizationID, userID, err)
		}
		summary.Memberships++
	}
	if err := memberships.Err(); err != nil {
		memberships.Close()
		return summary, fmt.Errorf("iterate workspace memberships: %w", err)
	}
	memberships.Close()

	if err := tx.Commit(ctx); err != nil {
		return summary, fmt.Errorf("commit legacy projection transaction: %w", err)
	}
	return summary, nil
}
