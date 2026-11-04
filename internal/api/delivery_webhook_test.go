package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/httpx"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testHandler(secret string) *DeliveryHandler {
	return &DeliveryHandler{
		webhookSecret: secret,
		logger:        testLogger(),
		idempotency:   newIdempotencyStore(),
	}
}

func computeWebhookSignature(secret string, timestamp int64, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", timestamp, body)))
	return fmt.Sprintf("t=%d, v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

func TestVerifyWebhookSignatureValid(t *testing.T) {
	h := &DeliveryHandler{webhookSecret: "test-secret-key-0123456789abcdef"}
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Unix()
	sig := computeWebhookSignature("test-secret-key-0123456789abcdef", ts, body)

	if err := h.verifyWebhookSignature([]byte(body), sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestVerifyWebhookSignatureMissing(t *testing.T) {
	h := &DeliveryHandler{webhookSecret: "test-secret-key-0123456789abcdef"}
	body := `{"event":"bounce","email":"user@example.com"}`

	if err := h.verifyWebhookSignature([]byte(body), ""); err == nil {
		t.Fatal("empty signature should be rejected")
	}
	if err := h.verifyWebhookSignature([]byte(body), "t=123"); err == nil {
		t.Fatal("signature without v1 should be rejected")
	}
	if err := h.verifyWebhookSignature([]byte(body), "v1=abc"); err == nil {
		t.Fatal("signature without t should be rejected")
	}
}

func TestVerifyWebhookSignatureWrongSecret(t *testing.T) {
	h := &DeliveryHandler{webhookSecret: "correct-secret"}
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Unix()
	sig := computeWebhookSignature("wrong-secret", ts, body)

	if err := h.verifyWebhookSignature([]byte(body), sig); err == nil {
		t.Fatal("wrong secret signature should be rejected")
	}
}

func TestVerifyWebhookSignatureReplayOld(t *testing.T) {
	h := &DeliveryHandler{webhookSecret: "test-secret-key-0123456789abcdef"}
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Add(-10 * time.Minute).Unix()
	sig := computeWebhookSignature("test-secret-key-0123456789abcdef", ts, body)

	if err := h.verifyWebhookSignature([]byte(body), sig); err == nil {
		t.Fatal("old timestamp should be rejected")
	}
}

func TestVerifyWebhookSignatureFuture(t *testing.T) {
	h := &DeliveryHandler{webhookSecret: "test-secret-key-0123456789abcdef"}
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Add(10 * time.Minute).Unix()
	sig := computeWebhookSignature("test-secret-key-0123456789abcdef", ts, body)

	if err := h.verifyWebhookSignature([]byte(body), sig); err == nil {
		t.Fatal("future timestamp should be rejected")
	}
}

func TestVerifyWebhookSignatureTamperedBody(t *testing.T) {
	h := &DeliveryHandler{webhookSecret: "test-secret-key-0123456789abcdef"}
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Unix()
	sig := computeWebhookSignature("test-secret-key-0123456789abcdef", ts, body)

	tampered := `{"event":"bounce","email":"attacker@example.com"}`
	if err := h.verifyWebhookSignature([]byte(tampered), sig); err == nil {
		t.Fatal("tampered body should be rejected")
	}
}

func TestIdempotencyStoreNewKey(t *testing.T) {
	store := newIdempotencyStore()
	if !store.checkAndMark("key-1") {
		t.Fatal("new key should be accepted")
	}
	if store.checkAndMark("key-1") {
		t.Fatal("duplicate key should be rejected")
	}
	if !store.checkAndMark("key-2") {
		t.Fatal("different new key should be accepted")
	}
}

func TestIdempotencyStoreEmptyKey(t *testing.T) {
	store := newIdempotencyStore()
	if !store.checkAndMark("") {
		t.Fatal("empty key should always be accepted")
	}
	if !store.checkAndMark("") {
		t.Fatal("empty key should always be accepted on second call too")
	}
}

func TestRequireWebhookAuthValid(t *testing.T) {
	secret := "test-secret-key-0123456789abcdef"
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Unix()
	sig := computeWebhookSignature(secret, ts, body)

	handler := testHandler(secret)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest("POST", "/api/v1/webhooks/bounce", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", sig)
	rec := httptest.NewRecorder()

	handler.requireWebhookAuth(next.ServeHTTP)(rec, req)

	if !called {
		t.Fatal("inner handler should be called for valid webhook")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequireWebhookAuthInvalidSignature(t *testing.T) {
	secret := "test-secret-key-0123456789abcdef"
	body := `{"event":"bounce","email":"user@example.com"}`

	handler := testHandler(secret)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest("POST", "/api/v1/webhooks/bounce", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", "t=1234567890, v1=badsignature")
	rec := httptest.NewRecorder()

	handler.requireWebhookAuth(next.ServeHTTP)(rec, req)

	if called {
		t.Fatal("inner handler should NOT be called for invalid signature")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRequireWebhookAuthDuplicateIdempotencyKey(t *testing.T) {
	secret := "test-secret-key-0123456789abcdef"
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Unix()
	sig := computeWebhookSignature(secret, ts, body)

	handler := testHandler(secret)
	callCount := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusNoContent)
	})

	req1 := httptest.NewRequest("POST", "/api/v1/webhooks/bounce", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-Webhook-Signature", sig)
	req1.Header.Set("Idempotency-Key", "idem-key-1")
	rec1 := httptest.NewRecorder()
	handler.requireWebhookAuth(next.ServeHTTP)(rec1, req1)
	if callCount != 1 {
		t.Fatalf("first call count = %d, want 1", callCount)
	}

	req2 := httptest.NewRequest("POST", "/api/v1/webhooks/bounce", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Webhook-Signature", sig)
	req2.Header.Set("Idempotency-Key", "idem-key-1")
	rec2 := httptest.NewRecorder()
	handler.requireWebhookAuth(next.ServeHTTP)(rec2, req2)
	if callCount != 1 {
		t.Fatalf("duplicate call count = %d, want 1", callCount)
	}
	if rec2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec2.Code, http.StatusConflict)
	}
}

func TestRequireWebhookAuthRestoresBody(t *testing.T) {
	secret := "test-secret-key-0123456789abcdef"
	body := `{"event":"bounce","email":"user@example.com"}`
	ts := time.Now().Unix()
	sig := computeWebhookSignature(secret, ts, body)

	handler := testHandler(secret)
	var readBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		readBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest("POST", "/api/v1/webhooks/bounce", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", sig)
	rec := httptest.NewRecorder()

	handler.requireWebhookAuth(next.ServeHTTP)(rec, req)

	if readBody != body {
		t.Fatalf("body = %q, want %q", readBody, body)
	}
}

var _ = httpx.JSON
var _ = auth.RoleOwner