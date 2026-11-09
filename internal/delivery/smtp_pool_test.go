package delivery

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSMTPPoolReusesAuthenticatedConnectionAndVERPEnvelope(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var connections atomic.Int32
	var messages atomic.Int32
	var verpEnvelope atomic.Bool
	var quitCommands atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go servePoolTestConnection(conn, &messages, &verpEnvelope, &quitCommands)
		}
	}()

	pool := NewSMTPPool(listener.Addr().String(), "localhost", "submit", "test-only-password", true, 1)
	for _, id := range []string{"one", "two"} {
		if _, err := pool.Send(context.Background(), Message{
			ID: id, From: "Sender <sender@example.test>",
			EnvelopeFrom: "bounces+token@example.test",
			To:           "person@example.test", Subject: "Pool test", Text: "body",
		}); err != nil {
			t.Fatalf("Send(%q): %v", id, err)
		}
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("SMTP connections = %d, want 1 reused connection", got)
	}
	if got := messages.Load(); got != 2 {
		t.Fatalf("messages accepted = %d, want 2", got)
	}
	if !verpEnvelope.Load() {
		t.Fatal("SMTP MAIL FROM did not use the VERP envelope sender")
	}
	if got := quitCommands.Load(); got != 1 {
		t.Fatalf("SMTP QUIT commands = %d, want 1 graceful pool close", got)
	}
}

func TestSMTPPoolConcurrentSendersShareAvailableConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var connections atomic.Int32
	var messages atomic.Int32
	var verpEnvelope atomic.Bool
	var quitCommands atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go servePoolTestConnection(conn, &messages, &verpEnvelope, &quitCommands)
		}
	}()

	pool := NewSMTPPool(listener.Addr().String(), "localhost", "submit", "test-only-password", true, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errs := make(chan error, 2)
	for _, id := range []string{"concurrent-one", "concurrent-two"} {
		go func(id string) {
			_, err := pool.Send(ctx, Message{
				ID: id, From: "sender@example.test", To: "person@example.test",
				Subject: "Concurrent pool test", Text: "body",
			})
			errs <- err
		}(id)
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent send failed: %v", err)
		}
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("SMTP connections = %d, want one shared connection", got)
	}
	if got := messages.Load(); got != 2 {
		t.Fatalf("messages accepted = %d, want 2", got)
	}
	if got := quitCommands.Load(); got != 1 {
		t.Fatalf("SMTP QUIT commands = %d, want 1 graceful pool close", got)
	}
}

func servePoolTestConnection(conn net.Conn, messages *atomic.Int32, verpEnvelope *atomic.Bool, quitCommands *atomic.Int32) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	_, _ = io.WriteString(conn, "220 smtp.test ESMTP\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
			_, _ = io.WriteString(conn, "250-smtp.test\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(command, "AUTH PLAIN "):
			_, _ = io.WriteString(conn, "235 2.7.0 authenticated\r\n")
		case strings.HasPrefix(command, "MAIL FROM:"):
			if strings.Contains(command, "BOUNCES+TOKEN@EXAMPLE.TEST") {
				verpEnvelope.Store(true)
			}
			_, _ = io.WriteString(conn, "250 2.1.0 accepted\r\n")
		case strings.HasPrefix(command, "RCPT TO:"):
			_, _ = io.WriteString(conn, "250 2.1.0 accepted\r\n")
		case command == "DATA":
			_, _ = io.WriteString(conn, "354 end with dot\r\n")
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					messages.Add(1)
					_, _ = io.WriteString(conn, "250 2.0.0 queued\r\n")
					break
				}
			}
		case command == "RSET":
			_, _ = io.WriteString(conn, "250 2.0.0 reset\r\n")
		case command == "QUIT":
			quitCommands.Add(1)
			_, _ = fmt.Fprint(conn, "221 2.0.0 bye\r\n")
			return
		default:
			_, _ = fmt.Fprintf(conn, "500 unexpected command: %s\r\n", command)
		}
	}
}
