package transactional

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SMTPCredential struct {
	ID             string
	OrganizationID string
	Username       string
	RequestsPerSec int
}

var ErrInvalidSMTPCredentials = errors.New("invalid SMTP credentials")

func NewSMTPSecret() (username, password, passwordHash string, err error) {
	usernameBytes := make([]byte, 18)
	passwordBytes := make([]byte, 32)
	if _, err := rand.Read(usernameBytes); err != nil {
		return "", "", "", fmt.Errorf("generate SMTP username: %w", err)
	}
	if _, err := rand.Read(passwordBytes); err != nil {
		return "", "", "", fmt.Errorf("generate SMTP password: %w", err)
	}
	username = "sw_" + base64.RawURLEncoding.EncodeToString(usernameBytes)
	password = "sw_smtp_" + base64.RawURLEncoding.EncodeToString(passwordBytes)
	digest := sha256.Sum256([]byte(password))
	return username, password, hex.EncodeToString(digest[:]), nil
}

func AuthenticateSMTP(ctx context.Context, pool *pgxpool.Pool, username, password string) (SMTPCredential, error) {
	var credential SMTPCredential
	var storedHash string
	err := pool.QueryRow(ctx, `
		SELECT id::text, organization_id::text, username, requests_per_second, secret_hash
		FROM transactional_smtp_credentials
		WHERE username = $1 AND revoked_at IS NULL
	`, username).Scan(&credential.ID, &credential.OrganizationID, &credential.Username,
		&credential.RequestsPerSec, &storedHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SMTPCredential{}, ErrInvalidSMTPCredentials
		}
		return SMTPCredential{}, fmt.Errorf("load SMTP credential: %w", err)
	}
	digest := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hex.EncodeToString(digest[:]))) != 1 {
		return SMTPCredential{}, ErrInvalidSMTPCredentials
	}
	return credential, nil
}

func (credential SMTPCredential) ReserveRequest(ctx context.Context, pool *pgxpool.Pool) error {
	tag, err := pool.Exec(ctx, `
		UPDATE transactional_smtp_credentials
		SET rate_window_count = CASE
		        WHEN rate_window_started_at IS NULL OR rate_window_started_at < date_trunc('second', now())
		        THEN 1 ELSE rate_window_count + 1
		    END,
		    rate_window_started_at = date_trunc('second', now()),
		    last_used_at = now()
		WHERE id = $1 AND revoked_at IS NULL
		  AND (rate_window_started_at IS NULL
		       OR rate_window_started_at < date_trunc('second', now())
		       OR rate_window_count < requests_per_second)
	`, credential.ID)
	if err != nil {
		return fmt.Errorf("reserve SMTP request: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("SMTP credential rate limit exceeded or credential revoked")
	}
	return nil
}
