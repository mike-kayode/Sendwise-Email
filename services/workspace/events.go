package workspace

import (
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/shared/contracts/events"
)

// newEnvelope builds a validated, versioned event envelope. Correlation IDs
// must be UUIDs, so an invalid incoming request id is replaced.
func newEnvelope[T any](typ string, version int, aggregateType, aggregateID string, correlationID string, data T) events.Envelope[T] {
	if _, err := uuid.Parse(correlationID); err != nil {
		correlationID = uuid.NewString()
	}
	return events.Envelope[T]{
		ID:            uuid.NewString(),
		Type:          typ,
		Version:       version,
		OccurredAt:    time.Now().UTC(),
		Producer:      "workspace",
		Aggregate:     events.Aggregate{Type: aggregateType, ID: aggregateID, Version: 1},
		CorrelationID: correlationID,
		Data:          data,
	}
}

func organizationCreatedEvent(org *Organization, ownerUserID, correlationID string) events.Envelope[events.OrganizationCreated] {
	return newEnvelope("workspace.organization_created", 1, "organization", org.ID, correlationID, events.OrganizationCreated{
		OrganizationID: org.ID,
		Name:           org.Name,
		Slug:           org.Slug,
		OwnerUserID:    ownerUserID,
	})
}

func organizationUpdatedEvent(org *Organization, correlationID string) events.Envelope[events.OrganizationUpdated] {
	return newEnvelope("workspace.organization_updated", 1, "organization", org.ID, correlationID, events.OrganizationUpdated{
		OrganizationID: org.ID,
		Name:           org.Name,
		Slug:           org.Slug,
	})
}

func memberAddedEvent(membership *Membership, correlationID string) events.Envelope[events.MemberAdded] {
	return newEnvelope("workspace.member_added", 1, "membership", membership.OrganizationID, correlationID, events.MemberAdded{
		OrganizationID: membership.OrganizationID,
		UserID:         membership.UserID,
		Role:           membership.Role,
	})
}

func invitationCreatedEvent(invitation *Invitation, correlationID string) events.Envelope[events.InvitationCreated] {
	return newEnvelope("workspace.invitation_created", 1, "invitation", invitation.ID, correlationID, events.InvitationCreated{
		OrganizationID: invitation.OrganizationID,
		InvitationID:   invitation.ID,
		Email:          invitation.Email,
		Role:           invitation.Role,
	})
}

func invitationAcceptedEvent(invitation *Invitation, userID, correlationID string) events.Envelope[events.InvitationAccepted] {
	return newEnvelope("workspace.invitation_accepted", 1, "invitation", invitation.ID, correlationID, events.InvitationAccepted{
		OrganizationID: invitation.OrganizationID,
		InvitationID:   invitation.ID,
		UserID:         userID,
		Role:           invitation.Role,
	})
}
