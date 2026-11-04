package workspace

import (
	"context"
	"errors"
	"time"
)

// Store is the Workspace service data-access contract. The pgx implementation
// owns the workspace schema; tests use an in-memory fake.
type Store interface {
	Ping(ctx context.Context) error
	Close()

	// ProvisionUserOrganization creates the default organization and owner
	// membership for a freshly registered user, idempotently keyed by the
	// triggering event id. It returns created=false when the event was already
	// processed or the user already belongs to an organization.
	ProvisionUserOrganization(ctx context.Context, eventID, userID, organizationName, correlationID string) (org *Organization, created bool, err error)

	MembershipsForUser(ctx context.Context, userID string) ([]MembershipWithOrganization, error)
	Membership(ctx context.Context, organizationID, userID string) (*Membership, error)
	ListMembers(ctx context.Context, organizationID string) ([]Membership, error)
	GetOrganization(ctx context.Context, organizationID string) (*Organization, error)
	UpdateOrganization(ctx context.Context, organizationID, name, slug, correlationID string) (*Organization, error)

	CreateInvitation(ctx context.Context, params InvitationCreate) (*Invitation, error)
	ListInvitations(ctx context.Context, organizationID string) ([]Invitation, error)
	RevokeInvitation(ctx context.Context, organizationID, invitationID string) error
	// AcceptInvitation consumes a pending invitation for the given user. The
	// invitation email must match the accepting user's email.
	AcceptInvitation(ctx context.Context, tokenHash, userID, email, correlationID string) (*Membership, error)
}

// Domain errors shared by implementations.
var (
	ErrNotFound          = errors.New("not found")
	ErrForbidden         = errors.New("forbidden")
	ErrAlreadyMember     = errors.New("already a member")
	ErrInvitationInvalid = errors.New("invitation is invalid or expired")
	ErrInvitationEmail   = errors.New("invitation email mismatch")
	ErrSlugInUse         = errors.New("slug already in use")
)

// Organization is a tenant profile owned by this service.
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// Membership ties a user to an organization with a role.
type Membership struct {
	OrganizationID string    `json:"organization_id"`
	UserID         string    `json:"user_id"`
	Role           string    `json:"role"`
	CreatedAt      time.Time `json:"created_at"`
}

// MembershipWithOrganization is a membership joined with its tenant profile.
type MembershipWithOrganization struct {
	Membership
	Organization Organization `json:"organization"`
}

// Invitation is a pending, accepted, or revoked join offer.
type Invitation struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	Email          string     `json:"email"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	InvitedBy      *string    `json:"invited_by,omitempty"`
	ExpiresAt      time.Time  `json:"expires_at"`
	AcceptedAt     *time.Time `json:"accepted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// InvitationCreate parameters for CreateInvitation.
type InvitationCreate struct {
	OrganizationID string
	Email          string
	Role           string
	InvitedBy      string
	TokenHash      string
	ExpiresAt      time.Time
}
