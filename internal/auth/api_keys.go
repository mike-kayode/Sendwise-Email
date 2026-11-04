package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

const (
	apiKeyPrefix     = "sk_"
	apiKeyEntropy    = 32
	apiKeyHashMemory = 64 * 1024
	apiKeyHashIter   = 1
	apiKeyHashPar    = 4
	apiKeyHashKeyLen = 32
	apiKeyHashSalt   = 16
)

// ErrNotFound is returned when a resource is not found.
var ErrNotFound = errors.New("not found")

// APIKey represents a scoped API credential for an organization.
type APIKey struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	Name           string     `json:"name"`
	KeyPrefix      string     `json:"key_prefix"`
	Scopes         []string   `json:"scopes"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	// PlainKey is only populated on creation; never stored.
	PlainKey string `json:"plain_key,omitempty"`
}

// Service manages API keys.
type Service struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewAPIKeyService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, logger: logger}
}

// Create generates a new API key, stores its hash, and returns the plain key once.
func (s *Service) Create(ctx context.Context, orgID, name string, scopes []string, expiresAt *time.Time) (*APIKey, string, error) {
	raw := apiKeyPrefix + randomHex(apiKeyEntropy)
	prefix := raw[:8] + "…"
	hash, err := hashAPIKey(raw)
	if err != nil {
		return nil, "", fmt.Errorf("hash api key: %w", err)
	}

	var id string
	var createdAt time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (organization_id, created_by, name, key_prefix, key_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, orgID, nil, name, prefix, hash, scopes, expiresAt).Scan(&id, &createdAt)
	if err != nil {
		var nilKey *APIKey
		return nilKey, "", fmt.Errorf("insert api key: %w", err)
	}

	return &APIKey{
		ID:             id,
		OrganizationID: orgID,
		Name:           name,
		KeyPrefix:      prefix,
		Scopes:         scopes,
		LastUsedAt:     nil,
		ExpiresAt:      expiresAt,
		RevokedAt:      nil,
		CreatedAt:      createdAt,
		PlainKey:       raw,
	}, "", nil
}

// List returns all API keys for an organization (without plain keys).
func (s *Service) List(ctx context.Context, orgID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, key_prefix, scopes, last_used_at, expires_at, revoked_at, created_at
		FROM api_keys
		WHERE organization_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var lastUsed, expires, revoked *time.Time
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.Scopes, &lastUsed, &expires, &revoked, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.OrganizationID = orgID
		k.LastUsedAt = lastUsed
		k.ExpiresAt = expires
		k.RevokedAt = revoked
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Delete revokes an API key.
func (s *Service) Delete(ctx context.Context, orgID, id string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE api_keys
		SET revoked_at = now(), updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND revoked_at IS NULL
	`, id, orgID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate validates an API key against stored hashes. Returns the organization ID and scopes.
func (s *Service) Authenticate(ctx context.Context, key string) (string, []string, error) {
	// Extract prefix to narrow the lookup
	if !strings.HasPrefix(key, apiKeyPrefix) {
		return "", nil, fmt.Errorf("invalid api key format")
	}
	prefix := key[:8] + "…"

	var orgID string
	var keyHash string
	var scopes []string
	var expiresAt *time.Time
	var revokedAt *time.Time

	err := s.pool.QueryRow(ctx, `
		SELECT organization_id, key_hash, scopes, expires_at, revoked_at
		FROM api_keys
		WHERE key_prefix = $1 AND revoked_at IS NULL
	`, prefix).Scan(&orgID, &keyHash, &scopes, &expiresAt, &revokedAt)
	if err != nil {
		return "", nil, fmt.Errorf("api key not found")
	}

	if revokedAt != nil {
		return "", nil, fmt.Errorf("api key revoked")
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return "", nil, fmt.Errorf("api key expired")
	}

	if !verifyAPIKey(keyHash, key) {
		return "", nil, fmt.Errorf("invalid api key")
	}

	// Update last_used_at
	_, _ = s.pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at = now(), updated_at = now()
		WHERE key_prefix = $1 AND organization_id = $2
	`, prefix, orgID)

	return orgID, scopes, nil
}

// randomHex generates n bytes of hex-encoded random data.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generate random: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n*2]
}

func hashAPIKey(key string) (string, error) {
	salt := make([]byte, apiKeyHashSalt)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(key), salt, apiKeyHashIter, apiKeyHashMemory, apiKeyHashPar, apiKeyHashKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		apiKeyHashMemory, apiKeyHashIter, apiKeyHashPar,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyAPIKey(encoded, key string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", memory, iterations, parallelism) ||
		memory != apiKeyHashMemory || iterations != apiKeyHashIter || parallelism != apiKeyHashPar {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != apiKeyHashSalt {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != apiKeyHashKeyLen {
		return false
	}
	actual := argon2.IDKey([]byte(key), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1
}
