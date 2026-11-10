package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/httpx"
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// domainColumns selects every public sending-domain column. dkim_public_key is
// nullable in the schema, so it is coalesced in SQL: a NULL must never reach the
// scanner as a typed nil and must never fail the whole domain read.
const domainColumns = `
	id, domain, status, dkim_selector, COALESCE(dkim_public_key, '') AS dkim_public_key, verification_token,
	spf_status, dkim_status, dmarc_status, verification_status, last_checked_at, verified_at, created_at,
	COALESCE(dns_check_details, '{}'::jsonb) AS dns_check_details,
	owner_conflict, stalwart_authenticated, warmup_profile
`

type sendingDomain struct {
	ID                    string          `json:"id"`
	Domain                string          `json:"domain"`
	Status                string          `json:"status"`
	DKIMSelector          string          `json:"dkim_selector"`
	DKIMPublicKey         string          `json:"dkim_public_key,omitempty"`
	VerificationToken     string          `json:"-"`
	SPFStatus             string          `json:"spf_status"`
	DKIMStatus            string          `json:"dkim_status"`
	DMARCStatus           string          `json:"dmarc_status"`
	VerificationStatus    string          `json:"verification_status"`
	LastCheckedAt         *time.Time      `json:"last_checked_at"`
	VerifiedAt            *time.Time      `json:"verified_at"`
	CreatedAt             time.Time       `json:"created_at"`
	DNSCheckDetails       json.RawMessage `json:"dns_check_details"`
	OwnerConflict         bool            `json:"owner_conflict"`
	StalwartAuthenticated bool            `json:"stalwart_authenticated"`
	WarmupProfile         string          `json:"warmup_profile"`
}

func scanDomain(row pgx.Row) (*sendingDomain, error) {
	var d sendingDomain
	if err := row.Scan(
		&d.ID, &d.Domain, &d.Status, &d.DKIMSelector, &d.DKIMPublicKey, &d.VerificationToken,
		&d.SPFStatus, &d.DKIMStatus, &d.DMARCStatus, &d.VerificationStatus,
		&d.LastCheckedAt, &d.VerifiedAt, &d.CreatedAt, &d.DNSCheckDetails, &d.OwnerConflict,
		&d.StalwartAuthenticated, &d.WarmupProfile,
	); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT `+domainColumns+`
		FROM sending_domains
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list domains", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domains.")
		return
	}
	defer rows.Close()

	domains := make([]sendingDomain, 0)
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domains.")
			return
		}
		domains = append(domains, *d)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": domains})
}

type domainInput struct {
	Domain string `json:"domain"`
}

func generateDKIMKeyPair(dkimEncryptionKey string) (publicKeyB64 string, encryptedPrivateKey string, err error) {
	return delivery.GenerateDKIMKeyPair(dkimEncryptionKey)
}

// decryptDKIMPrivateKey reverses generateDKIMKeyPair for a stored envelope.
func decryptDKIMPrivateKey(dkimEncryptionKey string, encryptedPrivateKey string) (string, error) {
	return delivery.DecryptDKIMPrivateKey(dkimEncryptionKey, encryptedPrivateKey)
}

func (s *Server) createDomain(w http.ResponseWriter, r *http.Request) {
	var input domainInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}

	domain, err := normalizeDomain(input.Domain)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain", "Enter a valid domain such as newsletter.example.com.")
		return
	}
	if s.cfg.DKIMEncryptionKey == "" {
		s.logger.Error("create domain", "error", errors.New("DKIM_ENCRYPTION_KEY is not configured"))
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not generate the sending domain DKIM key: DKIM_ENCRYPTION_KEY is not configured.")
		return
	}

	publicKeyB64, encryptedPrivateKey, err := generateDKIMKeyPair(s.cfg.DKIMEncryptionKey)
	if err != nil {
		s.logger.Error("generate dkim keypair", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not generate the sending domain DKIM key.")
		return
	}

	d, err := scanDomain(s.pool.QueryRow(r.Context(), `
		INSERT INTO sending_domains (organization_id, domain, status, dkim_selector, dkim_public_key, dkim_private_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+domainColumns,
		s.organizationID(r), domain, "pending", s.cfg.DKIMSelector, publicKeyB64, encryptedPrivateKey,
	))
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "domain_exists", "This sending domain is already registered.")
		return
	}
	if err != nil {
		s.logger.Error("create domain", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not add the sending domain.")
		return
	}

	httpx.JSON(w, http.StatusCreated, d)
}

func (s *Server) deleteDomain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin sending domain delete", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the sending domain.")
		return
	}
	defer tx.Rollback(ctx)

	var domain string
	err = tx.QueryRow(ctx, `
		SELECT domain FROM sending_domains
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, r.PathValue("id"), s.organizationID(r)).Scan(&domain)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load sending domain for delete", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the sending domain.")
		return
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM sender_identities
		WHERE organization_id = $1 AND lower(domain) = lower($2)
	`, s.organizationID(r), domain); err != nil {
		s.logger.Error("delete sender identities for sending domain", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the sending domain.")
		return
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM sending_domains
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)); err != nil {
		s.logger.Error("delete sending domain", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the sending domain.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit sending domain delete", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the sending domain.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifyDomain(w http.ResponseWriter, r *http.Request) {
	s.verifyDomainByProvider(w, r, "")
}

func (s *Server) verifyDomainProvider(w http.ResponseWriter, r *http.Request) {
	s.verifyDomainByProvider(w, r, r.PathValue("provider"))
}

// verifyDomainByProvider verifies a sending domain. Every domain is hosted on
// Stalwart, so any provider other than "stalwart" is rejected: there is no
// second onboarding path to route to.
func (s *Server) verifyDomainByProvider(w http.ResponseWriter, r *http.Request, provider string) {
	d, err := scanDomain(s.pool.QueryRow(r.Context(), `
		SELECT `+domainColumns+`
		FROM sending_domains
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get domain for verification", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sending domain.")
		return
	}
	if provider != "" && strings.ToLower(strings.TrimSpace(provider)) != delivery.ProviderStalwart {
		httpx.Error(w, http.StatusBadRequest, "campaign_delivery_stalwart_only", "Campaign sending domains are verified only through Stalwart.")
		return
	}
	s.verifyStalwartDomain(w, r, d)
}

func (s *Server) verifyStalwartDomain(w http.ResponseWriter, r *http.Request, d *sendingDomain) {
	if d.OwnerConflict {
		httpx.Error(w, http.StatusConflict, "domain_owner_conflict", "Domain already owned by another workspace.")
		return
	}
	checks := s.checkDomainDNSDetails(
		r.Context(), d.Domain, d.DKIMSelector, d.DKIMPublicKey, d.VerificationToken,
	)
	spf, dkim, dmarc, verification := checks.Statuses()
	status := domainDNSVerificationStatus(spf, dkim, verification)
	if status == "verified" {
		if s.stalwartDomainHoster == nil {
			httpx.Error(w, http.StatusServiceUnavailable, "stalwart_unavailable", "Stalwart domain hosting is not configured.")
			return
		}
		if err := s.stalwartDomainHoster.EnsureDomain(r.Context(), d.Domain); err != nil {
			s.logger.Error("host verified sending domain in Stalwart", "domain", d.Domain, "error", err)
			httpx.Error(w, http.StatusServiceUnavailable, "stalwart_unavailable", "Stalwart could not host the verified sending domain.")
			return
		}
	}
	dnsCheckDetails, err := json.Marshal(checks)
	if err != nil {
		s.logger.Error("encode domain DNS verification details", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save domain DNS verification results.")
		return
	}

	updated, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET status = $3,
		    spf_status = $4,
		    dkim_status = $5,
		    dmarc_status = $6,
		    verification_status = $7,
		    dns_check_details = $8::jsonb,
		    stalwart_authenticated = ($3 = 'verified'),
		    last_checked_at = now(),
		    verified_at = CASE WHEN $3 = 'verified' THEN COALESCE(verified_at, now()) ELSE verified_at END,
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		d.ID, s.organizationID(r), status, spf, dkim, dmarc, verification, dnsCheckDetails,
	))
	if err != nil {
		s.logger.Error("update domain verification", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update domain verification.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"domain":      updated,
		"dns_records": dnsRecordsFor(*updated, verification, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
	})
}

// configureDomainProvider re-runs the Stalwart DKIM key setup for a domain.
// Any other provider is rejected: domains are hosted on Stalwart only.
func (s *Server) configureDomainProvider(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if provider != delivery.ProviderStalwart {
		httpx.Error(w, http.StatusBadRequest, "campaign_delivery_stalwart_only", "Sending domains are hosted on Stalwart only.")
		return
	}
	domain, err := scanDomain(s.pool.QueryRow(r.Context(), `
		SELECT `+domainColumns+`
		FROM sending_domains
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load sending domain for provider setup", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sending domain.")
		return
	}

	s.configureStalwartDomain(w, r, domain)
}

func (s *Server) configureStalwartDomain(w http.ResponseWriter, r *http.Request, domain *sendingDomain) {
	if domain.DKIMPublicKey != "" {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"domain":      domain,
			"dns_records": dnsRecordsFor(*domain, domain.VerificationStatus, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
		})
		return
	}
	if s.cfg.DKIMEncryptionKey == "" {
		httpx.Error(w, http.StatusServiceUnavailable, "encryption_unavailable", "Stalwart DKIM key encryption is not configured.")
		return
	}
	publicKey, encryptedPrivateKey, err := generateDKIMKeyPair(s.cfg.DKIMEncryptionKey)
	if err != nil {
		s.logger.Error("generate secondary Stalwart DKIM key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not generate the Stalwart DKIM key.")
		return
	}
	updated, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET dkim_selector = $3, dkim_public_key = $4, dkim_private_key = $5,
		    stalwart_authenticated = false, updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		domain.ID, s.organizationID(r), s.cfg.DKIMSelector, publicKey, encryptedPrivateKey,
	))
	if err != nil {
		s.logger.Error("save secondary Stalwart DKIM key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save the Stalwart DKIM key.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"domain":      updated,
		"dns_records": dnsRecordsFor(*updated, updated.VerificationStatus, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
	})
}

func (s *Server) domainDNSRecords(w http.ResponseWriter, r *http.Request) {
	d, err := scanDomain(s.pool.QueryRow(r.Context(), `
		SELECT `+domainColumns+`
		FROM sending_domains
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get domain dns records", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sending domain.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"domain":      d,
		"dns_records": dnsRecordsFor(*d, d.VerificationStatus, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
	})
}

func (s *Server) updateDomainWarmupProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Profile string `json:"profile"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	profile := strings.ToLower(strings.TrimSpace(input.Profile))
	if profile != "standard" && profile != "accelerated" {
		httpx.Error(w, http.StatusBadRequest, "invalid_warmup_profile", "Choose standard or accelerated as the warm-up profile.")
		return
	}
	domain, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET warmup_profile = $3, updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		r.PathValue("id"), s.organizationID(r), profile,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("update sending-domain warm-up profile", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the sending-domain warm-up profile.")
		return
	}
	httpx.JSON(w, http.StatusOK, domain)
}

type dnsRecord struct {
	Type        string             `json:"type"`
	Name        string             `json:"name"`
	Value       string             `json:"value"`
	TTL         string             `json:"ttl"`
	Purpose     string             `json:"purpose"`
	Status      string             `json:"status"`
	FoundValue  string             `json:"found_value,omitempty"`
	Resolvers   []dnsResolverCheck `json:"resolvers,omitempty"`
	LastChecked *time.Time         `json:"last_checked,omitempty"`
}

type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

type netDNSResolver struct {
	resolver *net.Resolver
	timeout  time.Duration
	// servers are the recursive resolvers used for record lookups and
	// authoritative-nameserver discovery.
	servers []string
}

// defaultDNSResolvers are the recursive resolvers used for sending-domain DNS
// verification when DNS_RESOLVER_ADDR is not configured.
var defaultDNSResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// dnsResolverServers returns the recursive resolvers a check queries. A
// configured DNS_RESOLVER_ADDR replaces the public defaults so an operator's
// resolver (for example one that is the only reachable resolver in a locked-down
// network) is actually consulted instead of being silently ignored.
func dnsResolverServers(serverAddr string) []string {
	if strings.TrimSpace(serverAddr) == "" {
		return append([]string(nil), defaultDNSResolvers...)
	}
	return []string{dnsServerAddress(serverAddr)}
}

// dnsServerAddress normalizes a configured resolver address to the host:port
// form resolverFor dials verbatim.
func dnsServerAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	// Bare IP (including IPv6) or bare hostname: append the DNS port.
	if !strings.Contains(addr, ":") || net.ParseIP(addr) != nil {
		return net.JoinHostPort(addr, "53")
	}
	return addr
}

func newDNSResolver(serverAddr string, timeout time.Duration) DNSResolver {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	resolver := &net.Resolver{PreferGo: true}
	if serverAddr != "" {
		resolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, "udp", serverAddr)
		}
	}
	return &netDNSResolver{resolver: resolver, timeout: timeout, servers: dnsResolverServers(serverAddr)}
}

func (r *netDNSResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	if r == nil || r.resolver == nil {
		return nil, fmt.Errorf("dns resolver is not configured")
	}
	if _, ok := ctx.Deadline(); !ok && r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	return r.resolver.LookupTXT(ctx, name)
}

// lookupTXTWithResolver resolves TXT records and distinguishes a failed lookup
// ("lookup_failed") from a name that simply has no records ("missing").
func lookupTXTWithResolver(ctx context.Context, resolver DNSResolver, name string) ([]string, error) {
	if resolver == nil {
		return nil, fmt.Errorf("lookup TXT %s: DNS resolver is not configured", name)
	}
	values, err := resolver.LookupTXT(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("lookup TXT %s: %w", name, err)
	}
	return values, nil
}

func dnsRecordsFor(d sendingDomain, verification string, trackingBase, spfInclude string) []dnsRecord {
	var checks dnsCheckDetails
	if len(d.DNSCheckDetails) > 0 {
		_ = json.Unmarshal(d.DNSCheckDetails, &checks)
	}
	record := func(key string, fallbackStatus string) dnsRecord {
		result, ok := checks.Records[key]
		if !ok {
			return dnsRecord{TTL: "300", Status: fallbackStatus}
		}
		status := result.Status
		switch status {
		case "verified":
			status = "found"
		case "mismatched":
			status = "wrong_value"
		case "found", "wrong_value", "missing", "lookup_failed":
		default:
			status = fallbackStatus
		}
		return dnsRecord{
			TTL:         "300",
			Status:      status,
			FoundValue:  result.FoundValue,
			Resolvers:   result.Resolvers,
			LastChecked: checks.CheckedAt,
		}
	}
	spfRecord := record("spf", d.SPFStatus)
	dkimRecord := record("dkim", d.DKIMStatus)
	dmarcRecord := record("dmarc", d.DMARCStatus)
	verificationRecord := record("verification", verification)
	mxRecord := record("mx", "warning")
	records := []dnsRecord{
		{
			Type:        "TXT",
			Name:        d.Domain,
			Value:       spfRecordValue(spfInclude),
			TTL:         spfRecord.TTL,
			Purpose:     "SPF must authorize the Stalwart sending host. Set SPF_INCLUDE to its authorized SPF domain; mx only works when this domain's MX points to Stalwart.",
			Status:      spfRecord.Status,
			FoundValue:  spfRecord.FoundValue,
			Resolvers:   spfRecord.Resolvers,
			LastChecked: spfRecord.LastChecked,
		},
		{
			Type:        "TXT",
			Name:        d.DKIMSelector + "._domainkey." + d.Domain,
			Value:       dkimRecordValue(d.DKIMPublicKey),
			TTL:         dkimRecord.TTL,
			Purpose:     "DKIM cryptographically signs outbound messages. Copy the public key from your DNS provider.",
			Status:      dkimRecord.Status,
			FoundValue:  dkimRecord.FoundValue,
			Resolvers:   dkimRecord.Resolvers,
			LastChecked: dkimRecord.LastChecked,
		},
		{
			Type:        "TXT",
			Name:        "_dmarc." + d.Domain,
			Value:       "v=DMARC1; p=none; rua=mailto:dmarc@" + d.Domain,
			TTL:         dmarcRecord.TTL,
			Purpose:     "DMARC tells receiving servers what to do with messages that fail authentication.",
			Status:      dmarcRecord.Status,
			FoundValue:  dmarcRecord.FoundValue,
			Resolvers:   dmarcRecord.Resolvers,
			LastChecked: dmarcRecord.LastChecked,
		},
		{
			Type:        "TXT",
			Name:        "_sendwise-verification." + d.Domain,
			Value:       "v=sendwise-verification " + d.VerificationToken,
			TTL:         verificationRecord.TTL,
			Purpose:     "Verification token used to prove domain ownership during DNS checks.",
			Status:      verificationRecord.Status,
			FoundValue:  verificationRecord.FoundValue,
			Resolvers:   verificationRecord.Resolvers,
			LastChecked: verificationRecord.LastChecked,
		},
		{
			Type:        "MX",
			Name:        d.Domain,
			Value:       "",
			TTL:         mxRecord.TTL,
			Purpose:     "Optional for sending. MX is checked as a warning and does not block verification.",
			Status:      mxRecord.Status,
			FoundValue:  mxRecord.FoundValue,
			Resolvers:   mxRecord.Resolvers,
			LastChecked: mxRecord.LastChecked,
		},
	}

	if host := trackingHostname(trackingBase); host != "" {
		records = append(records, dnsRecord{
			Type:    "CNAME",
			Name:    "track." + d.Domain,
			Value:   host,
			TTL:     "300",
			Purpose: "Optional branded click-tracking domain.",
			Status:  "pending",
		})
	}

	return records
}

func dkimRecordValue(publicKeyB64 string) string {
	publicKeyB64 = strings.TrimSpace(publicKeyB64)
	if publicKeyB64 == "" {
		return ""
	}
	return "v=DKIM1; k=rsa; p=" + publicKeyB64
}

// trackingHostname returns the click-tracking host for a configured tracking
// base URL, or an empty string when no real tracking host is configured. Local
// development defaults (localhost / loopback) are treated as unconfigured so no
// placeholder tracking record is emitted.
func trackingHostname(trackingBase string) string {
	trimmed := strings.TrimSpace(trackingBase)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "", "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return ""
	}
	return host
}

func normalizeDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	domain = strings.TrimSuffix(domain, ".")
	if strings.Contains(domain, "://") || strings.ContainsAny(domain, "/@ :") {
		return "", errors.New("invalid domain")
	}
	if !domainPattern.MatchString(domain) {
		return "", errors.New("invalid domain")
	}
	return domain, nil
}

func (s *Server) checkDomainDNS(ctx context.Context, domain, selector, expectedPublicKey, expectedVerificationToken string) (spf, dkim, dmarc, verification string) {
	return s.checkDomainDNSDetails(ctx, domain, selector, expectedPublicKey, expectedVerificationToken).Statuses()
}

func (s *Server) checkDomainDNSDetails(ctx context.Context, domain, selector, expectedPublicKey, expectedVerificationToken string) dnsCheckDetails {
	resolver := s.dnsResolver
	if resolver == nil {
		resolver = newDNSResolver("", 2*time.Second)
	}
	ctx = withDNSAuthorityLookup(ctx)
	expectedVerificationToken = strings.ToLower(strings.TrimSpace(expectedVerificationToken))
	checks := dnsCheckDetails{Records: make(map[string]dnsRecordResult), CheckedAt: timePointer(time.Now().UTC())}
	specs := []dnsRecordSpec{
		{Key: "spf", Type: "TXT", Name: domain, Zone: domain},
		{Key: "dkim", Type: "TXT", Name: selector + "._domainkey." + domain, Zone: domain},
		{Key: "dmarc", Type: "TXT", Name: "_dmarc." + domain, Zone: domain},
		{Key: "verification", Type: "TXT", Name: "_sendwise-verification." + domain, Zone: domain},
		{Key: "mx", Type: "MX", Name: domain, Zone: domain},
	}
	type result struct {
		key    string
		record dnsRecordResult
	}
	results := make(chan result, len(specs))
	for _, spec := range specs {
		spec := spec
		go func() {
			record := lookupDNSRecord(ctx, resolver, spec, func(value string) bool {
				switch spec.Key {
				case "spf":
					lower := strings.ToLower(strings.TrimSpace(value))
					return strings.HasPrefix(lower, "v=spf1") && spfHasAuthMechanism(lower, s.cfg.SPFInclude)
				case "dkim":
					return normalizeDkimTXTValue(value) == normalizeDkimKey(expectedPublicKey)
				case "dmarc":
					return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "v=dmarc1")
				case "verification":
					lower := strings.ToLower(strings.TrimSpace(value))
					candidate := strings.TrimSpace(strings.TrimPrefix(lower, "v=sendwise-verification"))
					candidate = strings.Trim(candidate, " \t\r\n\"'")
					return strings.HasPrefix(lower, "v=sendwise-verification") && candidate == expectedVerificationToken && expectedVerificationToken != ""
				case "mx":
					return strings.TrimSpace(value) != ""
				default:
					return false
				}
			})
			results <- result{key: spec.Key, record: record}
		}()
	}
	for range specs {
		result := <-results
		checks.Records[result.key] = result.record
	}
	return checks
}

func isDNSLookupFailure(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false
	}
	return true
}

func hasDomainDNSResult(want string, statuses ...string) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

func domainDNSVerificationStatus(spf, dkim, verification string) string {
	if spf == "verified" && dkim == "verified" && verification == "verified" {
		return "verified"
	}
	if hasDomainDNSResult("verified", spf, dkim, verification) ||
		hasDomainDNSResult("mismatched", spf, dkim, verification) {
		return "misconfigured"
	}
	return "pending"
}

func spfRecordValue(spfInclude string) string {
	spfInclude = strings.TrimSpace(spfInclude)
	if spfInclude == "" {
		return "v=spf1 mx ~all"
	}
	return "v=spf1 include:" + spfInclude + " ~all"
}

func spfHasAuthMechanism(spfRecord, configuredInclude string) bool {
	configuredInclude = strings.ToLower(strings.TrimSpace(configuredInclude))
	mechanisms := strings.Fields(spfRecord)
	for _, mechanism := range mechanisms {
		mechanism = strings.TrimLeft(strings.ToLower(mechanism), "+-~?")
		if (configuredInclude == "" && mechanism == "mx") ||
			(configuredInclude != "" && mechanism == "include:"+configuredInclude) {
			return true
		}
	}
	return false
}

func normalizeDkimKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "\"", "")
	key = strings.ReplaceAll(key, "'", "")
	key = strings.ReplaceAll(key, "-----BEGIN PUBLIC KEY-----", "")
	key = strings.ReplaceAll(key, "-----END PUBLIC KEY-----", "")
	key = strings.ReplaceAll(key, "\\n", "")
	key = strings.ReplaceAll(key, "\n", "")
	return strings.Join(strings.Fields(key), "")
}

// normalizeDkimTXTValue normalizes a DKIM TXT record value: strips quotes and
// whitespace, takes the "p=" tag, and returns the normalized public key for
// exact comparison.
func normalizeDkimTXTValue(txt string) string {
	value := strings.ReplaceAll(strings.TrimSpace(txt), "\"", "")
	value = strings.ReplaceAll(value, "'", "")
	for _, part := range strings.Split(value, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "p=") {
			return normalizeDkimKey(strings.TrimSpace(strings.TrimPrefix(part, "p=")))
		}
	}
	return ""
}
