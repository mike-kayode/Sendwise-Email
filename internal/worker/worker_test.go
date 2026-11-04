package worker

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/campaigns"
)

func TestBackoff(t *testing.T) {
	tests := []struct {
		name     string
		attempt  int
		expected time.Duration
	}{
		{name: "attempt 1", attempt: 1, expected: 1 * time.Minute},
		{name: "attempt 2", attempt: 2, expected: 2 * time.Minute},
		{name: "attempt 3", attempt: 3, expected: 4 * time.Minute},
		{name: "attempt 4", attempt: 4, expected: 8 * time.Minute},
		{name: "attempt 5", attempt: 5, expected: 16 * time.Minute},
		{name: "attempt 0 treated as 1", attempt: 0, expected: 1 * time.Minute},
		{name: "attempt 8 capped", attempt: 8, expected: 128 * time.Minute},
		{name: "attempt 9", attempt: 9, expected: 4*time.Hour + 16*time.Minute},
		{name: "attempt 10 max 6h", attempt: 10, expected: 6 * time.Hour},
		{name: "attempt 14 max 6h", attempt: 14, expected: 6 * time.Hour},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := backoff(test.attempt); got != test.expected {
				t.Fatalf("backoff(%d) = %v, want %v", test.attempt, got, test.expected)
			}
		})
	}
}

func TestIsTerminalJobStatus(t *testing.T) {
	terminal := []string{"submitted", "accepted", "bounced", "complained", "unsubscribed", "failed", "cancelled"}
	nonTerminal := []string{"queued", "processing", "deferred"}

	for _, status := range terminal {
		if !isTerminalJobStatus(status) {
			t.Fatalf("isTerminalJobStatus(%q) = false, want true", status)
		}
	}
	for _, status := range nonTerminal {
		if isTerminalJobStatus(status) {
			t.Fatalf("isTerminalJobStatus(%q) = true, want false", status)
		}
	}
}

func TestDomainOf(t *testing.T) {
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

func TestPersonalizationData(t *testing.T) {
	row := &jobRow{
		FirstName:    "John",
		LastName:     "Doe",
		Email:        "john@example.com",
		Organization: "Acme",
		CustomFields: []byte(`{"company":"Acme Corp","country":"US"}`),
	}

	data := personalizationData(row)

	if data["first_name"] != "John" {
		t.Fatalf("first_name = %q, want %q", data["first_name"], "John")
	}
	if data["last_name"] != "Doe" {
		t.Fatalf("last_name = %q, want %q", data["last_name"], "Doe")
	}
	if data["email"] != "john@example.com" {
		t.Fatalf("email = %q, want %q", data["email"], "john@example.com")
	}
	if data["organization"] != "Acme" {
		t.Fatalf("organization = %q, want %q", data["organization"], "Acme")
	}
	if data["custom.company"] != "Acme Corp" {
		t.Fatalf("custom.company = %q, want %q", data["custom.company"], "Acme Corp")
	}
	if data["country"] != "US" {
		t.Fatalf("country = %q, want %q", data["country"], "US")
	}
	if data["company"] != "Acme Corp" {
		t.Fatalf("company = %q, want %q", data["company"], "Acme Corp")
	}
}

func TestPersonalizationDataInvalidJSON(t *testing.T) {
	row := &jobRow{
		FirstName:    "Jane",
		LastName:     "Smith",
		Email:        "jane@example.com",
		Organization: "Test",
		CustomFields: []byte(`not valid json`),
	}

	data := personalizationData(row)

	if data["first_name"] != "Jane" {
		t.Fatalf("first_name = %q, want %q", data["first_name"], "Jane")
	}
	if data["custom.company"] != "" {
		t.Fatalf("custom.company should be empty for invalid JSON, got %q", data["custom.company"])
	}
}

func TestPersonalizationDataEmptyCustomFields(t *testing.T) {
	row := &jobRow{
		FirstName:    "Bob",
		Email:        "bob@example.com",
		Organization: "Org",
		CustomFields: []byte(`{}`),
	}

	data := personalizationData(row)

	if data["first_name"] != "Bob" {
		t.Fatalf("first_name = %q, want %q", data["first_name"], "Bob")
	}
	if len(data) != 4 {
		t.Fatalf("expected 4 keys, got %d: %v", len(data), data)
	}
}

func TestTruncateError(t *testing.T) {
	if truncateError(nil) != "" {
		t.Fatal("truncateError(nil) should return empty string")
	}

	longErr := errorString(strings.Repeat("x", 2000))
	if got := truncateError(longErr); len(got) != 1000 {
		t.Fatalf("truncateError(long) length = %d, want 1000", len(got))
	}

	shortErr := errorString("short error")
	if got := truncateError(shortErr); got != "short error" {
		t.Fatalf("truncateError(short) = %q, want %q", got, "short error")
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestMarshalJobPayload(t *testing.T) {
	job := campaigns.JobPayload{
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

func TestAppendBeforeBodyClose(t *testing.T) {
	doc := "<html><body><p>content</p></body></html>"
	fragment := `<img src="track.gif">`
	result := appendBeforeBodyClose(doc, fragment)

	if !strings.Contains(result, `<img src="track.gif"></body>`) {
		t.Fatalf("appendBeforeBodyClose() did not insert before </body>: %q", result)
	}

	noBody := "<html><p>no body tag</p></html>"
	result2 := appendBeforeBodyClose(noBody, fragment)
	if !strings.HasSuffix(result2, fragment) {
		t.Fatalf("appendBeforeBodyClose() should append fragment when no </body>: %q", result2)
	}
}
