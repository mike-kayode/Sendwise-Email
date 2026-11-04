package events

// Domain event payload contracts. Every payload is versioned through the
// enclosing Envelope; breaking changes require a new event version and a new
// subject suffix (e.g. .v2). Consumers must tolerate additive fields.

// UserRegistered is emitted by Identity after an account is created. The
// Workspace service consumes it to provision the user's default organization.
type UserRegistered struct {
	UserID           string `json:"user_id"`
	Email            string `json:"email"`
	Name             string `json:"name"`
	OrganizationName string `json:"organization_name"`
}

// UserVerified is emitted by Identity when an email address is confirmed.
type UserVerified struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// PasswordReset is emitted by Identity when a reset is requested.
type PasswordReset struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// OrganizationCreated is emitted by Workspace when a tenant is provisioned.
type OrganizationCreated struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	OwnerUserID    string `json:"owner_user_id"`
}

// OrganizationUpdated is emitted when tenant profile data changes.
type OrganizationUpdated struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
}

// MemberAdded is emitted when a user joins an organization.
type MemberAdded struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
	Role           string `json:"role"`
}

// MemberRoleChanged is emitted when a membership role changes.
type MemberRoleChanged struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
	Role           string `json:"role"`
}

// MemberRemoved is emitted when a membership is revoked.
type MemberRemoved struct {
	OrganizationID string `json:"organization_id"`
	UserID         string `json:"user_id"`
}

// InvitationCreated is emitted when an invitation is issued.
type InvitationCreated struct {
	OrganizationID string `json:"organization_id"`
	InvitationID   string `json:"invitation_id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
}

// InvitationAccepted is emitted when an invited user joins.
type InvitationAccepted struct {
	OrganizationID string `json:"organization_id"`
	InvitationID   string `json:"invitation_id"`
	UserID         string `json:"user_id"`
	Role           string `json:"role"`
}
