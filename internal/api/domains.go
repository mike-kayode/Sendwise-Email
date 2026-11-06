package api

import (
	"context"
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
	id, domain, status, dkim_selector, COALESCE(dkim_public_key, '') AS dkim_public_key, spf_status, dkim_status,
	dmarc_status, verification_status, last_checked_at, verified_at, created_at
`

type sendingDomain struct {
	ID                 string     `json:"id"`
	Domain             string     `json:"domain"`
	Status             string     `json:"status"`
	DKIMSelector       string     `json:"dkim_selector"`
	DKIMPublicKey      string     `json:"dkim_public_key,omitempty"`
	SPFStatus          string     `json:"spf_status"`
	DKIMStatus         string     `json:"dkim_status"`
	DMARCStatus        string     `json:"dmarc_status"`
	VerificationStatus string     `json:"verification_status"`
	LastCheckedAt      *time.Time `json:"last_checked_at"`
	VerifiedAt         *time.Time `json:"verified_at"`
	CreatedAt          time.Time  `json:"created_at"`
}

func scanDomain(row pgx.Row) (*sendingDomain, error) {
	var d sendingDomain
	if err := row.Scan(
		&d.ID, &d.Domain, &d.Status, &d.DKIMSelector, &d.DKIMPublicKey,
		&d.SPFStatus, &d.DKIMStatus, &d.DMARCStatus, &d.VerificationStatus,
		&d.LastCheckedAt, &d.VerifiedAt, &d.CreatedAt,
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

func (s *Server) verifyDomain(w http.ResponseWriter, r *http.Request) {
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

	spf, dkim, dmarc, verification := s.checkDomainDNS(r.Context(), d.Domain, d.DKIMSelector, d.DKIMPublicKey)
	status := "pending"
	switch {
	case spf == "verified" && dkim == "verified" && dmarc == "verified" && verification == "verified":
		status = "verified"
	case hasDomainDNSResult("verified", spf, dkim, dmarc, verification) ||
		hasDomainDNSResult("mismatched", spf, dkim, dmarc, verification):
		status = "misconfigured"
	}

	updated, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET status = $3,
		    spf_status = $4,
		    dkim_status = $5,
		    dmarc_status = $6,
		    verification_status = $7,
		    last_checked_at = now(),
		    verified_at = CASE WHEN $3 = 'verified' THEN COALESCE(verified_at, now()) ELSE verified_at END,
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		d.ID, s.organizationID(r), status, spf, dkim, dmarc, verification,
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

type dnsRecord struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	Purpose string `json:"purpose"`
	Status  string `json:"status"`
}

type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

type netDNSResolver struct {
	resolver *net.Resolver
	timeout  time.Duration
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
	return &netDNSResolver{resolver: resolver, timeout: timeout}
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
	records := []dnsRecord{
		{
			Type:    "TXT",
			Name:    d.Domain,
			Value:   spfRecordValue(spfInclude),
			Purpose: "SPF authorizes your sending infrastructure to send for this domain.",
			Status:  d.SPFStatus,
		},
		{
			Type:    "TXT",
			Name:    d.DKIMSelector + "._domainkey." + d.Domain,
			Value:   dkimRecordValue(d.DKIMPublicKey),
			Purpose: "DKIM cryptographically signs outbound messages. Copy the public key from your DNS provider.",
			Status:  d.DKIMStatus,
		},
		{
			Type:    "TXT",
			Name:    "_dmarc." + d.Domain,
			Value:   "v=DMARC1; p=none; rua=mailto:dmarc@" + d.Domain,
			Purpose: "DMARC tells receiving servers what to do with messages that fail authentication.",
			Status:  d.DMARCStatus,
		},
		{
			Type:    "TXT",
			Name:    "_sendwise-verification." + d.Domain,
			Value:   "v=sendwise-verification " + strings.ToLower(d.Domain),
			Purpose: "Verification token used to prove domain ownership during DNS checks.",
			Status:  verification,
		},
	}

	if host := trackingHostname(trackingBase); host != "" {
		records = append(records, dnsRecord{
			Type:    "CNAME",
			Name:    "track." + d.Domain,
			Value:   host,
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

func (s *Server) checkDomainDNS(ctx context.Context, domain, selector, expectedPublicKey string) (spf, dkim, dmarc, verification string) {
	resolver := s.dnsResolver
	if resolver == nil {
		resolver = newDNSResolver("", 2*time.Second)
	}

	spf = "missing"
	txtValues, err := lookupTXTWithResolver(ctx, resolver, domain)
	if isDNSLookupFailure(err) {
		spf = "lookup_failed"
	} else if err == nil {
		for _, txt := range txtValues {
			lower := strings.ToLower(strings.TrimSpace(txt))
			if strings.HasPrefix(lower, "v=spf1") {
				if spfHasAuthMechanism(lower, s.cfg.SPFInclude) {
					spf = "verified"
				} else {
					spf = "mismatched"
				}
				break
			}
			if strings.Contains(lower, "v=") {
				spf = "mismatched"
			}
		}
	}

	dkim = "missing"
	txtValues, err = lookupTXTWithResolver(ctx, resolver, selector+"._domainkey."+domain)
	if isDNSLookupFailure(err) {
		dkim = "lookup_failed"
	} else if err == nil {
		for _, txt := range txtValues {
			normalized := normalizeDkimTXTValue(txt)
			if normalized == "" {
				continue
			}
			if normalized == normalizeDkimKey(expectedPublicKey) {
				dkim = "verified"
				break
			}
			dkim = "mismatched"
			break
		}
	}

	dmarc = "missing"
	txtValues, err = lookupTXTWithResolver(ctx, resolver, "_dmarc."+domain)
	if isDNSLookupFailure(err) {
		dmarc = "lookup_failed"
	} else if err == nil {
		for _, txt := range txtValues {
			lower := strings.ToLower(strings.TrimSpace(txt))
			if strings.HasPrefix(lower, "v=dmarc1") {
				dmarc = "verified"
				break
			}
			if strings.Contains(lower, "v=") {
				dmarc = "mismatched"
			}
		}
	}

	expectedVerification := strings.ToLower(strings.TrimSpace(domain))
	verification = "missing"
	txtValues, err = lookupTXTWithResolver(ctx, resolver, "_sendwise-verification."+domain)
	if isDNSLookupFailure(err) {
		verification = "lookup_failed"
	} else if err == nil {
		for _, txt := range txtValues {
			lower := strings.ToLower(strings.TrimSpace(txt))
			if !strings.Contains(lower, "v=sendwise-verification") {
				continue
			}
			candidate := strings.TrimSpace(strings.TrimPrefix(lower, "v=sendwise-verification"))
			candidate = strings.Trim(candidate, " \t\r\n\"'")
			if candidate == expectedVerification || candidate == "\""+expectedVerification+"\"" {
				verification = "verified"
				break
			}
			verification = "mismatched"
		}
	}

	return spf, dkim, dmarc, verification
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

func spfRecordValue(spfInclude string) string {
	spfInclude = strings.TrimSpace(spfInclude)
	if spfInclude == "" {
		return "v=spf1 mx ~all"
	}
	return "v=spf1 include:" + spfInclude + " ~all"
}

func spfHasAuthMechanism(spfRecord, configuredInclude string) bool {
	mechanisms := strings.Fields(spfRecord)
	for _, mechanism := range mechanisms {
		mechanism = strings.TrimLeft(strings.ToLower(mechanism), "+-~?")
		if mechanism == "mx" || configuredInclude != "" && mechanism == "include:"+strings.ToLower(configuredInclude) {
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
