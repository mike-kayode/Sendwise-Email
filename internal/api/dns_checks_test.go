package api

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

func TestDNSResolverServersHonorConfiguredAddress(t *testing.T) {
	defaults := dnsResolverServers("")
	if len(defaults) != 2 || defaults[0] != "1.1.1.1:53" || defaults[1] != "8.8.8.8:53" {
		t.Fatalf("dnsResolverServers(\"\") = %v, want the public defaults", defaults)
	}
	cases := map[string]string{
		"10.0.0.53":      "10.0.0.53:53",
		"10.0.0.53:5353": "10.0.0.53:5353",
		"dns.internal":   "dns.internal:53",
		"2001:db8::1":    "[2001:db8::1]:53",
	}
	for input, want := range cases {
		got := dnsResolverServers(input)
		if len(got) != 1 || got[0] != want {
			t.Errorf("dnsResolverServers(%q) = %v, want [%q]", input, got, want)
		}
	}
}

func TestNewDNSResolverCarriesConfiguredServerIntoVerification(t *testing.T) {
	configured, ok := newDNSResolver("10.0.0.53", time.Second).(*netDNSResolver)
	if !ok {
		t.Fatal("newDNSResolver did not return a netDNSResolver")
	}
	if len(configured.servers) != 1 || configured.servers[0] != "10.0.0.53:53" {
		t.Fatalf("configured resolver servers = %v, want [10.0.0.53:53]", configured.servers)
	}
	byDefault, ok := newDNSResolver("", time.Second).(*netDNSResolver)
	if !ok {
		t.Fatal("newDNSResolver did not return a netDNSResolver")
	}
	if len(byDefault.servers) != len(defaultDNSResolvers) {
		t.Fatalf("default resolver servers = %v, want %v", byDefault.servers, defaultDNSResolvers)
	}
}

type sourceRecordingResolver struct {
	mu      sync.Mutex
	queries []string
	answers []dnsAnswer
}

func (r *sourceRecordingResolver) LookupTXT(context.Context, string) ([]string, error) {
	return nil, nil
}

func (r *sourceRecordingResolver) LookupTXTBySource(_ context.Context, name, zone string) []dnsAnswer {
	r.record(name, zone)
	return r.answers
}

func (r *sourceRecordingResolver) LookupMXBySource(_ context.Context, name, zone string) []dnsAnswer {
	r.record(name, zone)
	return r.answers
}

func (r *sourceRecordingResolver) record(name, zone string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, name+"|"+zone)
}

func TestLookupDNSRecordUsesParentDomainForAuthoritativeQuery(t *testing.T) {
	resolver := &sourceRecordingResolver{
		answers: []dnsAnswer{{resolver: "1.1.1.1", values: []string{"expected"}}},
	}
	spec := dnsRecordSpec{
		Key:  "dkim",
		Type: "TXT",
		Name: "mail._domainkey.news.example.com",
		Zone: "news.example.com",
	}
	result := lookupDNSRecord(context.Background(), resolver, spec, func(value string) bool {
		return value == "expected"
	})
	if result.Status != "found" {
		t.Fatalf("status = %q, want found", result.Status)
	}
	if len(resolver.queries) != 1 || resolver.queries[0] != "mail._domainkey.news.example.com|news.example.com" {
		t.Fatalf("authoritative lookup used unexpected query/zone: %v", resolver.queries)
	}
}

func TestAuthoritativeZoneCandidatesRespectMultiLabelPublicSuffixes(t *testing.T) {
	got := authoritativeZoneCandidates("send.mail.example.co.uk.")
	want := []string{"send.mail.example.co.uk", "mail.example.co.uk", "example.co.uk"}
	if len(got) != len(want) {
		t.Fatalf("authoritativeZoneCandidates() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("authoritativeZoneCandidates() = %v, want %v", got, want)
		}
	}
}

func TestLookupDNSRecordDistinguishesWrongValueFromMissing(t *testing.T) {
	tests := []struct {
		name   string
		answer dnsAnswer
		want   string
	}{
		{name: "wrong value", answer: dnsAnswer{resolver: "8.8.8.8", values: []string{"old-key"}}, want: "wrong_value"},
		{name: "missing", answer: dnsAnswer{resolver: "8.8.8.8", err: &net.DNSError{Err: "no such host", IsNotFound: true}}, want: "missing"},
		{name: "lookup failure", answer: dnsAnswer{resolver: "8.8.8.8", err: &net.DNSError{Err: "timeout", IsTimeout: true}}, want: "lookup_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &sourceRecordingResolver{answers: []dnsAnswer{test.answer}}
			result := lookupDNSRecord(context.Background(), resolver, dnsRecordSpec{
				Key: "dkim", Type: "TXT", Name: "mail._domainkey.example.com", Zone: "example.com",
			}, func(value string) bool { return value == "new-key" })
			if result.Status != test.want {
				t.Fatalf("status = %q, want %q", result.Status, test.want)
			}
			if len(result.Resolvers) != 1 {
				t.Fatalf("resolver details = %+v, want one resolver result", result.Resolvers)
			}
		})
	}
}
