package deliveryjourney

import (
	"testing"
	"time"
)

func TestShouldAdvanceJourneyStatus(t *testing.T) {
	now := time.Now().UTC()
	older := now.Add(-time.Minute)
	newer := now.Add(time.Minute)
	tests := []struct {
		name        string
		current     string
		currentAt   *time.Time
		currentTry  int
		next        Event
		wantAdvance bool
	}{
		{
			name: "older event cannot roll state back", current: "submitted_to_mail_server",
			currentAt: &now, next: Event{Type: "queued", OccurredAt: older}, wantAdvance: false,
		},
		{
			name: "new attempt can resume relaying after recipient deferral", current: "deferred_by_recipient",
			currentAt: &now, currentTry: 1, next: Event{Type: "relaying", OccurredAt: newer, Attempt: intPointer(2)},
			wantAdvance: true,
		},
		{
			name: "late bounce corrects recipient acceptance", current: "accepted_by_recipient_server",
			currentAt: &now, next: Event{Type: "bounced", OccurredAt: newer}, wantAdvance: true,
		},
		{
			name: "late deferral cannot overwrite recipient acceptance", current: "accepted_by_recipient_server",
			currentAt: &now, next: Event{Type: "deferred_by_recipient", OccurredAt: newer},
			wantAdvance: false,
		},
		{
			name: "terminal failure cannot be replaced by another terminal status", current: "bounced",
			currentAt: &now, next: Event{Type: "failed", OccurredAt: newer}, wantAdvance: false,
		},
		{
			name: "newer terminal event advances", current: "submitted_to_mail_server",
			currentAt: &now, next: Event{Type: "accepted_by_recipient_server", OccurredAt: newer},
			wantAdvance: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldAdvance(test.current, test.currentAt, test.currentTry, test.next); got != test.wantAdvance {
				t.Fatalf("shouldAdvance() = %v, want %v", got, test.wantAdvance)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestValidTypeAndSourceAcceptTimelineEvents(t *testing.T) {
	for _, typ := range []string{"picked_up", "smtp_attempt", "deferred_by_recipient"} {
		if !validType(typ) {
			t.Fatalf("validType(%q) = false, want true", typ)
		}
	}
	for _, src := range []string{"sendwise", "stalwart_webhook", "reconciliation", "dsn"} {
		if !validSource(src) {
			t.Fatalf("validSource(%q) = false, want true", src)
		}
	}
	if validType("not_a_real_type") {
		t.Fatal("validType accepted unknown type")
	}
	if validSource("not_a_real_source") {
		t.Fatal("validSource accepted unknown source")
	}
}
