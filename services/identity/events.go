package identity

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
		Producer:      "identity",
		Aggregate:     events.Aggregate{Type: aggregateType, ID: aggregateID, Version: 1},
		CorrelationID: correlationID,
		Data:          data,
	}
}

func userRegisteredEvent(user *User, organizationName, correlationID string) events.Envelope[events.UserRegistered] {
	return newEnvelope("identity.user_registered", 1, "user", user.ID, correlationID, events.UserRegistered{
		UserID:           user.ID,
		Email:            user.Email,
		Name:             user.Name,
		OrganizationName: organizationName,
	})
}

func userVerifiedEvent(user *User, correlationID string) events.Envelope[events.UserVerified] {
	return newEnvelope("identity.user_verified", 1, "user", user.ID, correlationID, events.UserVerified{
		UserID: user.ID,
		Email:  user.Email,
	})
}

func passwordResetEvent(user *User, correlationID string) events.Envelope[events.PasswordReset] {
	return newEnvelope("identity.password_reset", 1, "user", user.ID, correlationID, events.PasswordReset{
		UserID: user.ID,
		Email:  user.Email,
	})
}
