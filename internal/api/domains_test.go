package api

import (
	"context"
	"strings"
	"testing"
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
	server := &Server{dnsResolver: &fakeDNSResolver{records: map[string][]string{
		"example.com": {"v=spf1 include:_spf.example.com ~all"},
		"mail._domainkey.example.com": {"v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQE"},
		"_dmarc.example.com": {"v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"},
		"_sendwise-verification.example.com": {"v=sendwise-verification example.com"},
	}}}

	spf, dkim, dmarc, verification := server.checkDomainDNS(context.Background(), "example.com", "mail", "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQE")
	if spf != "verified" || dkim != "verified" || dmarc != "verified" || verification != "verified" {
		t.Fatalf("checkDomainDNS() = (%q, %q, %q, %q), want (verified, verified, verified, verified)", spf, dkim, dmarc, verification)
	}
}

func TestCheckDomainDNSReportsMismatchesAndMissing(t *testing.T) {
	server := &Server{dnsResolver: &fakeDNSResolver{records: map[string][]string{
		"example.com": {"v=mx"},
		"mail._domainkey.example.com": {"v=DKIM1; k=rsa; p=wrong-key"},
		"_sendwise-verification.example.com": {"v=sendwise-verification wrong.example.com"},
	}}}

	spf, dkim, dmarc, verification := server.checkDomainDNS(context.Background(), "example.com", "mail", "expected-key")
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
