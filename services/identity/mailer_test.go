package identity

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func startSMTPTestServer(t *testing.T, mechanism string, rejectAuth bool) (string, <-chan []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	commands := make(chan []string, 1)
	go func() {
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(line string) bool {
			if _, err := fmt.Fprintf(writer, "%s\r\n", line); err != nil {
				return false
			}
			return writer.Flush() == nil
		}
		read := func() (string, bool) {
			line, err := reader.ReadString('\n')
			if err != nil {
				return "", false
			}
			return strings.TrimRight(line, "\r\n"), true
		}
		_ = write("220 smtp.test ESMTP")
		seen := make([]string, 0)
		authenticated := false
		for {
			line, ok := read()
			if !ok {
				commands <- seen
				return
			}
			seen = append(seen, line)
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO "), strings.HasPrefix(upper, "HELO "):
				_ = write("250-smtp.test")
				_ = write("250-AUTH " + mechanism)
				_ = write("250 OK")
			case strings.HasPrefix(upper, "AUTH PLAIN"):
				parts := strings.Fields(line)
				if len(parts) == 3 {
					decoded, _ := base64.StdEncoding.DecodeString(parts[2])
					if string(decoded) != "\x00mailer\x00smtp-secret" {
						_ = write("535 5.7.8 invalid credentials")
						continue
					}
					if rejectAuth {
						_ = write("535 5.7.8 relay credentials rejected " + parts[2])
						continue
					}
					authenticated = true
					_ = write("235 2.7.0 authenticated")
					continue
				}
				_ = write("501 5.5.4 invalid AUTH PLAIN")
			case strings.HasPrefix(upper, "AUTH LOGIN"):
				_ = write("334 VXNlcm5hbWU6")
				username, ok := read()
				if !ok {
					commands <- seen
					return
				}
				seen = append(seen, username)
				_ = write("334 UGFzc3dvcmQ6")
				password, ok := read()
				if !ok {
					commands <- seen
					return
				}
				seen = append(seen, password)
				decodedUser, _ := base64.StdEncoding.DecodeString(username)
				decodedPassword, _ := base64.StdEncoding.DecodeString(password)
				if string(decodedUser) != "mailer" || string(decodedPassword) != "smtp-secret" || rejectAuth {
					_ = write("535 5.7.8 relay credentials rejected")
					continue
				}
				authenticated = true
				_ = write("235 2.7.0 authenticated")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				if !authenticated {
					_ = write("530 5.7.0 authentication required")
					continue
				}
				_ = write("250 2.1.0 sender accepted")
			case strings.HasPrefix(upper, "RCPT TO:"):
				_ = write("250 2.1.5 recipient accepted")
			case upper == "DATA":
				_ = write("354 end with dot")
				for {
					data, ok := read()
					if !ok {
						commands <- seen
						return
					}
					if data == "." {
						break
					}
				}
				_ = write("250 2.0.0 queued")
			case upper == "QUIT":
				_ = write("221 2.0.0 goodbye")
				commands <- seen
				return
			default:
				_ = write("500 5.5.1 unsupported command")
			}
		}
	}()
	return listener.Addr().String(), commands
}

func TestSMTPMailerSupportsPlainAndLogin(t *testing.T) {
	for _, mechanism := range []string{"PLAIN", "LOGIN"} {
		t.Run(mechanism, func(t *testing.T) {
			addr, commands := startSMTPTestServer(t, mechanism, false)
			mailer := &smtpMailer{
				addr: addr, host: "localhost", username: "mailer",
				password: "smtp-secret", from: "noreply@example.test", tlsMode: "none",
			}
			if err := mailer.Send(context.Background(), MailMessage{
				To: "person@example.test", Subject: "Test", HTML: "<p>Message</p>",
			}); err != nil {
				t.Fatalf("Send() error = %v", err)
			}
			select {
			case seen := <-commands:
				joined := strings.Join(seen, "\n")
				if !strings.Contains(joined, "MAIL FROM:<noreply@example.test>") ||
					!strings.Contains(joined, "RCPT TO:<person@example.test>") {
					t.Fatalf("SMTP transaction missing envelope commands: %s", joined)
				}
				if strings.Contains(joined, "smtp-secret") {
					t.Fatal("SMTP password appeared in the protocol log")
				}
			case <-time.After(time.Second):
				t.Fatal("SMTP test server did not finish")
			}
		})
	}
}

func TestSMTPProbeAuthenticatesWithoutSending(t *testing.T) {
	addr, commands := startSMTPTestServer(t, "PLAIN", false)
	mailer := &smtpMailer{
		addr: addr, host: "localhost", username: "mailer",
		password: "smtp-secret", from: "noreply@example.test", tlsMode: "none",
	}
	if err := mailer.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	select {
	case seen := <-commands:
		for _, command := range seen {
			if strings.HasPrefix(strings.ToUpper(command), "MAIL FROM:") ||
				strings.HasPrefix(strings.ToUpper(command), "RCPT TO:") ||
				strings.EqualFold(command, "DATA") {
				t.Fatalf("probe attempted to send mail: %q", command)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP test server did not finish")
	}
}

func TestSMTPMailerUsesRequiredSTARTTLS(t *testing.T) {
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := certServer.TLS.Certificates[0]
	parsedCertificate, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("parse test certificate: %v", err)
	}
	certServer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(parsedCertificate)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() {
		defer listener.Close()
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(line string) error {
			if _, err := fmt.Fprintf(writer, "%s\r\n", line); err != nil {
				return err
			}
			return writer.Flush()
		}
		read := func() (string, error) {
			line, err := reader.ReadString('\n')
			return strings.TrimRight(line, "\r\n"), err
		}
		if err := write("220 smtp.test ESMTP"); err != nil {
			serverDone <- err
			return
		}
		line, err := read()
		if err != nil || !strings.HasPrefix(strings.ToUpper(line), "EHLO ") {
			serverDone <- fmt.Errorf("initial EHLO = %q, err = %v", line, err)
			return
		}
		for _, response := range []string{"250-smtp.test", "250-STARTTLS", "250 OK"} {
			if err := write(response); err != nil {
				serverDone <- err
				return
			}
		}
		line, err = read()
		if err != nil || !strings.EqualFold(line, "STARTTLS") {
			serverDone <- fmt.Errorf("STARTTLS command = %q, err = %v", line, err)
			return
		}
		if err := write("220 ready to start TLS"); err != nil {
			serverDone <- err
			return
		}
		tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		if err := tlsConn.Handshake(); err != nil {
			serverDone <- err
			return
		}
		reader = bufio.NewReader(tlsConn)
		writer = bufio.NewWriter(tlsConn)
		line, err = read()
		if err != nil || !strings.HasPrefix(strings.ToUpper(line), "EHLO ") {
			serverDone <- fmt.Errorf("TLS EHLO = %q, err = %v", line, err)
			return
		}
		for _, response := range []string{"250-smtp.test", "250-AUTH PLAIN", "250 OK"} {
			if err := write(response); err != nil {
				serverDone <- err
				return
			}
		}
		line, err = read()
		parts := strings.Fields(line)
		if err != nil || len(parts) != 3 || !strings.EqualFold(parts[0], "AUTH") || !strings.EqualFold(parts[1], "PLAIN") {
			serverDone <- fmt.Errorf("AUTH command = %q, err = %v", line, err)
			return
		}
		auth, _ := base64.StdEncoding.DecodeString(parts[2])
		if string(auth) != "\x00mailer\x00smtp-secret" {
			serverDone <- fmt.Errorf("AUTH PLAIN value did not match test credentials")
			return
		}
		if err := write("235 2.7.0 authenticated"); err != nil {
			serverDone <- err
			return
		}
		line, err = read()
		if err != nil || !strings.EqualFold(line, "QUIT") {
			serverDone <- fmt.Errorf("QUIT command = %q, err = %v", line, err)
			return
		}
		serverDone <- write("221 2.0.0 goodbye")
	}()

	mailer := &smtpMailer{
		addr: listener.Addr().String(), host: "example.com", username: "mailer",
		password: "smtp-secret", from: "noreply@example.test", tlsMode: "starttls",
		tlsConfig: &tls.Config{RootCAs: roots},
	}
	if err := mailer.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("SMTP test server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP test server did not finish")
	}
}

func TestSMTPAuthErrorIncludesCodeAndResponse(t *testing.T) {
	addr, _ := startSMTPTestServer(t, "PLAIN", true)
	mailer := &smtpMailer{
		addr: addr, host: "localhost", username: "mailer",
		password: "smtp-secret", from: "noreply@example.test", tlsMode: "none",
	}
	err := mailer.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe() error = nil, want rejected credentials")
	}
	if !strings.Contains(err.Error(), "535") || !strings.Contains(err.Error(), "relay credentials rejected") {
		t.Fatalf("Probe() error = %q, want SMTP response code and text", err)
	}
	if strings.Contains(err.Error(), "smtp-secret") {
		t.Fatal("SMTP error exposed the configured password")
	}
	if strings.Contains(err.Error(), base64.StdEncoding.EncodeToString([]byte("\x00mailer\x00smtp-secret"))) {
		t.Fatal("SMTP error exposed the encoded AUTH credentials")
	}
}
