package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
)

type fakeDNSResolver struct {
	records map[string][]string
}

type sourceDNSResolver struct {
	answers map[string][]dnsAnswer
}

func (r sourceDNSResolver) LookupTXT(context.Context, string) ([]string, error) {
	return nil, nil
}

func (r sourceDNSResolver) LookupTXTBySource(_ context.Context, name, _ string) []dnsAnswer {
	return r.answers[strings.ToLower(name)]
}

func (r sourceDNSResolver) LookupMXBySource(_ context.Context, name, _ string) []dnsAnswer {
	return r.answers[strings.ToLower(name)]
}

type delayedDNSResolver struct {
	delay time.Duration
}

func (d delayedDNSResolver) LookupTXT(context.Context, string) ([]string, error) {
	time.Sleep(d.delay)
	return []string{"v=spf1 -all"}, nil
}

func (f *fakeDNSResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	name = strings.ToLower(name)
	if values, ok := f.records[name]; ok {
		return values, nil
	}
	return nil, nil
}

func TestDomainDNSParallelLatencyComparedWithSequentialLookups(t *testing.T) {
	const delay = 40 * time.Millisecond
	resolver := delayedDNSResolver{delay: delay}
	names := []string{
		"example.com",
		"mail._domainkey.example.com",
		"_dmarc.example.com",
		"_sendwise-verification.example.com",
	}

	before := time.Now()
	for _, name := range names {
		if _, err := resolver.LookupTXT(context.Background(), name); err != nil {
			t.Fatalf("sequential DNS lookup: %v", err)
		}
	}
	beforeDuration := time.Since(before)

	server := &Server{dnsResolver: resolver}
	after := time.Now()
	server.checkDomainDNSDetails(context.Background(), "example.com", "mail", "key", "token")
	afterDuration := time.Since(after)
	t.Logf("simulated DNS check latency: sequential_before=%s parallel_after=%s", beforeDuration.Round(time.Millisecond), afterDuration.Round(time.Millisecond))
	if afterDuration >= beforeDuration*3/4 {
		t.Fatalf("parallel DNS check took %s, expected under 75%% of sequential baseline %s", afterDuration, beforeDuration)
	}
}

func TestCheckDomainDNSVerified(t *testing.T) {
	server := &Server{
		cfg: config.Config{SPFInclude: "_spf.example.com"},
		dnsResolver: &fakeDNSResolver{records: map[string][]string{
			"example.com":                        {"v=spf1 include:_spf.example.com include:_spf.mail.hostinger.com ~all"},
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

func TestDomainDNSVerificationDoesNotRequireDMARCOrMX(t *testing.T) {
	server := &Server{
		cfg: config.Config{SPFInclude: "_spf.example.com"},
		dnsResolver: &fakeDNSResolver{records: map[string][]string{
			"example.com":                        {"v=spf1 include:_spf.example.com ~all"},
			"mail._domainkey.example.com":        {"v=DKIM1; k=rsa; p=expectedkey"},
			"_sendwise-verification.example.com": {"v=sendwise-verification expected-token"},
		}},
	}
	checks := server.checkDomainDNSDetails(context.Background(), "example.com", "mail", "expectedkey", "expected-token")
	spf, dkim, dmarc, verification := checks.Statuses()
	if got := domainDNSVerificationStatus(spf, dkim, verification); got != "verified" {
		t.Fatalf("domain status = %q with DMARC=%q; want verified when ownership, SPF, and DKIM pass", got, dmarc)
	}
	if checks.Records["mx"].Status != "missing" {
		t.Fatalf("MX status = %q, want missing warning data", checks.Records["mx"].Status)
	}
}

type concurrentDNSResolver struct {
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
}

func (r *concurrentDNSResolver) LookupTXT(context.Context, string) ([]string, error) {
	r.started <- struct{}{}
	<-r.release
	return nil, nil
}

func TestCheckDomainDNSRunsRecordLookupsInParallel(t *testing.T) {
	resolver := &concurrentDNSResolver{
		started: make(chan struct{}, 4),
		release: make(chan struct{}),
	}
	server := &Server{dnsResolver: resolver}
	done := make(chan struct{})
	go func() {
		server.checkDomainDNSDetails(context.Background(), "example.com", "mail", "key", "token")
		close(done)
	}()

	for range 4 {
		select {
		case <-resolver.started:
		case <-time.After(time.Second):
			close(resolver.release)
			t.Fatal("DNS record queries did not start concurrently")
		}
	}
	close(resolver.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DNS checks did not finish after releasing concurrent lookups")
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

func TestCheckDomainDNSRequiresConfiguredSPFIncludeInsteadOfMX(t *testing.T) {
	server := &Server{
		cfg: config.Config{SPFInclude: "_spf.sendwise.example"},
		dnsResolver: &fakeDNSResolver{records: map[string][]string{
			"example.com": {"v=spf1 mx ~all"},
		}},
	}
	spf, _, _, _ := server.checkDomainDNS(context.Background(), "example.com", "mail", "expected-key", "expected-token")
	if spf != "mismatched" {
		t.Fatalf("SPF status = %q, want mismatched when configured SPF include is absent", spf)
	}
}

func TestCheckDomainDNSIgnoresFailedResolverWhenOthersFindRecords(t *testing.T) {
	const domain = "example.com"
	records := map[string]string{
		domain:                             "v=spf1 include:_spf.example.com include:_spf.mail.hostinger.com ~all",
		"mail._domainkey." + domain:        "v=DKIM1; k=rsa; p=expectedkey",
		"_dmarc." + domain:                 "v=DMARC1; p=none",
		"_sendwise-verification." + domain: "v=sendwise-verification expected-token",
	}
	answers := make(map[string][]dnsAnswer, len(records))
	for name, value := range records {
		answers[name] = []dnsAnswer{
			{resolver: "1.1.1.1", values: []string{value}},
			{resolver: "8.8.8.8", values: []string{value}},
			{resolver: "authoritative IPv4", values: []string{value}},
			{resolver: "authoritative IPv6", err: errors.New("network is unreachable")},
		}
	}
	server := &Server{
		cfg:         config.Config{SPFInclude: "_spf.example.com"},
		dnsResolver: sourceDNSResolver{answers: answers},
	}

	checks := server.checkDomainDNSDetails(context.Background(), domain, "mail", "expectedkey", "expected-token")
	spf, dkim, dmarc, verification := checks.Statuses()
	if spf != "verified" || dkim != "verified" || dmarc != "verified" || verification != "verified" {
		t.Fatalf("DNS statuses = (%q, %q, %q, %q), want all verified", spf, dkim, dmarc, verification)
	}
	for name, record := range checks.Records {
		if name == "mx" {
			continue
		}
		if record.Status != "found" || len(record.Resolvers) != 4 {
			t.Fatalf("%s result = %+v, want found with four resolver details", name, record)
		}
		if record.Resolvers[3].Status != "lookup_failed" {
			t.Fatalf("%s final resolver = %+v, want lookup_failed detail retained", name, record.Resolvers[3])
		}
	}
}

func TestCheckDomainDNSReportsLookupFailedWhenEveryResolverFails(t *testing.T) {
	failure := errors.New("network is unreachable")
	answers := map[string][]dnsAnswer{
		"example.com": {
			{resolver: "1.1.1.1", err: failure},
			{resolver: "8.8.8.8", err: failure},
			{resolver: "authoritative IPv4", err: failure},
			{resolver: "authoritative IPv6", err: failure},
		},
		"mail._domainkey.example.com": {
			{resolver: "1.1.1.1", err: failure},
			{resolver: "8.8.8.8", err: failure},
			{resolver: "authoritative IPv4", err: failure},
			{resolver: "authoritative IPv6", err: failure},
		},
		"_dmarc.example.com": {
			{resolver: "1.1.1.1", err: failure},
			{resolver: "8.8.8.8", err: failure},
			{resolver: "authoritative IPv4", err: failure},
			{resolver: "authoritative IPv6", err: failure},
		},
		"_sendwise-verification.example.com": {
			{resolver: "1.1.1.1", err: failure},
			{resolver: "8.8.8.8", err: failure},
			{resolver: "authoritative IPv4", err: failure},
			{resolver: "authoritative IPv6", err: failure},
		},
	}
	server := &Server{dnsResolver: sourceDNSResolver{answers: answers}}

	checks := server.checkDomainDNSDetails(context.Background(), "example.com", "mail", "expectedkey", "expected-token")
	spf, dkim, dmarc, verification := checks.Statuses()
	if spf != "lookup_failed" || dkim != "lookup_failed" ||
		dmarc != "lookup_failed" || verification != "lookup_failed" {
		t.Fatalf("DNS statuses = (%q, %q, %q, %q), want all lookup_failed", spf, dkim, dmarc, verification)
	}
}

func TestCheckDomainDNSRejectsMultipleSPFRecords(t *testing.T) {
	server := &Server{
		cfg: config.Config{SPFInclude: "_spf.example.com"},
		dnsResolver: &fakeDNSResolver{records: map[string][]string{
			"example.com": {
				"v=spf1 include:_spf.example.com ~all",
				"v=spf1 include:_spf.mail.hostinger.com ~all",
			},
		}},
	}

	checks := server.checkDomainDNSDetails(context.Background(), "example.com", "mail", "expectedkey", "expected-token")
	if got := dnsStatus(checks.Records["spf"].Status); got != "mismatched" {
		t.Fatalf("SPF status = %q, want mismatched for multiple SPF records", got)
	}
	if checks.Records["spf"].FoundValue != "Multiple SPF records; merge them into one." {
		t.Fatalf("SPF explanation = %q", checks.Records["spf"].FoundValue)
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
	publicKey, _, err := generateDKIMKeyPair("dns-record-test-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	d := sendingDomain{
		ID:                "d1",
		Domain:            "news.example.com",
		Status:            "pending",
		DKIMSelector:      "mail",
		DKIMPublicKey:     publicKey,
		VerificationToken: "029be151-9ec4-4fa8-83b8-f8c02b289a96",
		SPFStatus:         "missing",
		DKIMStatus:        "missing",
		DMARCStatus:       "missing",
	}
	records := dnsRecordsFor(d, "pending", "", "")

	if len(records) != 5 {
		t.Fatalf("expected 5 dns records, got %d: %+v", len(records), records)
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
	publishedKey := strings.TrimPrefix(records[1].Value, "v=DKIM1; k=rsa; p=")
	publishedDER, err := base64.StdEncoding.DecodeString(publishedKey)
	if err != nil {
		t.Fatalf("decode generated DKIM DNS key: %v", err)
	}
	if _, err := x509.ParsePKIXPublicKey(publishedDER); err != nil {
		t.Fatalf("generated DKIM DNS key is not SPKI: %v", err)
	}
	for _, record := range records {
		if strings.Contains(record.Value, "yourplatform.com") || strings.Contains(record.Value, "PUBLIC_KEY_FROM_STALWART") {
			t.Fatalf("unexpected placeholder DNS value: %+v", record)
		}
	}
}

func TestDNSRecordsForEmitsSPKIAndPreservesPublishedLegacyKey(t *testing.T) {
	const encryptionKey = "domain-record-format-test"
	pkcs1Key, encryptedPrivateKey, err := delivery.GenerateDKIMKeyPair(encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := delivery.DecryptDKIMPrivateKey(encryptionKey, encryptedPrivateKey)
	if err != nil {
		t.Fatal(err)
	}

	domain := sendingDomain{
		Domain: "news.example.com", DKIMSelector: "mail", DKIMPublicKey: pkcs1Key,
		VerificationToken: "verify-token", DKIMStatus: "pending", Status: "pending",
	}
	pendingRecords := dnsRecordsFor(domain, "pending", "", "")
	pendingDKIM := pendingRecords[1]
	publishedSPKI := strings.TrimPrefix(pendingDKIM.Value, "v=DKIM1; k=rsa; p=")
	der, err := base64.StdEncoding.DecodeString(publishedSPKI)
	if err != nil {
		t.Fatalf("decode emitted DKIM key: %v", err)
	}
	if _, err := x509.ParsePKIXPublicKey(der); err != nil {
		t.Fatalf("emitted DNS key is not SubjectPublicKeyInfo: %v", err)
	}

	legacyTXT := "v=DKIM1; k=rsa; p=" + pkcs1Key
	domain.Status = "verified"
	domain.DKIMStatus = "verified"
	domain.DNSCheckDetails, err = json.Marshal(dnsCheckDetails{
		Records: map[string]dnsRecordResult{
			"dkim": {Status: "found", FoundValue: legacyTXT},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedRecords := dnsRecordsFor(domain, "verified", "", "")
	verifiedDKIM := verifiedRecords[1]
	if verifiedDKIM.Value != legacyTXT {
		t.Fatalf("verified domain DKIM value = %q, want currently published %q", verifiedDKIM.Value, legacyTXT)
	}
	if !verifiedDKIM.UpdateRecommended {
		t.Fatal("verified legacy PKCS#1 record should recommend an SPKI update")
	}

	config := &delivery.DKIMConfig{
		Domain: "news.example.com", Selector: "mail", PublicKey: pkcs1Key, PrivateKey: privateKey,
	}
	signed, err := delivery.SignMessage([]byte(
		"From: sender@news.example.com\r\nTo: receiver@example.net\r\nSubject: DKIM SPKI test\r\n\r\nSigned body.\r\n",
	), config)
	if err != nil {
		t.Fatalf("sign test message: %v", err)
	}
	verified, err := dkim.VerifyWithOptions(bytes.NewReader(signed), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			if name != "mail._domainkey.news.example.com" {
				return nil, fmt.Errorf("unexpected DKIM lookup name %q", name)
			}
			return []string{pendingDKIM.Value}, nil
		},
	})
	if err != nil {
		t.Fatalf("verify SPKI-signed test message: %v", err)
	}
	if len(verified) != 1 || verified[0].Err != nil {
		if len(verified) == 0 {
			t.Fatal("signed message has no DKIM signature")
		}
		t.Fatalf("standard DKIM verifier rejected generated SPKI record: %v", verified[0].Err)
	}
}

func TestDNSRecordsForTrackingCNAMEAndConfiguredSPF(t *testing.T) {
	d := sendingDomain{
		Domain: "news.example.com", DKIMSelector: "mail",
		VerificationToken: "029be151-9ec4-4fa8-83b8-f8c02b289a96", SPFStatus: "pending",
	}
	records := dnsRecordsFor(d, "pending", "https://clicks.sendwise.test", "_spf.sendwise.test")
	if len(records) != 6 {
		t.Fatalf("expected five DNS records and configured tracking CNAME, got %d", len(records))
	}
	if records[0].Value != "v=spf1 include:_spf.sendwise.test ~all" {
		t.Fatalf("SPF value = %q", records[0].Value)
	}
	if records[5].Type != "CNAME" || records[5].Value != "clicks.sendwise.test" {
		t.Fatalf("unexpected tracking record: %+v", records[5])
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

func TestNormalizeDkimKeyAcceptsPKIXAndPKCS1AndRejectsMismatch(t *testing.T) {
	first, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	pkcs1 := base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(&first.PublicKey))
	pkixDER, err := x509.MarshalPKIXPublicKey(&first.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pkix := base64.StdEncoding.EncodeToString(pkixDER)
	otherPKIXDER, err := x509.MarshalPKIXPublicKey(&second.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	otherPKIX := base64.StdEncoding.EncodeToString(otherPKIXDER)

	if normalizeDkimKey(pkix) != normalizeDkimKey(pkcs1) {
		t.Fatal("SPKI and PKCS#1 encodings of the same key did not normalize equally")
	}
	if normalizeDkimTXTValue("v=DKIM1; k=rsa; p="+pkix) != normalizeDkimKey(pkcs1) {
		t.Fatal("SPKI DKIM TXT record did not match a stored PKCS#1 key")
	}
	if normalizeDkimTXTValue("v=DKIM1; k=rsa; p="+otherPKIX) == normalizeDkimKey(pkcs1) {
		t.Fatal("a different public key normalized as the stored key")
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
