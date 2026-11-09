package identity

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/testutil"
)

type fakeSMTPRelay struct {
	listener net.Listener
	attempts int
	failures chan string
	accepted chan string
}

func newFakeSMTPRelay(t *testing.T) *fakeSMTPRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for fake SMTP relay: %v", err)
	}
	relay := &fakeSMTPRelay{
		listener: listener,
		failures: make(chan string, 2),
		accepted: make(chan string, 2),
	}
	go relay.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (s *fakeSMTPRelay) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPRelay) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	reply := func(message string) bool {
		if _, err := fmt.Fprintf(writer, "%s\r\n", message); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !reply("220 fake.smtp.test ESMTP ready") {
		return
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO "), strings.HasPrefix(command, "HELO "):
			if _, err := io.WriteString(writer, "250-fake.smtp.test\r\n250 SIZE 10485760\r\n"); err != nil || writer.Flush() != nil {
				return
			}
		case strings.HasPrefix(command, "MAIL FROM:"), strings.HasPrefix(command, "RCPT TO:"):
			if !reply("250 accepted") {
				return
			}
		case command == "DATA":
			if !reply("354 end with <CRLF>.<CRLF>") {
				return
			}
			var data strings.Builder
			for {
				messageLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if strings.TrimRight(messageLine, "\r\n") == "." {
					break
				}
				data.WriteString(messageLine)
			}
			s.attempts++
			if s.attempts == 1 {
				s.failures <- data.String()
				if !reply("451 temporary relay failure") {
					return
				}
				continue
			}
			s.accepted <- data.String()
			if !reply("250 queued") {
				return
			}
		case command == "QUIT":
			_ = reply("221 bye")
			return
		default:
			if !reply("250 ok") {
				return
			}
		}
	}
}

func TestPostgresRegistrationQueuesThenRetriesThroughFakeSMTP(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	relay := newFakeSMTPRelay(t)
	cfg := Config{
		BaseConfig:              kit.BaseConfig{JWTSecret: "fake-smtp-integration-jwt", JWTIssuer: "email-platform"},
		AccessTokenTTL:          time.Minute,
		AppBaseURL:              "https://sendwise.test",
		MailOutboxEncryptionKey: "fake-smtp-integration-outbox-key",
	}
	store := NewStoreFromPool(pool)
	mailer := &smtpMailer{
		addr:    relay.listener.Addr().String(),
		host:    "127.0.0.1",
		from:    "noreply@example.test",
		tlsMode: "none",
	}
	handler := NewHandler(
		store,
		kit.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL),
		nil,
		mailer,
		cfg,
		kit.NewTestLogger(),
	)
	handler.mailRetryDelay = 10 * time.Millisecond
	response := doJSON(t, handler.Routes(), "POST", "/auth/register",
		`{"email":"smtp-outage@example.test","password":"correct horse battery staple","name":"SMTP Outage","organization_name":"Relay Test"}`)
	if response.Code != 201 {
		t.Fatalf("registration status = %d, body = %s", response.Code, response.Body.String())
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_delivery_outbox WHERE attempts = 0`).Scan(&queued); err != nil {
		t.Fatalf("count initial queued email: %v", err)
	}
	if queued != 1 {
		t.Fatalf("initial queued emails = %d, want 1", queued)
	}
	if relay.attempts != 0 {
		t.Fatalf("relay attempts before worker start = %d, want 0", relay.attempts)
	}

	retryCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		handler.RunMailRetries(retryCtx)
		close(done)
	}()
	select {
	case failedMessage := <-relay.failures:
		if !strings.Contains(failedMessage, "To: smtp-outage@example.test") {
			cancel()
			t.Fatalf("first SMTP message recipient missing: %s", failedMessage)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("worker did not attempt the queued SMTP message")
	}
	var attempts int
	var lastError sql.NullString
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT attempts, last_error FROM mail_delivery_outbox LIMIT 1`).Scan(&attempts, &lastError); err != nil {
			cancel()
			t.Fatalf("read failed attempt: %v", err)
		}
		if attempts == 1 && lastError.Valid && strings.Contains(lastError.String, "451") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attempts != 1 || !lastError.Valid || !strings.Contains(lastError.String, "451") {
		cancel()
		t.Fatalf("recorded attempt/error = %d/%q, want attempt 1 and SMTP 451", attempts, lastError.String)
	}
	select {
	case deliveredMessage := <-relay.accepted:
		if !strings.Contains(deliveredMessage, "Verify your email address") {
			cancel()
			t.Fatalf("delivered SMTP message has no verification subject: %s", deliveredMessage)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not retry after the fake relay recovered")
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_delivery_outbox`).Scan(&queued); err != nil {
			cancel()
			t.Fatalf("count completed outbox entries: %v", err)
		}
		if queued == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if queued != 0 {
		cancel()
		t.Fatalf("outbox rows after successful retry = %d, want 0", queued)
	}
	cancel()
	<-done
}

func TestPostgresMailRetryOutboxSurvivesHandlerRestart(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	store := NewStoreFromPool(pool).(*pgxStore)
	const encryptionKey = "mail-outbox-integration-encryption-key"
	message := MailMessage{
		To:      "retry@example.test",
		Subject: "Verify your address",
		Text:    "https://sendwise.test/auth/verify?token=secret-token-value",
	}
	cfg := Config{
		BaseConfig:              kit.BaseConfig{JWTSecret: "outbox-test-jwt-secret", JWTIssuer: "email-platform"},
		AccessTokenTTL:          time.Minute,
		MailOutboxEncryptionKey: encryptionKey,
	}
	tokens := kit.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)
	initial := NewHandler(store, tokens, nil, failingMailer{err: context.DeadlineExceeded}, cfg, kit.NewTestLogger())
	initial.mailRetryDelay = time.Millisecond
	if err := initial.deliverMail(ctx, "verification", message); !errors.Is(err, ErrMailRetryQueued) {
		t.Fatalf("initial delivery error = %v, want retry queued", err)
	}

	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT encrypted_message FROM mail_delivery_outbox`).Scan(&stored); err != nil {
		t.Fatalf("read stored retry: %v", err)
	}
	if strings.Contains(string(stored), "secret-token-value") {
		t.Fatal("retry outbox stored the verification token in plaintext")
	}

	restartedStore := NewStoreFromPool(pool)
	mailer := &retryMailer{delivered: make(chan struct{}, 1)}
	restarted := NewHandler(restartedStore, tokens, nil, mailer, cfg, kit.NewTestLogger())
	restarted.mailRetryDelay = time.Millisecond
	retryCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		restarted.RunMailRetries(retryCtx)
		close(done)
	}()
	select {
	case <-mailer.delivered:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("new handler did not deliver the persisted retry")
	}
	var pending int
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM mail_delivery_outbox`).Scan(&pending); err != nil {
			cancel()
			t.Fatalf("count remaining retries: %v", err)
		}
		if pending == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if pending != 0 {
		t.Fatalf("pending retries = %d, want 0 after delivery", pending)
	}
}

func TestPostgresMailRetryLeasePreventsDuplicateClaims(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	store := NewStoreFromPool(pool).(*pgxStore)
	payload, err := delivery.EncryptSecret("mail-outbox-test-key", `{"to":"lease@example.test"}`)
	if err != nil {
		t.Fatalf("encrypt retry: %v", err)
	}
	if err := store.EnqueueMailRetry(ctx, "verification", payload, 1, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("enqueue retry: %v", err)
	}
	firstClaim, err := store.ClaimMailRetries(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("claim retry: %v", err)
	}
	if len(firstClaim) != 1 {
		t.Fatalf("first claim count = %d, want 1", len(firstClaim))
	}
	secondClaim, err := store.ClaimMailRetries(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("claim leased retry: %v", err)
	}
	if len(secondClaim) != 0 {
		t.Fatalf("second claim count = %d, want 0 while first lease is active", len(secondClaim))
	}
	if err := store.CompleteMailRetry(ctx, firstClaim[0].id, firstClaim[0].leaseToken); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
}
