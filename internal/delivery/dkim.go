package delivery

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DKIMConfig struct {
	Domain     string
	Selector   string
	PublicKey  string
	PrivateKey string
}

func GenerateDKIMKeyPair(encryptionKey string) (publicKey, encryptedPrivateKey string, err error) {
	if encryptionKey == "" {
		return "", "", errors.New("DKIM encryption key is not configured")
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate dkim keypair: %w", err)
	}
	publicKey = base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(&privateKey.PublicKey))

	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal dkim private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	encrypted, err := EncryptSecret(encryptionKey, string(privatePEM))
	if err != nil {
		return "", "", fmt.Errorf("encrypt dkim private key: %w", err)
	}
	return publicKey, base64.StdEncoding.EncodeToString(encrypted), nil
}

func DecryptDKIMPrivateKey(encryptionKey, encryptedPrivateKey string) (string, error) {
	if encryptionKey == "" {
		return "", errors.New("dkim encryption key is not configured")
	}
	if encryptedPrivateKey == "" {
		return "", errors.New("dkim private key is not configured")
	}
	data, err := base64.StdEncoding.DecodeString(encryptedPrivateKey)
	if err != nil {
		return "", fmt.Errorf("decode dkim private key: %w", err)
	}
	plaintext, err := DecryptSecret(encryptionKey, data)
	if err != nil {
		return "", fmt.Errorf("decrypt dkim private key: %w", err)
	}
	blockPEM, _ := pem.Decode([]byte(plaintext))
	if blockPEM == nil || blockPEM.Type != "PRIVATE KEY" {
		return "", errors.New("could not decode dkim private key pem block")
	}
	if _, err := x509.ParsePKCS8PrivateKey(blockPEM.Bytes); err != nil {
		return "", fmt.Errorf("parse dkim private key: %w", err)
	}
	return string(blockPEM.Bytes), nil
}

func EncryptSecret(encryptionKey, plaintext string) ([]byte, error) {
	if encryptionKey == "" {
		return nil, errors.New("encryption key is not configured")
	}
	derivedKey := sha256.Sum256([]byte(encryptionKey))
	block, err := aes.NewCipher(derivedKey[:])
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate secret nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func DecryptSecret(encryptionKey string, envelope []byte) (string, error) {
	if encryptionKey == "" {
		return "", errors.New("encryption key is not configured")
	}
	derivedKey := sha256.Sum256([]byte(encryptionKey))
	block, err := aes.NewCipher(derivedKey[:])
	if err != nil {
		return "", fmt.Errorf("initialize secret decryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("initialize secret decryption: %w", err)
	}
	if len(envelope) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted secret envelope")
	}
	nonce, ciphertext := envelope[:gcm.NonceSize()], envelope[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plaintext), nil
}

func LoadDKIMConfig(ctx context.Context, pool *pgxpool.Pool, organizationID, fromAddress, encryptionKey string) (*DKIMConfig, error) {
	address, err := mail.ParseAddress(fromAddress)
	if err != nil {
		return nil, fmt.Errorf("parse dkim sender address: %w", err)
	}
	at := strings.LastIndexByte(address.Address, '@')
	if at < 0 || at == len(address.Address)-1 {
		return nil, errors.New("dkim sender address has no domain")
	}
	senderDomain := strings.ToLower(strings.TrimSuffix(address.Address[at+1:], "."))

	var config DKIMConfig
	var encryptedPrivateKey string
	err = pool.QueryRow(ctx, `
		SELECT lower(domain), dkim_selector, COALESCE(dkim_public_key, ''),
		       COALESCE(dkim_private_key, '')
		FROM sending_domains
		WHERE organization_id = $1 AND lower(domain) = $2
	`, organizationID, senderDomain).Scan(
		&config.Domain, &config.Selector, &config.PublicKey, &encryptedPrivateKey,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load sending domain dkim key: %w", err)
	}
	if config.Domain == "" || config.Selector == "" || config.PublicKey == "" {
		return nil, fmt.Errorf("sending domain %q has incomplete dkim configuration", senderDomain)
	}
	config.PrivateKey, err = DecryptDKIMPrivateKey(encryptionKey, encryptedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("load sending domain dkim private key: %w", err)
	}
	if _, err := signerForDKIMConfig(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

func SignMessage(message []byte, config *DKIMConfig) ([]byte, error) {
	signer, err := signerForDKIMConfig(config)
	if err != nil {
		return nil, err
	}
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(message), &dkim.SignOptions{
		Domain:                 config.Domain,
		Selector:               config.Selector,
		Signer:                 signer,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
	}); err != nil {
		return nil, fmt.Errorf("sign dkim message: %w", err)
	}
	return signed.Bytes(), nil
}

func signerForDKIMConfig(config *DKIMConfig) (crypto.Signer, error) {
	if config == nil || config.Domain == "" || config.Selector == "" || config.PublicKey == "" || config.PrivateKey == "" {
		return nil, errors.New("incomplete dkim signing configuration")
	}
	key, err := x509.ParsePKCS8PrivateKey([]byte(config.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("parse dkim signing key: %w", err)
	}
	privateKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unsupported dkim signing key type %T", key)
	}
	publicKey := base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(&privateKey.PublicKey))
	if publicKey != config.PublicKey {
		return nil, errors.New("dkim private key does not match the published public key")
	}
	return privateKey, nil
}
