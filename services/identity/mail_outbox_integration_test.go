package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/testutil"
)

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
