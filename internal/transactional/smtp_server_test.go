package transactional

import (
	"strings"
	"testing"
)

func TestParseSMTPMessageParsesAlternativeAndAttachment(t *testing.T) {
	raw := "From: sender@example.test\r\n" +
		"To: Recipient <recipient@example.test>\r\n" +
		"Bcc: hidden@example.test\r\n" +
		"Subject: =?UTF-8?Q?Receipt_=E2=9C=93?=\r\n" +
		"X-Request-ID: req-123\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Plain body\r\n--inner\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" +
		"<p>HTML body</p>\r\n--inner--\r\n" +
		"--outer\r\nContent-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=receipt.bin\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nAQID\r\n--outer--\r\n"
	input, err := parseSMTPMessage(strings.NewReader(raw), 4096,
		"sender@example.test", []string{"recipient@example.test", "hidden@example.test"})
	if err != nil {
		t.Fatalf("parseSMTPMessage returned error: %v", err)
	}
	if input.From != "sender@example.test" || input.Subject != "Receipt ✓" {
		t.Fatalf("parsed sender/subject = %q / %q", input.From, input.Subject)
	}
	if len(input.To) != 1 || input.To[0] != "recipient@example.test" ||
		len(input.BCC) != 1 || input.BCC[0] != "hidden@example.test" {
		t.Fatalf("parsed recipients = To %v, BCC %v", input.To, input.BCC)
	}
	if input.Text != "Plain body" || input.HTML != "<p>HTML body</p>" {
		t.Fatalf("parsed alternatives = text %q, html %q", input.Text, input.HTML)
	}
	if len(input.Attachments) != 1 || input.Attachments[0].Filename != "receipt.bin" ||
		input.Attachments[0].Content != "AQID" {
		t.Fatalf("parsed attachments = %+v", input.Attachments)
	}
	if input.Headers["X-Request-Id"] != "req-123" {
		t.Fatalf("custom headers = %+v", input.Headers)
	}
}

func TestParseSMTPMessageRejectsSenderMismatchAndOversize(t *testing.T) {
	message := "From: other@example.test\r\nTo: recipient@example.test\r\nSubject: Notice\r\n\r\nBody"
	if _, err := parseSMTPMessage(strings.NewReader(message), 1024,
		"sender@example.test", []string{"recipient@example.test"}); err == nil {
		t.Fatal("sender mismatch was accepted")
	}
	if _, err := parseSMTPMessage(strings.NewReader(message), 8,
		"other@example.test", []string{"recipient@example.test"}); err == nil {
		t.Fatal("oversized message was accepted")
	}
}

func TestNewSMTPSecretStoresOnlyPasswordHash(t *testing.T) {
	username, password, passwordHash, err := NewSMTPSecret()
	if err != nil {
		t.Fatalf("NewSMTPSecret returned error: %v", err)
	}
	if !strings.HasPrefix(username, "sw_") || !strings.HasPrefix(password, "sw_smtp_") {
		t.Fatalf("unexpected credential format: username %q", username)
	}
	if passwordHash == "" || passwordHash == password {
		t.Fatalf("secret hash is empty or contains the password")
	}
	_, password2, _, err := NewSMTPSecret()
	if err != nil {
		t.Fatalf("second NewSMTPSecret returned error: %v", err)
	}
	if password == password2 {
		t.Fatal("generated SMTP passwords are not unique")
	}
}
