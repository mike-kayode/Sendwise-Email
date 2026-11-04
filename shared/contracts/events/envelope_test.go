package events

import (
	"testing"
	"time"
)

func TestEnvelopeValidate(t *testing.T) {
	tenantID := "44444444-4444-4444-8444-444444444444"
	valid := Envelope[struct{}]{
		ID:            "11111111-1111-4111-8111-111111111111",
		Type:          "audience.contact_created",
		Version:       1,
		OccurredAt:    time.Now(),
		Producer:      "audience",
		TenantID:      &tenantID,
		Aggregate:     Aggregate{Type: "contact", ID: "22222222-2222-4222-8222-222222222222", Version: 1},
		CorrelationID: "33333333-3333-4333-8333-333333333333",
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
}

func TestEnvelopeValidateRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Envelope[struct{}])
		want string
	}{
		{"id", func(e *Envelope[struct{}]) { e.ID = "invalid" }, "event id must be a UUID"},
		{"type", func(e *Envelope[struct{}]) { e.Type = "" }, "event type is required"},
		{"type subject characters", func(e *Envelope[struct{}]) { e.Type = "audience.>" }, "event type must be dot-separated lowercase identifiers"},
		{"version", func(e *Envelope[struct{}]) { e.Version = 0 }, "event version must be positive"},
		{"time", func(e *Envelope[struct{}]) { e.OccurredAt = time.Time{} }, "event occurrence time is required"},
		{"producer", func(e *Envelope[struct{}]) { e.Producer = "" }, "event producer is required"},
		{"aggregate", func(e *Envelope[struct{}]) { e.Aggregate.ID = "invalid" }, "aggregate id must be a UUID"},
		{"correlation", func(e *Envelope[struct{}]) { e.CorrelationID = "invalid" }, "correlation id must be a UUID"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := Envelope[struct{}]{
				ID:            "11111111-1111-4111-8111-111111111111",
				Type:          "audience.contact_created",
				Version:       1,
				OccurredAt:    time.Now(),
				Producer:      "audience",
				Aggregate:     Aggregate{Type: "contact", ID: "22222222-2222-4222-8222-222222222222", Version: 1},
				CorrelationID: "33333333-3333-4333-8333-333333333333",
			}
			test.edit(&envelope)
			if err := envelope.Validate(); err == nil || err.Error() != test.want {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestEnvelopeValidateRejectsInvalidTenantID(t *testing.T) {
	invalid := "not-a-uuid"
	envelope := Envelope[struct{}]{
		ID:            "11111111-1111-4111-8111-111111111111",
		Type:          "identity.user_registered",
		Version:       1,
		OccurredAt:    time.Now(),
		Producer:      "identity",
		TenantID:      &invalid,
		Aggregate:     Aggregate{Type: "user", ID: "22222222-2222-4222-8222-222222222222", Version: 1},
		CorrelationID: "33333333-3333-4333-8333-333333333333",
	}
	if err := envelope.Validate(); err == nil || err.Error() != "tenant id must be a UUID when provided" {
		t.Fatalf("Validate() error = %v, want invalid tenant error", err)
	}
}
