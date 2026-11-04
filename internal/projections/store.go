package projections

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/outbox"
)

// Consumer names used in the legacy event_inbox idempotency ledger.
const (
	consumerUserRegistered      = "legacy-user-registered"
	consumerOrganizationCreated = "legacy-organization-created"
)

// ErrOwnerNotProjected indicates the owner's user row does not exist yet, so
// the membership insert would violate a foreign key. The caller should retry.
var ErrOwnerNotProjected = errors.New("owner user is not projected yet")

// UserProjection is the legacy users row derived from identity.user_registered.
type UserProjection struct {
	ID    string
	Email string
	Name  string
}

// OrganizationProjection is the legacy organizations + membership row derived
// from workspace.organization_created.
type OrganizationProjection struct {
	OrganizationID string
	Name           string
	Slug           string
	OwnerUserID    string
}

// Store writes projections into the legacy schema, idempotently keyed by the
// triggering event id.
type Store interface {
	// ProjectUser inserts the legacy users row. It returns projected=false when
	// the email is already owned by a different user id (the event cannot be
	// represented and must not be retried).
	ProjectUser(ctx context.Context, eventID string, user UserProjection) (projected bool, err error)
	// ProjectOrganization inserts the legacy organizations row and the owner
	// membership. It returns ErrOwnerNotProjected when the owner user row is
	// missing so the caller can retry after the user projection lands.
	ProjectOrganization(ctx context.Context, eventID string, org OrganizationProjection) error
}

// pgxStore is the production Store backed by the legacy database.
type pgxStore struct {
	pool *pgxpool.Pool
}

// NewStore adapts the legacy database pool.
func NewStore(pool *pgxpool.Pool) Store {
	return &pgxStore{pool: pool}
}

func (s *pgxStore) ProjectUser(ctx context.Context, eventID string, user UserProjection) (bool, error) {
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		return false, fmt.Errorf("user id must be a UUID: %w", err)
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return false, fmt.Errorf("event id must be a UUID: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin user projection: %w", err)
	}
	defer tx.Rollback(ctx)

	newlyProcessed, err := outbox.MarkProcessed(ctx, tx, consumerUserRegistered, eventID)
	if err != nil {
		return false, fmt.Errorf("record inbox event: %w", err)
	}
	if !newlyProcessed {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit: %w", err)
		}
		return false, nil
	}

	// Identity owns credentials; legacy only needs a row so foreign keys
	// resolve. The placeholder hash can never satisfy a legacy password check.
	tag, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status)
		VALUES ($1, lower($2), $3, $4, 'pending')
		ON CONFLICT DO NOTHING
	`, userID, user.Email, "identity-managed:"+user.ID, user.Name)
	if err != nil {
		return false, fmt.Errorf("insert legacy user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either this event was already applied outside the inbox, or the email
		// belongs to a different (legacy-created) user. Both must not loop.
		var sameUser bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&sameUser); err != nil {
			return false, fmt.Errorf("check legacy user: %w", err)
		}
		if !sameUser {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit: %w", err)
			}
			return false, nil
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit user projection: %w", err)
	}
	return true, nil
}

func (s *pgxStore) ProjectOrganization(ctx context.Context, eventID string, org OrganizationProjection) error {
	orgID, err := uuid.Parse(org.OrganizationID)
	if err != nil {
		return fmt.Errorf("organization id must be a UUID: %w", err)
	}
	ownerID, err := uuid.Parse(org.OwnerUserID)
	if err != nil {
		return fmt.Errorf("owner id must be a UUID: %w", err)
	}
	if _, err := uuid.Parse(eventID); err != nil {
		return fmt.Errorf("event id must be a UUID: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin organization projection: %w", err)
	}
	defer tx.Rollback(ctx)

	newlyProcessed, err := outbox.MarkProcessed(ctx, tx, consumerOrganizationCreated, eventID)
	if err != nil {
		return fmt.Errorf("record inbox event: %w", err)
	}
	if !newlyProcessed {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, orgID, org.Name, org.Slug); err != nil {
		return fmt.Errorf("insert legacy organization: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT DO NOTHING
	`, orgID, ownerID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrOwnerNotProjected
		}
		return fmt.Errorf("insert legacy membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit organization projection: %w", err)
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
