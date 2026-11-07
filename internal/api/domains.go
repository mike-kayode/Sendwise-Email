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
	provider, COALESCE(resend_domain_id, ''), COALESCE(resend_dns_records, '[]'::jsonb)::text,
	stalwart_authenticated, resend_authenticated
`

type sendingDomain struct {
	ID                    string                     `json:"id"`
	Domain                string                     `json:"domain"`
	Status                string                     `json:"status"`
	DKIMSelector          string                     `json:"dkim_selector"`
	DKIMPublicKey         string                     `json:"dkim_public_key,omitempty"`
	VerificationToken     string                     `json:"-"`
	SPFStatus             string                     `json:"spf_status"`
	DKIMStatus            string                     `json:"dkim_status"`
	DMARCStatus           string                     `json:"dmarc_status"`
	VerificationStatus    string                     `json:"verification_status"`
	LastCheckedAt         *time.Time                 `json:"last_checked_at"`
	VerifiedAt            *time.Time                 `json:"verified_at"`
	CreatedAt             time.Time                  `json:"created_at"`
	Provider              string                     `json:"provider"`
	ResendDomainID        string                     `json:"resend_domain_id,omitempty"`
	ResendDNSRecords      []delivery.ResendDNSRecord `json:"resend_dns_records,omitempty"`
	StalwartAuthenticated bool                       `json:"stalwart_authenticated"`
	ResendAuthenticated   bool                       `json:"resend_authenticated"`
}

func scanDomain(row pgx.Row) (*sendingDomain, error) {
	var d sendingDomain
	var resendRecords string
	if err := row.Scan(
		&d.ID, &d.Domain, &d.Status, &d.DKIMSelector, &d.DKIMPublicKey, &d.VerificationToken,
		&d.SPFStatus, &d.DKIMStatus, &d.DMARCStatus, &d.VerificationStatus,
		&d.LastCheckedAt, &d.VerifiedAt, &d.CreatedAt, &d.Provider, &d.ResendDomainID, &resendRecords,
		&d.StalwartAuthenticated, &d.ResendAuthenticated,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(resendRecords), &d.ResendDNSRecords); err != nil {
		return nil, fmt.Errorf("decode Resend DNS records: %w", err)
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
	Domain   string `json:"domain"`
	Provider string `json:"provider"`
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
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider == "" {
		provider = delivery.ProviderStalwart
	}
	if provider != delivery.ProviderStalwart && provider != delivery.ProviderResend {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider", "Choose stalwart or resend as the domain provider.")
		return
	}

	if provider == delivery.ProviderResend {
		s.createResendDomain(w, r, domain)
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
		INSERT INTO sending_domains (organization_id, domain, status, dkim_selector, dkim_public_key, dkim_private_key, provider)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+domainColumns,
		s.organizationID(r), domain, "pending", s.cfg.DKIMSelector, publicKeyB64, encryptedPrivateKey, delivery.ProviderStalwart,
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
	if d.Provider == delivery.ProviderResend {
		s.verifyResendDomain(w, r, d)
		return
	}

	spf, dkim, dmarc, verification := s.checkDomainDNS(
		r.Context(), d.Domain, d.DKIMSelector, d.DKIMPublicKey, d.VerificationToken,
	)
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
		    stalwart_authenticated = ($3 = 'verified'),
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
		"dns_records": domainDNSRecordsFor(*updated, verification, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
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
		"dns_records": domainDNSRecordsFor(*d, d.VerificationStatus, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
	})
}

func (s *Server) createResendDomain(w http.ResponseWriter, r *http.Request, domain string) {
	if s.resendDomains == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "resend_unavailable", "Resend API client is not configured.")
		return
	}
	workspaceID := s.organizationID(r)
	apiKey, err := s.workspaceResendAPIKey(r, workspaceID)
	if err != nil {
		s.logger.Error("read Resend API key for domain creation", "workspace_id", workspaceID, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not read the configured Resend API key.")
		return
	}
	if apiKey == "" {
		httpx.Error(w, http.StatusBadRequest, "resend_api_key_required", "Save a Resend API key in Settings before adding a Resend domain.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin Resend domain transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not add the sending domain.")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO sending_domains (organization_id, domain, status, dkim_selector, provider)
		VALUES ($1, $2, 'pending', '', 'resend')
	`, workspaceID, domain); isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "domain_exists", "This sending domain is already registered to a workspace.")
		return
	} else if err != nil {
		s.logger.Error("reserve Resend domain ownership", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not reserve the sending domain.")
		return
	}
	resendDomain, err := s.resendDomains.CreateDomain(r.Context(), apiKey, domain)
	if err != nil {
		s.logger.Warn("Resend domain creation failed", "workspace_id", workspaceID, "domain", domain, "error", err)
		httpx.Error(w, http.StatusBadGateway, "resend_domain_create_failed", "Resend could not create the domain. Check the domain and API key, then try again.")
		return
	}
	if resendDomain == nil || strings.TrimSpace(resendDomain.ID) == "" {
		httpx.Error(w, http.StatusBadGateway, "resend_domain_invalid_response", "Resend returned an incomplete domain response.")
		return
	}
	records, err := json.Marshal(resendDomain.Records)
	if err != nil {
		s.logger.Error("encode Resend DNS records", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save Resend DNS records.")
		return
	}
	status, spf, dkim, dmarc, verification := resendDomainStatuses(resendDomain)
	updated, err := scanDomain(tx.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET resend_domain_id = $3, resend_dns_records = $4::jsonb, status = $5,
		    spf_status = $6, dkim_status = $7, dmarc_status = $8, verification_status = $9,
		    resend_authenticated = ($5 = 'verified'),
		    last_checked_at = now(),
		    verified_at = CASE WHEN $5 = 'verified' THEN now() ELSE NULL END,
		    updated_at = now()
		WHERE organization_id = $1 AND domain = $2
		RETURNING `+domainColumns,
		workspaceID, domain, resendDomain.ID, records, status, spf, dkim, dmarc, verification,
	))
	if err != nil {
		s.logger.Error("save Resend domain", "workspace_id", workspaceID, "domain", domain, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Resend created the domain but Sendwise could not save its DNS records.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit Resend domain", "workspace_id", workspaceID, "domain", domain, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Resend created the domain but Sendwise could not finish saving it.")
		return
	}
	httpx.JSON(w, http.StatusCreated, updated)
}

func (s *Server) verifyResendDomain(w http.ResponseWriter, r *http.Request, domain *sendingDomain) {
	workspaceID := s.organizationID(r)
	apiKey, err := s.workspaceResendAPIKey(r, workspaceID)
	if err != nil {
		s.logger.Error("read Resend API key for verification", "workspace_id", workspaceID, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not read the configured Resend API key.")
		return
	}
	if apiKey == "" || s.resendDomains == nil || domain.ResendDomainID == "" {
		httpx.Error(w, http.StatusBadRequest, "resend_domain_unavailable", "The Resend domain or workspace API key is not configured.")
		return
	}
	resendDomain, err := s.resendDomains.VerifyDomain(r.Context(), apiKey, domain.ResendDomainID)
	if err != nil {
		s.logger.Warn("Resend domain verification failed", "workspace_id", workspaceID, "domain", domain.Domain, "error", err)
		httpx.Error(w, http.StatusBadGateway, "resend_verify_failed", "Resend could not verify the domain. Review its DNS records and retry.")
		return
	}
	if resendDomain == nil {
		httpx.Error(w, http.StatusBadGateway, "resend_status_failed", "Resend returned an incomplete verification response.")
		return
	}
	if len(resendDomain.Records) == 0 {
		resendDomain, err = s.resendDomains.GetDomain(r.Context(), apiKey, domain.ResendDomainID)
		if err != nil {
			s.logger.Warn("read Resend domain status after verification", "workspace_id", workspaceID, "domain", domain.Domain, "error", err)
			httpx.Error(w, http.StatusBadGateway, "resend_status_failed", "Resend verification was requested, but its latest status could not be read.")
			return
		}
		if resendDomain == nil {
			httpx.Error(w, http.StatusBadGateway, "resend_status_failed", "Resend returned an incomplete domain status.")
			return
		}
	}
	status, spf, dkim, dmarc, verification := resendDomainStatuses(resendDomain)
	records, err := json.Marshal(resendDomain.Records)
	if err != nil {
		s.logger.Error("encode Resend DNS records", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save updated Resend DNS records.")
		return
	}
	updated, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET status = $3, spf_status = $4, dkim_status = $5, dmarc_status = $6,
		    verification_status = $7, resend_dns_records = $8::jsonb, last_checked_at = now(),
		    resend_authenticated = ($3 = 'verified'),
		    verified_at = CASE WHEN $3 = 'verified' THEN COALESCE(verified_at, now()) ELSE verified_at END,
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		domain.ID, workspaceID, status, spf, dkim, dmarc, verification, records,
	))
	if err != nil {
		s.logger.Error("update Resend domain verification", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save Resend verification status.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"domain":      updated,
		"dns_records": domainDNSRecordsFor(*updated, updated.VerificationStatus, s.cfg.TrackingBaseURL, s.cfg.SPFInclude),
	})
}

func resendDomainStatuses(domain *delivery.ResendDomain) (status, spf, dkim, dmarc, verification string) {
	status, spf, dkim, dmarc, verification = "pending", "missing", "missing", "missing", "pending"
	if domain == nil {
		return
	}
	for _, record := range domain.Records {
		recordType := strings.ToLower(record.Record + " " + record.Type + " " + record.Name)
		recordStatus := strings.ToLower(strings.TrimSpace(record.Status))
		if recordStatus == "" {
			recordStatus = "pending"
		}
		switch {
		case strings.Contains(recordType, "spf"):
			spf = recordStatus
		case strings.Contains(recordType, "dkim") || strings.Contains(recordType, "_domainkey"):
			dkim = recordStatus
		case strings.Contains(recordType, "dmarc"):
			dmarc = recordStatus
		}
	}
	if strings.EqualFold(domain.Status, "verified") {
		status, spf, dkim, dmarc, verification = "verified", "verified", "verified", "verified", "verified"
	} else if strings.EqualFold(domain.Status, "failed") {
		status, verification = "misconfigured", "mismatched"
	}
	return
}

func domainDNSRecordsFor(domain sendingDomain, verification, trackingBase, spfInclude string) []dnsRecord {
	if domain.Provider != delivery.ProviderResend {
		return dnsRecordsFor(domain, verification, trackingBase, spfInclude)
	}
	records := make([]dnsRecord, 0, len(domain.ResendDNSRecords))
	for _, record := range domain.ResendDNSRecords {
		records = append(records, dnsRecord{
			Type: record.Type, Name: record.Name, Value: record.Value,
			Purpose: "DNS record required by Resend for domain authentication.",
			Status:  record.Status,
		})
	}
	return records
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
			Value:   "v=sendwise-verification " + d.VerificationToken,
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

func (s *Server) checkDomainDNS(ctx context.Context, domain, selector, expectedPublicKey, expectedVerificationToken string) (spf, dkim, dmarc, verification string) {
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

	expectedVerificationToken = strings.ToLower(strings.TrimSpace(expectedVerificationToken))
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
			if candidate == expectedVerificationToken && expectedVerificationToken != "" {
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
