package transactional

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/kit"
)

const (
	webhookPollInterval = 500 * time.Millisecond
	webhookMaxAttempts  = 10
	maxWebhookReplySize = 4 << 10
)

type WebhookWorker struct {
	pool   *pgxpool.Pool
	cfg    config.Config
	logger *slog.Logger
}

type webhookDelivery struct {
	ID              string
	WebhookID       string
	URL             string
	EncryptedSecret []byte
	Payload         []byte
	Attempts        int
}

func NewWebhookWorker(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *WebhookWorker {
	return &WebhookWorker{pool: pool, cfg: cfg, logger: logger}
}

func (w *WebhookWorker) Run(ctx context.Context) error {
	if !w.cfg.TransactionalEnabled {
		return nil
	}
	if strings.TrimSpace(w.cfg.DKIMEncryptionKey) == "" {
		return errors.New("DKIM_ENCRYPTION_KEY is required to encrypt transactional webhook secrets")
	}
	for ctx.Err() == nil {
		worked, err := w.processOne(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.Error("process transactional webhook delivery", "error", err)
		}
		if !worked || err != nil {
			waitContext(ctx, webhookPollInterval)
		}
	}
	return nil
}

func (w *WebhookWorker) processOne(ctx context.Context) (bool, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin webhook claim: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := enqueueWebhookEvents(ctx, tx); err != nil {
		return false, err
	}

	var delivery webhookDelivery
	err = tx.QueryRow(ctx, `
		SELECT d.id::text, d.webhook_id::text, w.url, w.encrypted_secret,
		       d.payload, d.attempts + 1
		FROM transactional_webhook_deliveries d
		JOIN transactional_webhooks w ON w.id = d.webhook_id
		WHERE d.delivered_at IS NULL AND d.next_attempt_at <= now()
		  AND w.enabled AND w.disabled_at IS NULL
		ORDER BY d.next_attempt_at, d.created_at, d.id
		FOR UPDATE OF d SKIP LOCKED
		LIMIT 1
	`).Scan(
		&delivery.ID, &delivery.WebhookID, &delivery.URL, &delivery.EncryptedSecret,
		&delivery.Payload, &delivery.Attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit empty webhook claim: %w", err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select transactional webhook delivery: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transactional_webhook_deliveries SET attempts = $2 WHERE id = $1
	`, delivery.ID, delivery.Attempts); err != nil {
		return false, fmt.Errorf("lease transactional webhook delivery: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit transactional webhook claim: %w", err)
	}

	secret, err := DecryptWebhookSecret(w.cfg.DKIMEncryptionKey, delivery.EncryptedSecret)
	if err != nil {
		return true, w.recordWebhookFailure(ctx, delivery, 0, "could not decrypt webhook signing secret")
	}
	code, responseBody, sendErr := sendWebhook(ctx, delivery.URL, secret, delivery.ID, delivery.Payload)
	if sendErr != nil {
		return true, w.recordWebhookFailure(ctx, delivery, code, sendErr.Error()+responseBody)
	}
	if code < http.StatusOK || code >= http.StatusMultipleChoices {
		return true, w.recordWebhookFailure(ctx, delivery, code, responseBody)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE transactional_webhook_deliveries
		SET delivered_at = now(), response_code = $2, response_body = $3
		WHERE id = $1
	`, delivery.ID, code, responseBody); err != nil {
		return true, fmt.Errorf("record successful webhook delivery: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE transactional_webhooks SET consecutive_failures = 0, updated_at = now()
		WHERE id = $1
	`, delivery.WebhookID); err != nil {
		return true, fmt.Errorf("reset webhook failure count: %w", err)
	}
	return true, nil
}

func enqueueWebhookEvents(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO transactional_webhook_deliveries
			(webhook_id, message_event_id, event_type, payload)
		SELECT w.id, e.id, e.event_type,
		       jsonb_build_object(
		           'id', e.id, 'type', e.event_type, 'occurred_at', e.occurred_at,
		           'organization_id', e.organization_id, 'message_id', e.message_id,
		           'recipient_id', e.recipient_id, 'smtp_code', e.smtp_code,
		           'enhanced_code', e.enhanced_code, 'smtp_response', e.smtp_response,
		           'metadata', e.metadata)
		FROM transactional_message_events e
		JOIN transactional_webhooks w
		  ON w.organization_id = e.organization_id
		 AND e.event_type = ANY(w.events)
		WHERE w.enabled AND w.disabled_at IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM transactional_webhook_deliveries d
		      WHERE d.webhook_id = w.id AND d.message_event_id = e.id
		  )
		ORDER BY e.id
		LIMIT 500
		ON CONFLICT DO NOTHING
	`); err != nil {
		return fmt.Errorf("enqueue transactional webhook events: %w", err)
	}
	return nil
}

func (w *WebhookWorker) recordWebhookFailure(
	ctx context.Context,
	delivery webhookDelivery,
	statusCode int,
	responseBody string,
) error {
	if len(responseBody) > maxWebhookReplySize {
		responseBody = responseBody[:maxWebhookReplySize]
	}
	delay := webhookRetryDelay(delivery.Attempts)
	_, err := w.pool.Exec(ctx, `
		UPDATE transactional_webhook_deliveries
		SET response_code = NULLIF($2, 0), response_body = $3,
		    next_attempt_at = now() + $4::interval
		WHERE id = $1
	`, delivery.ID, statusCode, responseBody, fmt.Sprintf("%.6f seconds", delay.Seconds()))
	if err != nil {
		return fmt.Errorf("schedule transactional webhook retry: %w", err)
	}
	_, err = w.pool.Exec(ctx, `
		UPDATE transactional_webhooks
		SET consecutive_failures = consecutive_failures + 1,
		    enabled = CASE WHEN consecutive_failures + 1 >= $2 THEN false ELSE enabled END,
		    disabled_at = CASE WHEN consecutive_failures + 1 >= $2 THEN COALESCE(disabled_at, now()) ELSE disabled_at END,
		    updated_at = now()
		WHERE id = $1
	`, delivery.WebhookID, webhookMaxAttempts)
	if err != nil {
		return fmt.Errorf("update transactional webhook failure count: %w", err)
	}
	return nil
}

func EncryptWebhookSecret(master, secret string) ([]byte, error) {
	if len(master) < 16 || secret == "" {
		return nil, errors.New("webhook encryption key and secret are required")
	}
	key := sha256.Sum256([]byte("sendwise/transactional-webhook/v1:" + master))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create webhook encryption cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create webhook encryption mode: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate webhook encryption nonce: %w", err)
	}
	return append(nonce, aead.Seal(nil, nonce, []byte(secret), nil)...), nil
}

func DecryptWebhookSecret(master string, ciphertext []byte) (string, error) {
	if len(master) < 16 {
		return "", errors.New("webhook encryption key is not configured")
	}
	key := sha256.Sum256([]byte("sendwise/transactional-webhook/v1:" + master))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("create webhook decryption cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create webhook decryption mode: %w", err)
	}
	if len(ciphertext) < aead.NonceSize() {
		return "", errors.New("webhook signing secret ciphertext is truncated")
	}
	nonce, body := ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():]
	secret, err := aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt webhook signing secret: %w", err)
	}
	return string(secret), nil
}

func NewWebhookSigningSecret() (string, error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return "", fmt.Errorf("generate webhook signing secret: %w", err)
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(material), nil
}

func ValidateWebhookURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("webhook URL must be an absolute HTTPS URL without user information")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return errors.New("webhook URL must use HTTPS port 443")
	}
	if address := net.ParseIP(parsed.Hostname()); address != nil && !kit.IsPublicIP(address) {
		return errors.New("webhook URL cannot target a private or reserved IP address")
	}
	return nil
}

func sendWebhook(
	ctx context.Context,
	rawURL string,
	secret string,
	deliveryID string,
	payload []byte,
) (int, string, error) {
	if err := ValidateWebhookURL(rawURL); err != nil {
		return 0, "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return 0, "", fmt.Errorf("parse webhook URL: %w", err)
	}
	parsed.Fragment = ""
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				if !strings.EqualFold(host, parsed.Hostname()) || port != "443" {
					return nil, errors.New("webhook transport attempted an unexpected destination")
				}
				resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, fmt.Errorf("resolve webhook host: %w", err)
				}
				if len(resolved) == 0 {
					return nil, errors.New("webhook host resolved to no addresses")
				}
				var selected net.IP
				for _, item := range resolved {
					if !kit.IsPublicIP(item.IP) {
						return nil, errors.New("webhook host resolved to a private or reserved address")
					}
					if selected == nil || item.IP.To4() != nil {
						selected = item.IP
					}
				}
				return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(
					ctx, network, net.JoinHostPort(selected.String(), port),
				)
			},
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
	if err != nil {
		return 0, "", fmt.Errorf("create webhook request: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(payload)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Sendwise-Transactional-Webhooks/1")
	request.Header.Set("X-Sendwise-Delivery", deliveryID)
	request.Header.Set("X-Sendwise-Timestamp", timestamp)
	request.Header.Set("X-Sendwise-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	response, err := client.Do(request)
	if err != nil {
		return 0, "", fmt.Errorf("send webhook request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxWebhookReplySize+1))
	if err != nil {
		return response.StatusCode, "", fmt.Errorf("read webhook response: %w", err)
	}
	if len(body) > maxWebhookReplySize {
		body = body[:maxWebhookReplySize]
	}
	return response.StatusCode, string(body), nil
}

func webhookRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		attempt = 10
	}
	delay := 10 * time.Second * time.Duration(1<<uint(attempt-1))
	if delay > 6*time.Hour {
		return 6 * time.Hour
	}
	return delay
}
