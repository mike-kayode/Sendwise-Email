package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

type dnsCheckDetails struct {
	CheckedAt *time.Time                 `json:"checked_at"`
	Records   map[string]dnsRecordResult `json:"records"`
}

type dnsRecordResult struct {
	Status     string             `json:"status"`
	FoundValue string             `json:"found_value,omitempty"`
	Resolvers  []dnsResolverCheck `json:"resolvers"`
}

type dnsResolverCheck struct {
	Resolver   string `json:"resolver"`
	Status     string `json:"status"`
	FoundValue string `json:"found_value,omitempty"`
	Error      string `json:"error,omitempty"`
}

type dnsRecordSpec struct {
	Key  string
	Type string
	Name string
	Zone string
}

type dnsAnswer struct {
	resolver string
	values   []string
	err      error
}

type dnsObservingResolver interface {
	LookupTXTBySource(context.Context, string, string) []dnsAnswer
	LookupMXBySource(context.Context, string, string) []dnsAnswer
}

func lookupDNSRecord(ctx context.Context, resolver DNSResolver, spec dnsRecordSpec, matches func(string) bool) dnsRecordResult {
	var answers []dnsAnswer
	if observing, ok := resolver.(dnsObservingResolver); ok {
		zone := spec.Zone
		if zone == "" {
			zone = spec.Name
		}
		if spec.Type == "MX" {
			answers = observing.LookupMXBySource(ctx, spec.Name, zone)
		} else {
			answers = observing.LookupTXTBySource(ctx, spec.Name, zone)
		}
	} else if spec.Type == "MX" {
		if mxResolver, ok := resolver.(interface {
			LookupMX(context.Context, string) ([]*net.MX, error)
		}); ok {
			values, err := mxResolver.LookupMX(ctx, spec.Name)
			answers = []dnsAnswer{{resolver: "configured resolver", values: mxValues(values), err: err}}
		} else {
			answers = []dnsAnswer{{resolver: "configured resolver"}}
		}
	} else {
		values, err := resolver.LookupTXT(ctx, spec.Name)
		answers = []dnsAnswer{{resolver: "configured resolver", values: values, err: err}}
	}

	result := dnsRecordResult{Status: "lookup_failed", Resolvers: make([]dnsResolverCheck, 0, len(answers))}
	allFound := len(answers) > 0
	hasWrongValue := false
	hasMissing := false
	hasLookupFailure := false
	for _, answer := range answers {
		check := dnsResolverCheck{Resolver: answer.resolver}
		switch {
		case answer.err != nil:
			if isDNSLookupFailure(answer.err) {
				check.Status = "lookup_failed"
				check.Error = answer.err.Error()
				hasLookupFailure = true
			} else {
				check.Status = "missing"
				hasMissing = true
			}
			allFound = false
		case len(answer.values) == 0:
			check.Status = "missing"
			hasMissing = true
			allFound = false
		default:
			check.Status = "wrong_value"
			wrongValue := true
			check.FoundValue = strings.Join(answer.values, " ")
			for _, value := range answer.values {
				if matches(value) {
					check.Status = "found"
					check.FoundValue = value
					wrongValue = false
					break
				}
			}
			hasWrongValue = hasWrongValue || wrongValue
			if check.Status != "found" {
				allFound = false
			}
		}
		result.Resolvers = append(result.Resolvers, check)
	}
	switch {
	case allFound:
		result.Status = "found"
		for _, check := range result.Resolvers {
			if result.FoundValue == "" && check.Status == "found" {
				result.FoundValue = check.FoundValue
			}
		}
	case hasWrongValue:
		result.Status = "wrong_value"
		for _, check := range result.Resolvers {
			if check.Status == "wrong_value" {
				result.FoundValue = check.FoundValue
				break
			}
		}
	case hasMissing:
		result.Status = "missing"
	case hasLookupFailure:
		result.Status = "lookup_failed"
	}
	return result
}

func mxValues(records []*net.MX) []string {
	values := make([]string, 0, len(records))
	for _, record := range records {
		if record != nil {
			values = append(values, fmt.Sprintf("%d %s", record.Pref, strings.TrimSuffix(record.Host, ".")))
		}
	}
	return values
}

func (d dnsCheckDetails) Statuses() (spf, dkim, dmarc, verification string) {
	return dnsStatus(d.Records["spf"].Status), dnsStatus(d.Records["dkim"].Status),
		dnsStatus(d.Records["dmarc"].Status), dnsStatus(d.Records["verification"].Status)
}

func dnsStatus(status string) string {
	switch status {
	case "found":
		return "verified"
	case "wrong_value":
		return "mismatched"
	case "missing", "lookup_failed":
		return status
	default:
		return "missing"
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func (r *netDNSResolver) LookupTXTBySource(ctx context.Context, name, zone string) []dnsAnswer {
	return r.lookupBySource(ctx, name, zone, func(resolver *net.Resolver, queryContext context.Context, name string) ([]string, error) {
		return resolver.LookupTXT(queryContext, name)
	})
}

func (r *netDNSResolver) LookupMXBySource(ctx context.Context, name, zone string) []dnsAnswer {
	return r.lookupBySource(ctx, name, zone, func(resolver *net.Resolver, queryContext context.Context, name string) ([]string, error) {
		records, err := resolver.LookupMX(queryContext, name)
		return mxValues(records), err
	})
}

func (r *netDNSResolver) lookupBySource(
	ctx context.Context,
	name, zone string,
	lookup func(*net.Resolver, context.Context, string) ([]string, error),
) []dnsAnswer {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	publicResolvers := []string{"1.1.1.1:53", "8.8.8.8:53"}
	authorityLookup, ok := ctx.Value(dnsAuthorityLookupKey{}).(*dnsAuthorityLookup)
	if !ok {
		authorityLookup = newDNSAuthorityLookup()
	}
	authorityLookup.start(ctx, zone, publicResolvers, timeout)
	answers := make([]dnsAnswer, len(publicResolvers))
	var wg sync.WaitGroup
	for index, server := range publicResolvers {
		index, server := index, server
		wg.Add(1)
		go func() {
			defer wg.Done()
			queryContext, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			values, err := lookup(resolverFor(server, timeout), queryContext, name)
			answers[index] = dnsAnswer{resolver: strings.TrimSuffix(server, ":53"), values: values, err: err}
		}()
	}
	wg.Wait()

	<-authorityLookup.ready
	nameservers := authorityLookup.nameservers
	if len(nameservers) == 0 {
		answers = append(answers, dnsAnswer{resolver: "authoritative nameserver", err: errors.New("could not discover an authoritative nameserver")})
		return answers
	}

	authAnswers := make([]dnsAnswer, len(nameservers))
	for index, nameserver := range nameservers {
		index, nameserver := index, nameserver
		wg.Add(1)
		go func() {
			defer wg.Done()
			queryContext, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			values, err := lookup(resolverFor(net.JoinHostPort(nameserver.address, "53"), timeout), queryContext, name)
			authAnswers[index] = dnsAnswer{
				resolver: "authoritative " + nameserver.name + " (" + nameserver.address + ")",
				values:   values,
				err:      err,
			}
		}()
	}
	wg.Wait()
	return append(answers, authAnswers...)
}

type authoritativeNameserver struct {
	name    string
	address string
}

type dnsAuthorityLookupKey struct{}

type dnsAuthorityLookup struct {
	once        sync.Once
	ready       chan struct{}
	nameservers []authoritativeNameserver
}

func withDNSAuthorityLookup(ctx context.Context) context.Context {
	return context.WithValue(ctx, dnsAuthorityLookupKey{}, newDNSAuthorityLookup())
}

func newDNSAuthorityLookup() *dnsAuthorityLookup {
	return &dnsAuthorityLookup{ready: make(chan struct{})}
}

func (a *dnsAuthorityLookup) start(ctx context.Context, zone string, resolvers []string, timeout time.Duration) {
	a.once.Do(func() {
		go func() {
			lookupContext, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			a.nameservers = lookupAuthoritativeNameservers(lookupContext, zone, resolvers, timeout)
			close(a.ready)
		}()
	})
}

func lookupAuthoritativeNameservers(ctx context.Context, zone string, publicResolvers []string, timeout time.Duration) []authoritativeNameserver {
	type nsResult struct {
		zone    string
		records []*net.NS
	}
	candidates := authoritativeZoneCandidates(zone)
	nsResults := make(chan nsResult, len(publicResolvers)*len(candidates))
	var wg sync.WaitGroup
	for _, candidate := range candidates {
		for _, server := range publicResolvers {
			candidate, server := candidate, server
			wg.Add(1)
			go func() {
				defer wg.Done()
				records, err := resolverFor(server, timeout).LookupNS(ctx, candidate)
				if err == nil && len(records) > 0 {
					nsResults <- nsResult{zone: candidate, records: records}
				}
			}()
		}
	}
	wg.Wait()
	close(nsResults)

	hosts := make(map[string]struct{})
	selectedZone := ""
	for result := range nsResults {
		if len(result.zone) > len(selectedZone) {
			selectedZone = result.zone
			hosts = make(map[string]struct{})
		}
		if result.zone != selectedZone {
			continue
		}
		for _, record := range result.records {
			if record != nil {
				hosts[strings.ToLower(strings.TrimSuffix(record.Host, "."))] = struct{}{}
			}
		}
	}
	hostnames := make([]string, 0, len(hosts))
	for host := range hosts {
		hostnames = append(hostnames, host)
	}
	sort.Strings(hostnames)
	if len(hostnames) > 8 {
		hostnames = hostnames[:8]
	}

	type ipResult struct {
		name string
		ips  []net.IPAddr
	}
	ipResults := make(chan ipResult, len(hostnames)*len(publicResolvers))
	for _, host := range hostnames {
		for _, server := range publicResolvers {
			host, server := host, server
			wg.Add(1)
			go func() {
				defer wg.Done()
				ips, _ := resolverFor(server, timeout).LookupIPAddr(ctx, host)
				ipResults <- ipResult{name: host, ips: ips}
			}()
		}
	}
	wg.Wait()
	close(ipResults)

	found := make(map[string]authoritativeNameserver)
	for result := range ipResults {
		for _, ip := range result.ips {
			if ip.IP == nil {
				continue
			}
			address := ip.IP.String()
			found[result.name+"|"+address] = authoritativeNameserver{name: result.name, address: address}
		}
	}
	keys := make([]string, 0, len(found))
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = keys[:8]
	}
	result := make([]authoritativeNameserver, 0, len(keys))
	for _, key := range keys {
		result = append(result, found[key])
	}
	return result
}

func authoritativeZoneCandidates(name string) []string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" {
		return nil
	}
	registrableDomain, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return []string{name}
	}
	labels := strings.Split(name, ".")
	minimumLabels := len(strings.Split(registrableDomain, "."))
	candidates := make([]string, 0, len(labels)-minimumLabels+1)
	for len(labels) >= minimumLabels {
		candidates = append(candidates, strings.Join(labels, "."))
		if len(labels) == minimumLabels {
			break
		}
		labels = labels[1:]
	}
	return candidates
}

func resolverFor(server string, timeout time.Duration) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, network, server)
		},
	}
}
