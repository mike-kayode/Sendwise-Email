package outbox

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/khayode/email-platform/shared/contracts/events"
)

func TestRetryDelayIsExponentialAndBounded(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: -1, want: time.Second},
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 10, want: 512 * time.Second},
		{attempt: 13, want: 2048 * time.Second},
	}

	for _, test := range tests {
		if got := RetryDelay(test.attempt); got != test.want {
			t.Errorf("RetryDelay(%d) = %s, want %s", test.attempt, got, test.want)
		}
	}
}

func TestEncodeRequiresVersionedSubjectMatchingEnvelope(t *testing.T) {
	envelope := events.Envelope[events.UserRegistered]{
		ID:            "11111111-1111-4111-8111-111111111111",
		Type:          "identity.user_registered",
		Version:       1,
		OccurredAt:    time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
		Producer:      "identity",
		Aggregate:     events.Aggregate{Type: "user", ID: "22222222-2222-4222-8222-222222222222", Version: 1},
		CorrelationID: "33333333-3333-4333-8333-333333333333",
		Data: events.UserRegistered{
			UserID:           "22222222-2222-4222-8222-222222222222",
			Email:            "ada@example.com",
			Name:             "Ada",
			OrganizationName: "Ada's workspace",
		},
	}

	id, payload, err := encode("identity.user_registered.v1", envelope)
	if err != nil {
		t.Fatalf("encode valid event: %v", err)
	}
	if id.String() != envelope.ID {
		t.Fatalf("encoded event id = %s, want %s", id, envelope.ID)
	}
	var decoded events.Envelope[events.UserRegistered]
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode encoded event: %v", err)
	}
	if decoded.Type != envelope.Type || decoded.Data.UserID != envelope.Data.UserID {
		t.Fatalf("decoded event = %+v, want %+v", decoded, envelope)
	}

	if _, _, err := encode("identity.user_registered.v2", envelope); err == nil {
		t.Fatal("encode accepted a subject with the wrong version")
	}
}
