package transactional

import (
	"bytes"
	"testing"
)

func TestValidateWebhookURLRequiresPublicHTTPSDestination(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{url: "https://hooks.example.test/events"},
		{url: "http://hooks.example.test/events", wantErr: true},
		{url: "https://user:pass@hooks.example.test/events", wantErr: true},
		{url: "https://hooks.example.test:8443/events", wantErr: true},
		{url: "https://127.0.0.1/events", wantErr: true},
		{url: "https://10.1.2.3/events", wantErr: true},
		{url: "https://[fd00::1]/events", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			err := ValidateWebhookURL(test.url)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateWebhookURL() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestWebhookSecretEncryptionAuthenticatesCiphertext(t *testing.T) {
	master := "test-key-material-at-least-sixteen"
	secret := "whsec_example-secret"
	ciphertext, err := EncryptWebhookSecret(master, secret)
	if err != nil {
		t.Fatalf("EncryptWebhookSecret() error = %v", err)
	}
	plaintext, err := DecryptWebhookSecret(master, ciphertext)
	if err != nil || plaintext != secret {
		t.Fatalf("DecryptWebhookSecret() = %q, %v; want original secret", plaintext, err)
	}
	ciphertext[len(ciphertext)-1] ^= 0xff
	if _, err := DecryptWebhookSecret(master, ciphertext); err == nil {
		t.Fatal("DecryptWebhookSecret() accepted tampered ciphertext")
	}
	if bytes.Contains(ciphertext, []byte(secret)) {
		t.Fatal("webhook secret was stored in plaintext")
	}
}
