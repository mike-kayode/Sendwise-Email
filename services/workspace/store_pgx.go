package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/outbox"
)

const consumerName = "workspace-service"

// pgxStore is the production Store backed by the workspace database.
type pgxStore struct {
	pool *pgxpool.Pool
}

// NewStore connects to the workspace database.
func NewStore(ctx context.Context, databaseURL string) (Store, error) {
	pool, err := kit.Connect(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect workspace database: %w", err)
	}
	return &pgxStore{pool: pool}, nil
}

func (s *pgxStore) Ping(ctx context.Context) error { return kit.MustPing(ctx, s.pool) }
func (s *pgxStore) Close()                         { s.pool.Close() }

// Pool exposes the underlying pool for infrastructure wiring (outbox relay).
func (s *pgxStore) Pool() *pgxpool.Pool { return s.pool }

func (s *pgxStore) ProvisionUserOrganization(ctx context.Context, eventID, userID, organizationName, correlationID string) (*Organization, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin provision transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	newlyProcessed, err := outbox.MarkProcessed(ctx, tx, consumerName, eventID)
	if err != nil {
		return nil, false, fmt.Errorf("record inbox event: %w", err)
	}
	if !newlyProcessed {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit idempotency record: %w", err)
		}
		return nil, false, nil
	}

	var alreadyMember bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id = $1)`, userID).Scan(&alreadyMember); err != nil {
		return nil, false, fmt.Errorf("check existing membership: %w", err)
	}
	if alreadyMember {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit: %w", err)
		}
		return nil, false, nil
	}

	name := strings.TrimSpace(organizationName)
	if name == "" {
		name = "My Workspace"
	}
	slug, err := chooseSlug(ctx, tx, name, userID)
	if err != nil {
		return nil, false, err
	}

	org := &Organization{}
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1, $2)
		RETURNING id, name, slug, created_at
	`, name, slug).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if isUniqueViolation(err) {
		// Another workspace took the slug between check and insert; retry once
		// with a suffix derived from the event id.
		slug = slugify(name) + "-" + shortID(eventID)
		err = tx.QueryRow(ctx, `
			INSERT INTO organizations (name, slug) VALUES ($1, $2)
			RETURNING id, name, slug, created_at
		`, name, slug).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	}
	if err != nil {
		return nil, false, fmt.Errorf("insert organization: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, 'owner')
	`, org.ID, userID); err != nil {
		return nil, false, fmt.Errorf("insert owner membership: %w", err)
	}

	if err := outbox.Enqueue(ctx, tx, "workspace.organization_created.v1",
		organizationCreatedEvent(org, userID, correlationID)); err != nil {
		return nil, false, fmt.Errorf("enqueue organization_created: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit provision: %w", err)
	}
	return org, true, nil
}

func chooseSlug(ctx context.Context, tx pgx.Tx, name, userID string) (string, error) {
	base := slugify(name)
	candidates := []string{base, base + "-" + shortID(userID), base + "-" + randomSuffix()}
	for _, candidate := range candidates {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE slug = $1)`, candidate).Scan(&taken); err != nil {
			return "", fmt.Errorf("check slug %q: %w", candidate, err)
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q", name)
}

func (s *pgxStore) MembershipsForUser(ctx context.Context, userID string) ([]MembershipWithOrganization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.organization_id, m.user_id, m.role, m.created_at,
		       o.id, o.name, o.slug, o.created_at
		FROM memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY m.created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	defer rows.Close()

	var result []MembershipWithOrganization
	for rows.Next() {
		var item MembershipWithOrganization
		if err := rows.Scan(&item.OrganizationID, &item.UserID, &item.Role, &item.CreatedAt,
			&item.Organization.ID, &item.Organization.Name, &item.Organization.Slug, &item.Organization.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *pgxStore) Membership(ctx context.Context, organizationID, userID string) (*Membership, error) {
	var membership Membership
	err := s.pool.QueryRow(ctx, `
		SELECT organization_id, user_id, role, created_at
		FROM memberships WHERE organization_id = $1 AND user_id = $2
	`, organizationID, userID).Scan(&membership.OrganizationID, &membership.UserID, &membership.Role, &membership.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load membership: %w", err)
	}
	return &membership, nil
}

func (s *pgxStore) ListMembers(ctx context.Context, organizationID string) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT organization_id, user_id, role, created_at
		FROM memberships WHERE organization_id = $1
		ORDER BY created_at, user_id
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var result []Membership
	for rows.Next() {
		var membership Membership
		if err := rows.Scan(&membership.OrganizationID, &membership.UserID, &membership.Role, &membership.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		result = append(result, membership)
	}
	return result, rows.Err()
}

func (s *pgxStore) GetOrganization(ctx context.Context, organizationID string) (*Organization, error) {
	var org Organization
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, created_at FROM organizations WHERE id = $1
	`, organizationID).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load organization: %w", err)
	}
	return &org, nil
}

func (s *pgxStore) UpdateOrganization(ctx context.Context, organizationID, name, slug, correlationID string) (*Organization, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin update transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var current Organization
	err = tx.QueryRow(ctx, `
		SELECT id, name, slug, created_at FROM organizations WHERE id = $1 FOR UPDATE
	`, organizationID).Scan(&current.ID, &current.Name, &current.Slug, &current.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load organization: %w", err)
	}

	if name == "" {
		name = current.Name
	}
	if slug == "" {
		slug = current.Slug
	}
	if slug != current.Slug {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE slug = $1 AND id <> $2)`, slug, organizationID).Scan(&taken); err != nil {
			return nil, fmt.Errorf("check slug: %w", err)
		}
		if taken {
			return nil, ErrSlugInUse
		}
	}

	org := &Organization{}
	err = tx.QueryRow(ctx, `
		UPDATE organizations SET name = $2, slug = $3 WHERE id = $1
		RETURNING id, name, slug, created_at
	`, organizationID, name, slug).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if isUniqueViolation(err) {
		return nil, ErrSlugInUse
	}
	if err != nil {
		return nil, fmt.Errorf("update organization: %w", err)
	}

	if org.Name != current.Name || org.Slug != current.Slug {
		if err := outbox.Enqueue(ctx, tx, "workspace.organization_updated.v1",
			organizationUpdatedEvent(org, correlationID)); err != nil {
			return nil, fmt.Errorf("enqueue organization_updated: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update: %w", err)
	}
	return org, nil
}

func (s *pgxStore) CreateInvitation(ctx context.Context, params InvitationCreate) (*Invitation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin invitation transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	invitation := &Invitation{}
	err = tx.QueryRow(ctx, `
		INSERT INTO invitations (organization_id, email, role, token_hash, invited_by, expires_at)
		VALUES ($1, lower($2), $3, $4, NULLIF($5, '')::uuid, $6)
		RETURNING id, organization_id, email, role, status, invited_by, expires_at, created_at
	`, params.OrganizationID, params.Email, params.Role, params.TokenHash, params.InvitedBy, params.ExpiresAt).Scan(
		&invitation.ID, &invitation.OrganizationID, &invitation.Email, &invitation.Role,
		&invitation.Status, &invitation.InvitedBy, &invitation.ExpiresAt, &invitation.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert invitation: %w", err)
	}

	if err := outbox.Enqueue(ctx, tx, "workspace.invitation_created.v1",
		invitationCreatedEvent(invitation, "")); err != nil {
		return nil, fmt.Errorf("enqueue invitation_created: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit invitation: %w", err)
	}
	return invitation, nil
}

func (s *pgxStore) ListInvitations(ctx context.Context, organizationID string) ([]Invitation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, email, role, status, invited_by, expires_at, accepted_at, created_at
		FROM invitations WHERE organization_id = $1
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()

	var result []Invitation
	for rows.Next() {
		var invitation Invitation
		if err := rows.Scan(&invitation.ID, &invitation.OrganizationID, &invitation.Email, &invitation.Role,
			&invitation.Status, &invitation.InvitedBy, &invitation.ExpiresAt, &invitation.AcceptedAt, &invitation.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		result = append(result, invitation)
	}
	return result, rows.Err()
}

func (s *pgxStore) RevokeInvitation(ctx context.Context, organizationID, invitationID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE invitations SET status = 'revoked'
		WHERE id = $1 AND organization_id = $2 AND status = 'pending'
	`, invitationID, organizationID)
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *pgxStore) AcceptInvitation(ctx context.Context, tokenHash, userID, email, correlationID string) (*Membership, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin accept transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var invitation Invitation
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, email, role, status, invited_by, expires_at, accepted_at, created_at
		FROM invitations WHERE token_hash = $1 FOR UPDATE
	`, tokenHash).Scan(&invitation.ID, &invitation.OrganizationID, &invitation.Email, &invitation.Role,
		&invitation.Status, &invitation.InvitedBy, &invitation.ExpiresAt, &invitation.AcceptedAt, &invitation.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvitationInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("load invitation: %w", err)
	}
	if invitation.Status != "pending" || time.Now().After(invitation.ExpiresAt) {
		return nil, ErrInvitationInvalid
	}
	if !strings.EqualFold(invitation.Email, email) {
		return nil, ErrInvitationEmail
	}

	var alreadyMember bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM memberships WHERE organization_id = $1 AND user_id = $2)
	`, invitation.OrganizationID, userID).Scan(&alreadyMember); err != nil {
		return nil, fmt.Errorf("check membership: %w", err)
	}

	membership := &Membership{}
	err = tx.QueryRow(ctx, `
		INSERT INTO memberships (organization_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, user_id) DO UPDATE SET role = memberships.role
		RETURNING organization_id, user_id, role, created_at
	`, invitation.OrganizationID, userID, invitation.Role).Scan(
		&membership.OrganizationID, &membership.UserID, &membership.Role, &membership.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert membership: %w", err)
	}

	if !alreadyMember {
		if err := outbox.Enqueue(ctx, tx, "workspace.member_added.v1",
			memberAddedEvent(membership, correlationID)); err != nil {
			return nil, fmt.Errorf("enqueue member_added: %w", err)
		}
	}
	if err := outbox.Enqueue(ctx, tx, "workspace.invitation_accepted.v1",
		invitationAcceptedEvent(&invitation, userID, correlationID)); err != nil {
		return nil, fmt.Errorf("enqueue invitation_accepted: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE invitations SET status = 'accepted', accepted_at = now(), accepted_by = $2
		WHERE id = $1
	`, invitation.ID, userID); err != nil {
		return nil, fmt.Errorf("mark invitation accepted: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit acceptance: %w", err)
	}
	return membership, nil
}

func shortID(value string) string {
	cleaned := strings.ReplaceAll(value, "-", "")
	if len(cleaned) > 6 {
		return strings.ToLower(cleaned[:6])
	}
	return strings.ToLower(cleaned)
}

func randomSuffix() string {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return uuid.NewString()[:6]
	}
	return hex.EncodeToString(buf)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
