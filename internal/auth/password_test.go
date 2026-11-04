package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordAndVerifyPassword(t *testing.T) {
	password := "correct horse battery staple"
	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	valid, err := VerifyPassword(encoded, password)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !valid {
		t.Fatal("VerifyPassword() rejected the correct password")
	}

	valid, err = VerifyPassword(encoded, "incorrect password")
	if err != nil {
		t.Fatalf("VerifyPassword() for wrong password error = %v", err)
	}
	if valid {
		t.Fatal("VerifyPassword() accepted an incorrect password")
	}
}

func TestVerifyPasswordRejectsUnsafeOrMalformedHashes(t *testing.T) {
	validHash, err := HashPassword("a password used only to build test fixtures")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	tests := []struct {
		name string
		hash string
	}{
		{name: "unsupported algorithm", hash: strings.Replace(validHash, "argon2id", "scrypt", 1)},
		{name: "unsupported version", hash: strings.Replace(validHash, "$v=19$", "$v=18$", 1)},
		{name: "excessive memory", hash: strings.Replace(validHash, "m=65536", "m=4294967295", 1)},
		{name: "unsupported iterations", hash: strings.Replace(validHash, "t=1", "t=2", 1)},
		{name: "malformed parameters", hash: strings.Replace(validHash, "m=65536", "m=invalid", 1)},
		{name: "short salt", hash: replaceHashPart(validHash, 4, "c2hvcnQ")},
		{name: "short key", hash: replaceHashPart(validHash, 5, "YWJj")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyPassword(test.hash, "password"); err == nil {
				t.Fatal("VerifyPassword() accepted a malformed hash")
			}
		})
	}
}

func replaceHashPart(encoded string, index int, replacement string) string {
	parts := strings.Split(encoded, "$")
	parts[index] = replacement
	return strings.Join(parts, "$")
}
