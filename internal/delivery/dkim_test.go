package delivery

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
)

func TestGenerateAndUseDKIMKeyPair(t *testing.T) {
	const encryptionKey = "unit-test-dkim-encryption-key"
	publicKey, encryptedPrivateKey, err := GenerateDKIMKeyPair(encryptionKey)
	if err != nil {
		t.Fatalf("GenerateDKIMKeyPair() error = %v", err)
	}
	privateKey, err := DecryptDKIMPrivateKey(encryptionKey, encryptedPrivateKey)
	if err != nil {
		t.Fatalf("DecryptDKIMPrivateKey() error = %v", err)
	}
	config := &DKIMConfig{
		Domain:     "example.com",
		Selector:   "test",
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}
	rawMessage, err := marshalMessage(Message{
		ID:      "dkim-test@example.com",
		From:    "sender@example.com",
		To:      "recipient@example.net",
		Subject: "DKIM signing test",
		Text:    "The signed message body.",
		DKIM:    config,
	})
	if err != nil {
		t.Fatalf("marshalMessage() error = %v", err)
	}
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(rawMessage), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			if name != "test._domainkey.example.com" {
				return nil, fmt.Errorf("unexpected dkim lookup name %q", name)
			}
			return []string{"v=DKIM1; k=rsa; p=" + publicKey}, nil
		},
	})
	if err != nil {
		t.Fatalf("VerifyWithOptions() error = %v", err)
	}
	if len(verifications) != 1 || verifications[0].Err != nil {
		if len(verifications) == 0 {
			t.Fatal("signed message has no dkim signatures")
		}
		t.Fatalf("DKIM signature verification error = %v", verifications[0].Err)
	}
}

func TestSignMessageRejectsMismatchedPublicKey(t *testing.T) {
	_, encryptedPrivateKey, err := GenerateDKIMKeyPair("mismatch-test-key")
	if err != nil {
		t.Fatalf("GenerateDKIMKeyPair() error = %v", err)
	}
	privateKey, err := DecryptDKIMPrivateKey("mismatch-test-key", encryptedPrivateKey)
	if err != nil {
		t.Fatalf("DecryptDKIMPrivateKey() error = %v", err)
	}
	otherPublicKey, _, err := GenerateDKIMKeyPair("other-key")
	if err != nil {
		t.Fatalf("GenerateDKIMKeyPair() error = %v", err)
	}
	config := &DKIMConfig{
		Domain:     "example.com",
		Selector:   "test",
		PublicKey:  otherPublicKey,
		PrivateKey: privateKey,
	}
	if _, err := SignMessage([]byte("From: sender@example.com\r\n\r\nbody"), config); err == nil {
		t.Fatal("SignMessage() accepted a public key that does not match the private key")
	}
}

func TestGenerateDKIMKeyPairRequiresEncryptionKey(t *testing.T) {
	if _, _, err := GenerateDKIMKeyPair(""); err == nil {
		t.Fatal("GenerateDKIMKeyPair() succeeded without an encryption key")
	}
}

func TestEncryptSecretUsesDKIMEncryptionScheme(t *testing.T) {
	const key = "workspace-secret-encryption-key"
	const secret = "re_fake_not_a_real_key"
	envelope, err := EncryptSecret(key, secret)
	if err != nil {
		t.Fatal(err)
	}
	if string(envelope) == secret || len(envelope) <= len(secret) {
		t.Fatal("encrypted secret envelope is not ciphertext")
	}
	got, err := DecryptSecret(key, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Fatalf("DecryptSecret() = %q, want %q", got, secret)
	}
	if _, err := DecryptSecret("different-key", envelope); err == nil {
		t.Fatal("DecryptSecret() succeeded with a different key")
	}
}
