package api

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/khayode/email-platform/internal/config"
)

type fakeDNSResolver struct {
	records map[string][]string
}

func (f *fakeDNSResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	name = strings.ToLower(name)
	if values, ok := f.records[name]; ok {
		return values, nil
	}
	return nil, nil
}

func TestCheckDomainDNSVerified(t *testing.T) {
	server := &Server{
		cfg: config.Config{SPFInclude: "_spf.example.com"},
		dnsResolver: &fakeDNSResolver{records: map[string][]string{
			"example.com":                        {"v=spf1 include:_spf.example.com ~all"},
			"mail._domainkey.example.com":        {`"v=DKIM1; k=rsa; p=MIIBIjANbgkqhkiG9w0BAQE" "FAAOCAQ8AMIIBCgKCAQE"`},
			"_dmarc.example.com":                 {"v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"},
			"_sendwise-verification.example.com": {"v=sendwise-verification 029be151-9ec4-4fa8-83b8-f8c02b289a96"},
		}},
	}

	spf, dkim, dmarc, verification := server.checkDomainDNS(
		context.Background(),
		"example.com",
		"mail",
		"MIIBIjANbgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQE",
		"029be151-9ec4-4fa8-83b8-f8c02b289a96",
	)
	if spf != "verified" || dkim != "verified" || dmarc != "verified" || verification != "verified" {
		t.Fatalf("checkDomainDNS() = (%q, %q, %q, %q), want all verified", spf, dkim, dmarc, verification)
	}
}

func TestCheckDomainDNSReportsMismatchesAndMissing(t *testing.T) {
	server := &Server{dnsResolver: &fakeDNSResolver{records: map[string][]string{
		"example.com":                        {"v=spf1 a ~all"},
		"mail._domainkey.example.com":        {"v=DKIM1; k=rsa; p=wrong-key"},
		"_sendwise-verification.example.com": {"v=sendwise-verification example.com"},
	}}}

	spf, dkim, dmarc, verification := server.checkDomainDNS(context.Background(), "example.com", "mail", "expected-key", "expected-token")
	if spf != "mismatched" {
		t.Fatalf("SPF status = %q, want mismatched", spf)
	}
	if dkim != "mismatched" {
		t.Fatalf("DKIM status = %q, want mismatched", dkim)
	}
	if dmarc != "missing" {
		t.Fatalf("DMARC status = %q, want missing", dmarc)
	}
	if verification != "mismatched" {
		t.Fatalf("Verification status = %q, want mismatched", verification)
	}
}

func TestCheckDomainDNSRejectsUnconfiguredSPFInclude(t *testing.T) {
	server := &Server{dnsResolver: &fakeDNSResolver{records: map[string][]string{
		"example.com": {"v=spf1 include:other.example.com ~all"},
	}}}
	spf, _, _, _ := server.checkDomainDNS(context.Background(), "example.com", "mail", "expected-key", "expected-token")
	if spf != "mismatched" {
		t.Fatalf("SPF status = %q, want mismatched for an unconfigured include", spf)
	}
}

type failingDNSResolver struct{}

func (failingDNSResolver) LookupTXT(context.Context, string) ([]string, error) {
	return nil, errors.New("dns lookup failed")
}

func TestCheckDomainDNSReportsLookupFailed(t *testing.T) {
	server := &Server{dnsResolver: failingDNSResolver{}}

	spf, dkim, dmarc, verification := server.checkDomainDNS(context.Background(), "example.com", "mail", "expected-key", "expected-token")
	if spf != "lookup_failed" || dkim != "lookup_failed" || dmarc != "lookup_failed" || verification != "lookup_failed" {
		t.Fatalf("checkDomainDNS() = (%q, %q, %q, %q), want all lookup_failed", spf, dkim, dmarc, verification)
	}
}

type notFoundDNSResolver struct{}

func (notFoundDNSResolver) LookupTXT(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}

func TestCheckDomainDNSNotFoundIsMissing(t *testing.T) {
	server := &Server{dnsResolver: notFoundDNSResolver{}}
	spf, dkim, dmarc, verification := server.checkDomainDNS(context.Background(), "example.com", "mail", "expected-key", "expected-token")
	if spf != "missing" || dkim != "missing" || dmarc != "missing" || verification != "missing" {
		t.Fatalf("checkDomainDNS() = (%q, %q, %q, %q), want all missing", spf, dkim, dmarc, verification)
	}
}

func TestDNSRecordsForIncludesSendwiseVerificationRecord(t *testing.T) {
	d := sendingDomain{
		ID:                "d1",
		Domain:            "news.example.com",
		Status:            "pending",
		DKIMSelector:      "mail",
		DKIMPublicKey:     "MIIBIjANbgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQE",
		VerificationToken: "029be151-9ec4-4fa8-83b8-f8c02b289a96",
		SPFStatus:         "missing",
		DKIMStatus:        "missing",
		DMARCStatus:       "missing",
	}
	records := dnsRecordsFor(d, "pending", "", "")

	if len(records) != 4 {
		t.Fatalf("expected 4 dns records, got %d: %+v", len(records), records)
	}
	if records[3].Type != "TXT" || records[3].Name != "_sendwise-verification.news.example.com" {
		t.Fatalf("expected verification TXT record, got %+v", records[3])
	}
	if records[3].Status != "pending" {
		t.Fatalf("verification record status = %q, want pending", records[3].Status)
	}
	if records[3].Value != "v=sendwise-verification "+d.VerificationToken {
		t.Fatalf("verification record value = %q, want generated token", records[3].Value)
	}
	if !strings.Contains(records[1].Value, "v=DKIM1; k=rsa; p=MIIBIjANbgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQE") {
		t.Fatalf("expected generated DKIM key in DNS record, got %q", records[1].Value)
	}
	for _, record := range records {
		if strings.Contains(record.Value, "yourplatform.com") || strings.Contains(record.Value, "PUBLIC_KEY_FROM_STALWART") {
			t.Fatalf("unexpected placeholder DNS value: %+v", record)
		}
	}
}

func TestDNSRecordsForTrackingCNAMEAndConfiguredSPF(t *testing.T) {
	d := sendingDomain{
		Domain: "news.example.com", DKIMSelector: "mail",
		VerificationToken: "029be151-9ec4-4fa8-83b8-f8c02b289a96", SPFStatus: "pending",
	}
	records := dnsRecordsFor(d, "pending", "https://clicks.sendwise.test", "_spf.sendwise.test")
	if len(records) != 5 {
		t.Fatalf("expected four required records and configured tracking CNAME, got %d", len(records))
	}
	if records[0].Value != "v=spf1 include:_spf.sendwise.test ~all" {
		t.Fatalf("SPF value = %q", records[0].Value)
	}
	if records[4].Type != "CNAME" || records[4].Value != "clicks.sendwise.test" {
		t.Fatalf("unexpected tracking record: %+v", records[4])
	}
}

func TestNormalizeDkimTXTValuePreservesCaseAndJoinsChunks(t *testing.T) {
	const publicKey = "AbCdEf0123456789"
	txt := `"v=DKIM1; k=rsa; p=AbCdEf01" "23456789"`
	if got := normalizeDkimTXTValue(txt); got != publicKey {
		t.Fatalf("normalized key = %q, want %q", got, publicKey)
	}
	if got := normalizeDkimTXTValue(`v=DKIM1; p=abcdef0123456789`); got == publicKey {
		t.Fatal("DKIM key comparison must preserve case")
	}
}

func TestGenerateDKIMKeyPairUses2048BitRSAAndEncryptedPrivateKey(t *testing.T) {
	const encryptionKey = "test-encryption-key"
	publicKey, encryptedPrivateKey, err := generateDKIMKeyPair(encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	parsedPublic, err := x509.ParsePKCS1PublicKey(publicDER)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if parsedPublic.N.BitLen() != 2048 {
		t.Fatalf("RSA key size = %d, want 2048", parsedPublic.N.BitLen())
	}
	privateKeyDER, err := decryptDKIMPrivateKey(encryptionKey, encryptedPrivateKey)
	if err != nil {
		t.Fatalf("decrypt private key: %v", err)
	}
	parsedPrivate, err := x509.ParsePKCS8PrivateKey([]byte(privateKeyDER))
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	privateKey, ok := parsedPrivate.(*rsa.PrivateKey)
	if !ok {
		t.Fatalf("private key type = %T, want *rsa.PrivateKey", parsedPrivate)
	}
	if privateKey.N.Cmp(parsedPublic.N) != 0 {
		t.Fatal("public and private DKIM keys do not match")
	}
	if strings.Contains(encryptedPrivateKey, "PRIVATE KEY") {
		t.Fatal("encrypted private key contains plaintext PEM")
	}
}
