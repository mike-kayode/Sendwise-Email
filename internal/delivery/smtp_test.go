package delivery

import (
	"net/textproto"
	"testing"
)

func TestEnsureSTARTTLS(t *testing.T) {
	tests := []struct {
		name          string
		advertised    bool
		allowInsecure bool
		wantError     bool
	}{
		{name: "advertised", advertised: true},
		{name: "development plaintext fallback", allowInsecure: true},
		{name: "production requires starttls", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ensureSTARTTLS(test.advertised, test.allowInsecure)
			if (err != nil) != test.wantError {
				t.Fatalf("ensureSTARTTLS() error = %v, wantError %t", err, test.wantError)
			}
		})
	}
}

func TestClassifySMTPResponse(t *testing.T) {
	permanent := classifySMTP(&textproto.Error{Code: 550, Msg: "mailbox unavailable"})
	if !IsPermanent(permanent) {
		t.Fatal("5xx response should be permanent")
	}

	temporary := classifySMTP(&textproto.Error{Code: 450, Msg: "mailbox busy"})
	if IsPermanent(temporary) {
		t.Fatal("4xx response should be temporary")
	}
}

func TestFormatMessageID(t *testing.T) {
	if got, want := formatMessageID("job-123"), "<job-123@email-platform.local>"; got != want {
		t.Fatalf("formatMessageID() = %q, want %q", got, want)
	}
	if got, want := formatMessageID("<job@example.com>"), "<job@example.com>"; got != want {
		t.Fatalf("formatMessageID() = %q, want %q", got, want)
	}
}

func TestParseEnvelopeAddress(t *testing.T) {
	if got, err := parseEnvelopeAddress("Sendwise <hello@example.com>"); err != nil || got != "hello@example.com" {
		t.Fatalf("parseEnvelopeAddress(\"Sendwise <hello@example.com>\") = (%q, %v), want (\"hello@example.com\", nil)", got, err)
	}
	if got, err := parseEnvelopeAddress("hello@example.com"); err != nil || got != "hello@example.com" {
		t.Fatalf("parseEnvelopeAddress(\"hello@example.com\") = (%q, %v), want (\"hello@example.com\", nil)", got, err)
	}
}
