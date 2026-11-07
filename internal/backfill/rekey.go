package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// identifierPattern guards catalog-sourced identifiers before they are
// interpolated into SQL.
var identifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// referencedColumn is one foreign-key column that points at users(id).
type referencedColumn struct {
	schema     string
	table      string
	column     string
	foreignKey bool
}

// RekeyLegacyUser reconciles a lower(email) clash before a caller projects an
// identity user into the legacy users table.
//
// When a legacy row already holds the email under a different id, that row is
// re-keyed onto identityUserID inside a savepoint: the email is freed, the row
// is copied onto the identity id, every foreign key referencing users(id)
// (discovered through information_schema) is moved, and the shadowed legacy
// row is deleted. When re-keying is unsafe — the identity id is already taken
// by another legacy row, or any step fails — the pair is recorded in
// backfill_user_id_map and the caller must skip the projection. A clash is
// data, not a fault, so this never returns an error for one; the caller's
// normal upsert is safe to run whenever it returns true.
func RekeyLegacyUser(ctx context.Context, tx pgx.Tx, identityUserID, email string, logger *slog.Logger) (bool, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if strings.TrimSpace(email) == "" {
		return true, nil
	}

	var legacyUserID string
	err := tx.QueryRow(ctx, `
		SELECT id FROM users
		WHERE lower(email) = lower($1) AND id <> $2
	`, email, identityUserID).Scan(&legacyUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil // nobody else holds the address: the upsert is safe
	}
	if err != nil {
		return false, fmt.Errorf("find legacy holder of %q: %w", email, err)
	}

	reason := ""
	if occupied, err := identityIDOccupied(ctx, tx, identityUserID); err != nil {
		return false, err
	} else if occupied {
		// Re-keying would collide with an existing legacy row on the primary
		// key, so the pair is recorded and the projection is skipped.
		reason = "identity id already exists in the legacy users table"
	} else if err := rekeyInSavepoint(ctx, tx, identityUserID, legacyUserID, email); err != nil {
		reason = fmt.Sprintf("re-key failed: %v", err)
	} else {
		logger.Info("legacy email clash: re-keyed legacy user onto identity id",
			"identity_user_id", identityUserID, "legacy_user_id", legacyUserID, "email", email)
		return true, nil
	}

	if err := recordUserMapping(ctx, tx, identityUserID, legacyUserID, email, reason); err != nil {
		return false, err
	}
	logger.Warn("legacy email clash: identity user skipped; pair recorded in backfill_user_id_map",
		"identity_user_id", identityUserID, "legacy_user_id", legacyUserID,
		"email", email, "reason", reason)
	return false, nil
}

func identityIDOccupied(ctx context.Context, tx pgx.Tx, identityUserID string) (bool, error) {
	var occupied bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, identityUserID,
	).Scan(&occupied); err != nil {
		return false, fmt.Errorf("check identity id occupancy: %w", err)
	}
	return occupied, nil
}

// rekeyInSavepoint performs the re-key on a nested transaction (a Postgres
// savepoint) so a failed attempt leaves the caller's transaction usable.
func rekeyInSavepoint(ctx context.Context, tx pgx.Tx, identityUserID, legacyUserID, email string) error {
	nested, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin re-key savepoint: %w", err)
	}
	rekeyErr := rekeyLegacyUserRow(ctx, nested, identityUserID, legacyUserID, email)
	if rekeyErr != nil {
		// ROLLBACK TO SAVEPOINT: the half-moved row and freed email are undone.
		if rollbackErr := nested.Rollback(ctx); rollbackErr != nil {
			return fmt.Errorf("roll back re-key after %v: %w", rekeyErr, rollbackErr)
		}
		return rekeyErr
	}
	if err := nested.Commit(ctx); err != nil { // RELEASE SAVEPOINT
		return fmt.Errorf("release re-key savepoint: %w", err)
	}
	return nil
}

// rekeyLegacyUserRow moves the legacy row that holds email (and every foreign
// key that references it) onto identityUserID. The identity id does not exist
// yet when this runs, so the row is copied onto it first and the shadowed
// legacy row is deleted last.
func rekeyLegacyUserRow(ctx context.Context, tx pgx.Tx, identityUserID, legacyUserID, email string) error {
	// Free the address first: the copy below re-uses it under the identity id.
	shadowEmail := "rekey-" + legacyUserID + "@rekey.invalid"
	if _, err := tx.Exec(ctx,
		`UPDATE users SET email = $1 WHERE id = $2`, shadowEmail, legacyUserID,
	); err != nil {
		return fmt.Errorf("free legacy email: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status, email_verified_at,
		                   last_login_at, created_at, updated_at)
		SELECT $1, $2, u.password_hash, u.name, u.status, u.email_verified_at,
		       u.last_login_at, u.created_at, u.updated_at
		FROM users u WHERE u.id = $3
	`, identityUserID, email, legacyUserID); err != nil {
		return fmt.Errorf("copy legacy row onto identity id: %w", err)
	}

	refs, err := userReferencingColumns(ctx, tx)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		stmt := fmt.Sprintf("UPDATE %s.%s SET %s = $1 WHERE %s = $2",
			pgx.Identifier{ref.schema}.Sanitize(),
			pgx.Identifier{ref.table}.Sanitize(),
			pgx.Identifier{ref.column}.Sanitize(),
			pgx.Identifier{ref.column}.Sanitize(),
		)
		if _, err := tx.Exec(ctx, stmt, identityUserID, legacyUserID); err != nil {
			return fmt.Errorf("move %s.%s references: %w", ref.table, ref.column, err)
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, legacyUserID); err != nil {
		return fmt.Errorf("delete shadowed legacy user: %w", err)
	}
	return nil
}

// userReferencingColumns lists declared foreign keys to users(id) and UUID
// columns with conventional user-reference names that have no such foreign key.
// The latter are moved as well so older tables without constraints do not keep
// pointing at the removed legacy user ID.
func userReferencingColumns(ctx context.Context, tx pgx.Tx) ([]referencedColumn, error) {
	rows, err := tx.Query(ctx, `
		SELECT kcu.table_schema, kcu.table_name, kcu.column_name, true
		FROM information_schema.table_constraints tc
		JOIN information_schema.key_column_usage kcu
		  ON  kcu.constraint_schema = tc.constraint_schema
		  AND kcu.constraint_name   = tc.constraint_name
		  AND kcu.table_schema      = tc.table_schema
		  AND kcu.table_name        = tc.table_name
		JOIN information_schema.constraint_column_usage ccu
		  ON  ccu.constraint_schema = tc.constraint_schema
		  AND ccu.constraint_name   = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY'
		  AND tc.table_schema   = current_schema()
		  AND ccu.table_schema  = current_schema()
		  AND ccu.table_name    = 'users'
		UNION
		SELECT columns.table_schema, columns.table_name, columns.column_name, false
		FROM information_schema.columns columns
		WHERE columns.table_schema = current_schema()
		  AND columns.udt_name = 'uuid'
		  AND (
		    columns.column_name = 'user_id'
		    OR right(columns.column_name, 8) = '_user_id'
		    OR columns.column_name IN ('owner_id', 'created_by', 'actor_id')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM information_schema.table_constraints tc
		    JOIN information_schema.key_column_usage kcu
		      ON  kcu.constraint_schema = tc.constraint_schema
		      AND kcu.constraint_name   = tc.constraint_name
		      AND kcu.table_schema      = tc.table_schema
		      AND kcu.table_name        = tc.table_name
		    JOIN information_schema.constraint_column_usage ccu
		      ON  ccu.constraint_schema = tc.constraint_schema
		      AND ccu.constraint_name   = tc.constraint_name
		    WHERE tc.constraint_type = 'FOREIGN KEY'
		      AND tc.table_schema = columns.table_schema
		      AND kcu.table_name = columns.table_name
		      AND kcu.column_name = columns.column_name
		      AND ccu.table_schema = current_schema()
		      AND ccu.table_name = 'users'
		  )
		ORDER BY 1, 2, 3
	`)
	if err != nil {
		return nil, fmt.Errorf("list foreign keys to users: %w", err)
	}
	defer rows.Close()

	var refs []referencedColumn
	for rows.Next() {
		var ref referencedColumn
		if err := rows.Scan(&ref.schema, &ref.table, &ref.column, &ref.foreignKey); err != nil {
			return nil, fmt.Errorf("scan foreign key to users: %w", err)
		}
		if !identifierPattern.MatchString(ref.schema) || !identifierPattern.MatchString(ref.table) || !identifierPattern.MatchString(ref.column) {
			return nil, fmt.Errorf("unexpected identifier from catalog: %q.%q.%q", ref.schema, ref.table, ref.column)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate foreign keys to users: %w", err)
	}
	return refs, nil
}

// recordUserMapping stores a pair that could not be re-keyed so the clash is
// auditable and the run stays idempotent.
func recordUserMapping(ctx context.Context, tx pgx.Tx, identityUserID, legacyUserID, email, reason string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO backfill_user_id_map (identity_user_id, legacy_user_id, email, reason)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (identity_user_id) DO UPDATE SET
			legacy_user_id = EXCLUDED.legacy_user_id,
			email          = EXCLUDED.email,
			reason         = EXCLUDED.reason
	`, identityUserID, legacyUserID, email, reason); err != nil {
		return fmt.Errorf("record user id mapping: %w", err)
	}
	return nil
}
