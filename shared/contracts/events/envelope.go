package events

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Aggregate struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type Envelope[T any] struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	Version       int       `json:"version"`
	OccurredAt    time.Time `json:"occurred_at"`
	Producer      string    `json:"producer"`
	TenantID      *string   `json:"tenant_id,omitempty"`
	Aggregate     Aggregate `json:"aggregate"`
	CorrelationID string    `json:"correlation_id"`
	CausationID   string    `json:"causation_id,omitempty"`
	Traceparent   string    `json:"traceparent,omitempty"`
	Data          T         `json:"data"`
}

func (e Envelope[T]) Validate() error {
	switch {
	case !isUUID(e.ID):
		return errors.New("event id must be a UUID")
	case strings.TrimSpace(e.Type) == "":
		return errors.New("event type is required")
	case !validEventType(e.Type):
		return errors.New("event type must be dot-separated lowercase identifiers")
	case e.Version < 1:
		return errors.New("event version must be positive")
	case e.OccurredAt.IsZero():
		return errors.New("event occurrence time is required")
	case strings.TrimSpace(e.Producer) == "":
		return errors.New("event producer is required")
	case strings.TrimSpace(e.Aggregate.Type) == "":
		return errors.New("aggregate type is required")
	case !isUUID(e.Aggregate.ID):
		return errors.New("aggregate id must be a UUID")
	case e.Aggregate.Version < 1:
		return errors.New("aggregate version must be positive")
	case !isUUID(e.CorrelationID):
		return errors.New("correlation id must be a UUID")
	case e.CausationID != "" && !isUUID(e.CausationID):
		return errors.New("causation id must be a UUID")
	case e.TenantID != nil && !isUUID(*e.TenantID):
		return errors.New("tenant id must be a UUID when provided")
	default:
		return nil
	}
}

func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validEventType(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, character := range part {
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') &&
				character != '_' {
				return false
			}
		}
	}
	return true
}
