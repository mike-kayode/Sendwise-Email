package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

const domainColumns = `
	id, domain, status, dkim_selector, spf_status, dkim_status, dmarc_status,
	last_checked_at, verified_at, created_at
`

type sendingDomain struct {
	ID            string     `json:"id"`
	Domain        string     `json:"domain"`
	Status        string     `json:"status"`
	DKIMSelector  string     `json:"dkim_selector"`
	SPFStatus     string     `json:"spf_status"`
	DKIMStatus    string     `json:"dkim_status"`
	DMARCStatus   string     `json:"dmarc_status"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	VerifiedAt    *time.Time `json:"verified_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func scanDomain(row pgx.Row) (*sendingDomain, error) {
	var d sendingDomain
	if err := row.Scan(
		&d.ID, &d.Domain, &d.Status, &d.DKIMSelector,
		&d.SPFStatus, &d.DKIMStatus, &d.DMARCStatus,
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

	d, err := scanDomain(s.pool.QueryRow(r.Context(), `
		INSERT INTO sending_domains (organization_id, domain, dkim_selector)
		VALUES ($1, $2, $3)
		RETURNING `+domainColumns,
		s.organizationID(r), domain, s.cfg.DKIMSelector,
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
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sending domain.")
		return
	}

	spf, dkim, dmarc := checkDomainDNS(r.Context(), d.Domain, d.DKIMSelector)
	status := "pending"
	switch {
	case spf == "verified" && dkim == "verified" && dmarc == "verified":
		status = "verified"
	case spf == "verified" || dkim == "verified" || dmarc == "verified":
		status = "misconfigured"
	}

	updated, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET status = $3,
		    spf_status = $4,
		    dkim_status = $5,
		    dmarc_status = $6,
		    last_checked_at = now(),
		    verified_at = CASE WHEN $3 = 'verified' THEN COALESCE(verified_at, now()) ELSE verified_at END,
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		d.ID, s.organizationID(r), status, spf, dkim, dmarc,
	))
	if err != nil {
		s.logger.Error("update domain verification", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update domain verification.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"domain":      updated,
		"dns_records": dnsRecordsFor(*updated),
	})
}

type dkimKeyInput struct {
	DKIMPublicKey string `json:"dkim_public_key"`
}

// updateDomainDKIM stores the DKIM public key for a sending domain. The public
// key is obtained from the SMTP relay (Stalwart) after key generation.
func (s *Server) updateDomainDKIM(w http.ResponseWriter, r *http.Request) {
	var input dkimKeyInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if strings.TrimSpace(input.DKIMPublicKey) == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "A DKIM public key is required.")
		return
	}

	updated, err := scanDomain(s.pool.QueryRow(r.Context(), `
		UPDATE sending_domains
		SET dkim_public_key = $3,
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+domainColumns,
		r.PathValue("id"), s.organizationID(r), input.DKIMPublicKey,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("update domain DKIM key", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the DKIM public key.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"domain": updated})
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
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sending domain.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"domain":      d,
		"dns_records": dnsRecordsFor(*d),
	})
}

type dnsRecord struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	Purpose string `json:"purpose"`
	Status  string `json:"status"`
}

func dnsRecordsFor(d sendingDomain) []dnsRecord {
	dkimValue := "v=DKIM1; k=rsa; p=<PUBLIC_KEY_FROM_STALWART>"
	return []dnsRecord{
		{
			Type:    "TXT",
			Name:    d.Domain,
			Value:   "v=spf1 mx ~all",
			Purpose: "SPF authorizes your sending infrastructure to send for this domain.",
			Status:  d.SPFStatus,
		},
		{
			Type:    "TXT",
			Name:    d.DKIMSelector + "._domainkey." + d.Domain,
			Value:   dkimValue,
			Purpose: "DKIM cryptographically signs outbound messages. Copy the public key from Stalwart.",
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
			Type:    "CNAME",
			Name:    "track." + d.Domain,
			Value:   "track.yourplatform.com",
			Purpose: "Optional branded click-tracking domain.",
			Status:  "pending",
		},
	}
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

func checkDomainDNS(ctx context.Context, domain, selector string) (spf, dkim, dmarc string) {
	_ = ctx
	spf = "missing"
	for _, txt := range lookupTXT(domain) {
		if strings.HasPrefix(strings.ToLower(txt), "v=spf1") {
			spf = "verified"
			break
		}
	}

	dkim = "missing"
	for _, txt := range lookupTXT(selector + "._domainkey." + domain) {
		lower := strings.ToLower(txt)
		if strings.Contains(lower, "v=dkim1") && strings.Contains(lower, "p=") {
			dkim = "verified"
			break
		}
	}

	dmarc = "missing"
	for _, txt := range lookupTXT("_dmarc." + domain) {
		if strings.HasPrefix(strings.ToLower(txt), "v=dmarc1") {
			dmarc = "verified"
			break
		}
	}
	return spf, dkim, dmarc
}

func lookupTXT(name string) []string {
	values, err := net.LookupTXT(name)
	if err != nil {
		return nil
	}
	return values
}
