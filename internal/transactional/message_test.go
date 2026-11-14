package transactional

import (
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestValidateMessageEnforcesRecipientsHeadersAndAlternatives(t *testing.T) {
	tests := []struct {
		name  string
		input MessageInput
		limit int
	}{
		{
			name: "duplicate recipients",
			input: MessageInput{
				From: "sender@example.test", To: []string{"person@example.test"},
				CC: []string{"PERSON@example.test"}, Subject: "Notice", Text: "Body",
			},
			limit: 5,
		},
		{
			name: "subject injection",
			input: MessageInput{
				From: "sender@example.test", To: []string{"person@example.test"},
				Subject: "Notice\r\nBcc: other@example.test", Text: "Body",
			},
			limit: 5,
		},
		{
			name: "custom header injection",
			input: MessageInput{
				From: "sender@example.test", To: []string{"person@example.test"},
				Subject: "Notice", Text: "Body", Headers: map[string]string{"X-Notice": "safe\r\nBcc: bad@example.test"},
			},
			limit: 5,
		},
		{
			name: "recipient limit",
			input: MessageInput{
				From: "sender@example.test", To: []string{"one@example.test", "two@example.test"},
				Subject: "Notice", Text: "Body",
			},
			limit: 1,
		},
		{
			name: "missing text alternative",
			input: MessageInput{
				From: "sender@example.test", To: []string{"one@example.test"},
				Subject: "Notice",
			},
			limit: 5,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateMessage(test.input, test.limit, 0); err == nil {
				t.Fatal("ValidateMessage() accepted invalid input")
			}
		})
	}
}

func TestValidateMessageCountsAttachmentBytes(t *testing.T) {
	input := MessageInput{
		From: "sender@example.test", To: []string{"person@example.test"},
		Subject: "Notice", Text: "Body",
		Attachments: []Attachment{{Filename: "file.bin", ContentType: "application/octet-stream", Content: strings.Repeat("a", 16)}},
	}
	if _, err := ValidateMessage(input, 5, 24); err == nil {
		t.Fatal("ValidateMessage() accepted a message larger than its byte limit")
	}
}

func TestRenderTemplateEscapesHTMLAndRejectsUnknownExpressions(t *testing.T) {
	rendered, err := RenderTemplate(Template{
		Subject: "Hello {{name}}",
		HTML:    "<p>{{name}}</p>",
		Text:    "Hello {{name}}",
	}, map[string]string{"name": `<script>alert("x")</script>`})
	if err != nil {
		t.Fatalf("RenderTemplate() error = %v", err)
	}
	if strings.Contains(rendered.HTML, "<script") || !strings.Contains(rendered.HTML, "&lt;script&gt;") {
		t.Fatalf("HTML variable was not escaped: %q", rendered.HTML)
	}
	if !strings.Contains(rendered.Text, `<script>alert("x")</script>`) {
		t.Fatalf("plain text variable was unexpectedly changed: %q", rendered.Text)
	}
	if _, err := RenderTemplate(Template{Text: "Hello {{missing}}"}, nil); err == nil {
		t.Fatal("RenderTemplate() accepted a missing variable")
	}
	if _, err := RenderTemplate(Template{Text: "Hello {{ if .Admin }}ok{{ end }}"}, nil); err == nil {
		t.Fatal("RenderTemplate() accepted executable template syntax")
	}
}

func TestPlainTextFromHTMLOmitsScriptAndStyle(t *testing.T) {
	got := PlainTextFromHTML(`<html><head><style>.x{display:none}</style></head><body>Hello <b>world</b><script>secret()</script></body></html>`)
	if got != "Hello world" {
		t.Fatalf("PlainTextFromHTML() = %q, want %q", got, "Hello world")
	}
}

func TestCanTransitionRestrictsTerminalStates(t *testing.T) {
	if !CanTransition(StatusQueued, StatusSending) ||
		!CanTransition(StatusSending, StatusDelivered) ||
		!CanTransition(StatusSending, StatusDeferred) ||
		!CanTransition(StatusDeferred, StatusSending) {
		t.Fatal("valid transactional status transition was rejected")
	}
	for _, status := range []Status{StatusDelivered, StatusFailed, StatusComplained, StatusBounced, StatusRejected, StatusSuppressed} {
		if CanTransition(status, StatusSending) {
			t.Errorf("terminal status %q transitioned back to sending", status)
		}
	}
}

func TestRetryDelayAndSMTPFailureClassification(t *testing.T) {
	for attempt := 1; attempt <= 20; attempt++ {
		delay := RetryDelay(attempt)
		if delay <= 0 || delay > time.Hour*2 {
			t.Fatalf("RetryDelay(%d) = %s, outside expected bounds", attempt, delay)
		}
	}
	temporary := ClassifyFailure(&textproto.Error{Code: 450, Msg: "4.2.0 mailbox busy"})
	if !temporary.Temporary || temporary.SMTPCode != 450 || temporary.EnhancedCode != "4.2.0" {
		t.Fatalf("temporary failure classification = %#v", temporary)
	}
	permanent := ClassifyFailure(&textproto.Error{Code: 550, Msg: "5.1.1 user unknown"})
	if permanent.Temporary || permanent.SMTPCode != 550 || permanent.EnhancedCode != "5.1.1" {
		t.Fatalf("permanent failure classification = %#v", permanent)
	}
}
