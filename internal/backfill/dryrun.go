package backfill

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DryRun reports email clashes and the legacy columns a re-key would move.
// It performs catalog and data reads only; it does not open a write
// transaction or modify any source or legacy records.
func DryRun(ctx context.Context, identity, workspace, legacy *pgxpool.Pool, out io.Writer) error {
	if out == nil {
		return fmt.Errorf("dry-run output writer is nil")
	}

	tx, err := legacy.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin read-only legacy inspection: %w", err)
	}
	defer tx.Rollback(ctx)

	refs, err := userReferencingColumns(ctx, tx)
	if err != nil {
		return fmt.Errorf("inspect legacy user reference columns: %w", err)
	}

	rows, err := identity.Query(ctx, `SELECT id, email FROM users ORDER BY id`)
	if err != nil {
		return fmt.Errorf("query identity users for dry-run: %w", err)
	}
	defer rows.Close()

	var clashes, rekeys, skips int
	fmt.Fprintln(out, "Backfill dry run (read-only)")
	for rows.Next() {
		var identityID, email string
		if err := rows.Scan(&identityID, &email); err != nil {
			return fmt.Errorf("scan identity user for dry-run: %w", err)
		}
		var legacyID string
		err := tx.QueryRow(ctx, `
			SELECT id FROM users
			WHERE lower(email) = lower($1) AND id <> $2
		`, email, identityID).Scan(&legacyID)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("find legacy email clash for identity user %s: %w", identityID, err)
		}

		clashes++
		var identityIDOccupied bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, identityID,
		).Scan(&identityIDOccupied); err != nil {
			return fmt.Errorf("check identity id occupancy for %s: %w", identityID, err)
		}

		if identityIDOccupied {
			skips++
			fmt.Fprintf(out, "CLASH identity_user_id=%s legacy_user_id=%s email=%s outcome=skip reason=identity_id_already_exists\n",
				identityID, legacyID, email)
			continue
		}

		rekeys++
		fmt.Fprintf(out, "CLASH identity_user_id=%s legacy_user_id=%s email=%s outcome=re-key\n",
			identityID, legacyID, email)
		fmt.Fprintln(out, "  UPDATE users.email to free the address; copy users.id,email,password_hash,name,status,email_verified_at,last_login_at,created_at,updated_at to the identity ID")
		fmt.Fprintf(out, "  UPDATE users.id: %s -> %s; DELETE shadowed users.id=%s\n",
			legacyID, identityID, legacyID)
		for _, ref := range refs {
			kind := "unconstrained UUID candidate"
			if ref.foreignKey {
				kind = "foreign key"
			}
			fmt.Fprintf(out, "  UPDATE %s.%s.%s (%s): %s -> %s\n",
				ref.schema, ref.table, ref.column, kind, legacyID, identityID)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate identity users for dry-run: %w", err)
	}

	// Report unconstrained UUID candidates even if no email clash currently
	// requires re-keying, so operators can review the schema before rollout.
	var candidates []string
	for _, ref := range refs {
		if !ref.foreignKey {
			candidates = append(candidates, ref.schema+"."+ref.table+"."+ref.column)
		}
	}
	if len(candidates) == 0 {
		fmt.Fprintln(out, "Unconstrained UUID user-reference columns: none")
	} else {
		fmt.Fprintf(out, "Unconstrained UUID user-reference columns: %s\n", strings.Join(candidates, ", "))
	}

	// Keep the workspace handle in the API to make it explicit that dry-run
	// covers the complete backfill source set; reading its tables checks that
	// the configured workspace database is reachable without changing it.
	var organizationCount, membershipCount int64
	if err := workspace.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&organizationCount); err != nil {
		return fmt.Errorf("count workspace organizations for dry-run: %w", err)
	}
	if err := workspace.QueryRow(ctx, `SELECT count(*) FROM memberships`).Scan(&membershipCount); err != nil {
		return fmt.Errorf("count workspace memberships for dry-run: %w", err)
	}
	fmt.Fprintf(out, "SUMMARY email_clashes=%d rekeys=%d skips=%d workspace_organizations=%d workspace_memberships=%d\n",
		clashes, rekeys, skips, organizationCount, membershipCount)
	return nil
}
