package campaigns

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakePublisher struct {
	published [][]byte
	subject   string
	err       error
}

func (f *fakePublisher) Publish(ctx context.Context, subject string, data []byte) error {
	f.subject = subject
	f.published = append(f.published, data)
	return f.err
}

func TestRepublishPendingNoPublisher(t *testing.T) {
	s := NewService(nil, nil, nil, 5, false)
	n, err := s.RepublishPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("RepublishPending() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("RepublishPending() = %d, want 0", n)
	}
}

func TestErrInvalidState(t *testing.T) {
	if ErrInvalidState.Error() != "campaign is not in a sendable state" {
		t.Fatalf("ErrInvalidState = %q", ErrInvalidState.Error())
	}
}

func TestErrNotFound(t *testing.T) {
	if ErrNotFound.Error() != "campaign not found" {
		t.Fatalf("ErrNotFound = %q", ErrNotFound.Error())
	}
}

func TestErrSegmentNotFound(t *testing.T) {
	if ErrSegmentNotFound.Error() != "campaign segment not found" {
		t.Fatalf("ErrSegmentNotFound = %q", ErrSegmentNotFound.Error())
	}
}

func TestErrInvalidSegment(t *testing.T) {
	if ErrInvalidSegment.Error() != "campaign segment rules are invalid" {
		t.Fatalf("ErrInvalidSegment = %q", ErrInvalidSegment.Error())
	}
}

func TestJobPayloadJSON(t *testing.T) {
	job := JobPayload{
		JobID:          "job-1",
		OrganizationID: "org-1",
		CampaignID:     "camp-1",
		ContactID:      "contact-1",
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	expected := `{"job_id":"job-1","organization_id":"org-1","campaign_id":"camp-1","contact_id":"contact-1"}`
	if string(data) != expected {
		t.Fatalf("json.Marshal() = %q, want %q", string(data), expected)
	}
}

func TestErrDomainUnverified(t *testing.T) {
	if ErrDomainUnverified.Error() != "sending domain is not verified" {
		t.Fatalf("ErrDomainUnverified = %q", ErrDomainUnverified.Error())
	}
}

func TestDomainOfCampaigns(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		expected string
	}{
		{name: "simple", email: "user@example.com", expected: "example.com"},
		{name: "subdomain", email: "user@mail.example.com", expected: "mail.example.com"},
		{name: "no at sign", email: "userexample.com", expected: ""},
		{name: "empty", email: "", expected: ""},
		{name: "at only", email: "@", expected: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := domainOf(test.email); got != test.expected {
				t.Fatalf("domainOf(%q) = %q, want %q", test.email, got, test.expected)
			}
		})
	}
}

func TestErrorsAreSentinel(t *testing.T) {
	if !errors.Is(ErrNotFound, ErrNotFound) {
		t.Fatal("ErrNotFound should match itself")
	}
	if !errors.Is(ErrInvalidState, ErrInvalidState) {
		t.Fatal("ErrInvalidState should match itself")
	}
	if !errors.Is(ErrSegmentNotFound, ErrSegmentNotFound) {
		t.Fatal("ErrSegmentNotFound should match itself")
	}
	if !errors.Is(ErrInvalidSegment, ErrInvalidSegment) {
		t.Fatal("ErrInvalidSegment should match itself")
	}
	if !errors.Is(ErrDomainUnverified, ErrDomainUnverified) {
		t.Fatal("ErrDomainUnverified should match itself")
	}
}

func TestFakePublisher(t *testing.T) {
	f := &fakePublisher{}
	err := f.Publish(context.Background(), "test.subject", []byte("data"))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if f.subject != "test.subject" {
		t.Fatalf("subject = %q, want %q", f.subject, "test.subject")
	}
	if len(f.published) != 1 {
		t.Fatalf("published count = %d, want 1", len(f.published))
	}
	if string(f.published[0]) != "data" {
		t.Fatalf("published data = %q, want %q", string(f.published[0]), "data")
	}
}
