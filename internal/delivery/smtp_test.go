package delivery

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
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

func TestSMTPTransportClassifiesGreetingEOFAsInfrastructureFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()
	transport := NewSMTPTransport(listener.Addr().String(), "localhost", "", "", true)
	transport.DialTimeout = time.Second
	_, err = transport.Send(t.Context(), Message{
		ID: "infrastructure-eof", From: "sender@example.test",
		To: "recipient@example.test", Subject: "Test", Text: "body",
	})
	if err == nil || !IsInfrastructureError(err) {
		t.Fatalf("Send() error = %v, want infrastructure-classified greeting EOF", err)
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

func TestMarshalMessageIncludesAttachmentAndTextAlternatives(t *testing.T) {
	var raw bytes.Buffer
	message := Message{
		ID:      "message@example.test",
		From:    "sender@example.test",
		To:      "recipient@example.test",
		Subject: "Receipt",
		Text:    "Plain receipt",
		HTML:    "<p>HTML receipt</p>",
		Attachments: []Attachment{{
			Filename: "receipt.txt", ContentType: "text/plain", Content: []byte("receipt data"),
		}},
	}
	if err := writeMessage(&raw, message); err != nil {
		t.Fatalf("writeMessage() error = %v", err)
	}
	parsed, err := mail.ReadMessage(&raw)
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("outer content type = %q, params=%v, err=%v", mediaType, params, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	bodyPart, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read text alternatives: %v", err)
	}
	bodyType, bodyParams, err := mime.ParseMediaType(bodyPart.Header.Get("Content-Type"))
	if err != nil || bodyType != "multipart/alternative" {
		t.Fatalf("body content type = %q, params=%v, err=%v", bodyType, bodyParams, err)
	}
	alternativeReader := multipart.NewReader(bodyPart, bodyParams["boundary"])
	plainPart, err := alternativeReader.NextPart()
	if err != nil {
		t.Fatalf("read plain-text part: %v", err)
	}
	plain, err := io.ReadAll(plainPart)
	if err != nil || !strings.Contains(string(plain), "Plain receipt") {
		t.Fatalf("plain part = %q, err=%v", plain, err)
	}
	htmlPart, err := alternativeReader.NextPart()
	if err != nil {
		t.Fatalf("read HTML part: %v", err)
	}
	htmlBody, err := io.ReadAll(htmlPart)
	if err != nil || !strings.Contains(string(htmlBody), "HTML receipt") {
		t.Fatalf("HTML part = %q, err=%v", htmlBody, err)
	}

	attachment, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read attachment part: %v", err)
	}
	_, attachmentParams, err := mime.ParseMediaType(attachment.Header.Get("Content-Disposition"))
	if err != nil || attachmentParams["filename"] != "receipt.txt" {
		t.Fatalf("attachment disposition = %q, err=%v", attachment.Header.Get("Content-Disposition"), err)
	}
	encoded, err := io.ReadAll(attachment)
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || !bytes.Equal(decoded, []byte("receipt data")) {
		t.Fatalf("decoded attachment = %q, err=%v", decoded, err)
	}
}

func TestSMTPTransportRequestsDeliveryNotifications(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	rcptCommand := make(chan string, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(response string) error {
			if _, err := fmt.Fprint(writer, response); err != nil {
				return err
			}
			return writer.Flush()
		}
		if err := write("220 smtp.example.test ready\r\n"); err != nil {
			serverErr <- err
			return
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverErr <- err
				return
			}
			command := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(command, "EHLO "):
				err = write("250-smtp.example.test\r\n250-DSN\r\n250 OK\r\n")
			case strings.HasPrefix(command, "MAIL FROM:"):
				err = write("250 sender accepted\r\n")
			case strings.HasPrefix(command, "RCPT TO:"):
				rcptCommand <- command
				err = write("250 recipient accepted\r\n")
			case command == "DATA":
				err = write("354 send message\r\n")
				if err == nil {
					for {
						line, err = reader.ReadString('\n')
						if err != nil || line == ".\r\n" {
							break
						}
					}
					if err == nil {
						err = write("250 queued\r\n")
					}
				}
			case command == "QUIT":
				err = write("221 bye\r\n")
				serverErr <- err
				return
			default:
				err = fmt.Errorf("unexpected SMTP command %q", command)
			}
			if err != nil {
				serverErr <- err
				return
			}
		}
	}()

	transport := NewSMTPTransport(listener.Addr().String(), "smtp.example.test", "", "", true)
	result, err := transport.Send(t.Context(), Message{
		ID: "transactional@example.test", From: "sender@example.test", To: "recipient@example.test",
		Subject: "Receipt", Text: "Your receipt", RequestDSN: true,
	})
	if err != nil {
		t.Fatalf("SMTPTransport.Send() error = %v", err)
	}
	if result == nil || !result.Accepted {
		t.Fatalf("SMTPTransport.Send() result = %+v, want accepted result", result)
	}
	select {
	case command := <-rcptCommand:
		if !strings.Contains(command, "NOTIFY=SUCCESS,FAILURE,DELAY") {
			t.Fatalf("RCPT command = %q, want delivery notification parameters", command)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP server did not receive RCPT command")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("SMTP test server: %v", err)
	}
}
