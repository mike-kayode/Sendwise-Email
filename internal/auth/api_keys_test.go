package auth

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestRandomHexReturnsRequestedEntropy(t *testing.T) {
	value, err := randomHex(apiKeyEntropy)
	if err != nil {
		t.Fatalf("randomHex() error = %v", err)
	}
	if len(value) != apiKeyEntropy*2 {
		t.Fatalf("randomHex() length = %d, want %d", len(value), apiKeyEntropy*2)
	}
	if _, err := hex.DecodeString(value); err != nil {
		t.Fatalf("randomHex() returned non-hex data: %v", err)
	}
}

func TestAPIKeyHashAndVerify(t *testing.T) {
	key := "sk_0123456789abcdef0123456789abcdef"
	hash, err := hashAPIKey(key)
	if err != nil {
		t.Fatalf("hashAPIKey() error = %v", err)
	}
	if !verifyAPIKey(hash, key) {
		t.Fatal("verifyAPIKey() rejected the correct key")
	}
	if verifyAPIKey(hash, key+"x") {
		t.Fatal("verifyAPIKey() accepted an incorrect key")
	}
}

func TestVerifyAPIKeyRejectsUnsafeHashes(t *testing.T) {
	hash, err := hashAPIKey("sk_test-api-key-value")
	if err != nil {
		t.Fatalf("hashAPIKey() error = %v", err)
	}
	malformed := []string{
		strings.Replace(hash, "argon2id", "scrypt", 1),
		strings.Replace(hash, "$v=19$", "$v=18$", 1),
		strings.Replace(hash, "m=65536", "m=4294967295", 1),
	}
	for _, encoded := range malformed {
		if verifyAPIKey(encoded, "sk_test-api-key-value") {
			t.Fatal("verifyAPIKey() accepted an unsupported hash")
		}
	}
}
