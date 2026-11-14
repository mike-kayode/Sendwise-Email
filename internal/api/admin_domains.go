package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

type adminSendingDomain struct {
	ID                    string          `json:"id"`
	WorkspaceID           string          `json:"workspace_id"`
	WorkspaceName         string          `json:"workspace_name"`
	WorkspaceSlug         string          `json:"workspace_slug"`
	Domain                string          `json:"domain"`
	Status                string          `json:"status"`
	SPFStatus             string          `json:"spf_status"`
	DKIMStatus            string          `json:"dkim_status"`
	DMARCStatus           string          `json:"dmarc_status"`
	VerificationStatus    string          `json:"verification_status"`
	OwnerConflict         bool            `json:"owner_conflict"`
	StalwartAuthenticated bool            `json:"stalwart_authenticated"`
	TransactionalEnabled  bool            `json:"transactional_enabled"`
	AdminDisabledAt       *time.Time      `json:"admin_disabled_at"`
	LastCheckedAt         *time.Time      `json:"last_checked_at"`
	CreatedAt             time.Time       `json:"created_at"`
	DNSCheckDetails       json.RawMessage `json:"dns_check_details,omitempty"`
}

func (s *Server) internalAdmin(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/domains") {
		s.listAllAdminDomains(w, r)
		return
	}
	s.getAdminDomain(w, r)
}

func (s *Server) listAllAdminDomains(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if len(query) > 100 || len(status) > 32 {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", "Domain filters are too long.")
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			httpx.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			httpx.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	filter := `
		WHERE ($1 = '' OR d.domain ILIKE '%' || $1 || '%'
			OR o.id::text ILIKE '%' || $1 || '%'
			OR o.name ILIKE '%' || $1 || '%' OR o.slug ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR d.status = $2)
	`
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM sending_domains d
		JOIN organizations o ON o.id = d.organization_id
	`+filter, query, status).Scan(&total); err != nil {
		s.logger.Error("count admin sending domains", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domains.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT d.id::text, d.organization_id::text, o.name, o.slug, d.domain,
		       d.status, d.spf_status, d.dkim_status, d.dmarc_status,
		       d.verification_status, d.owner_conflict, d.stalwart_authenticated,
		       d.transactional_enabled, d.admin_disabled_at, d.last_checked_at, d.created_at
		FROM sending_domains d
		JOIN organizations o ON o.id = d.organization_id
	`+filter+`
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $3 OFFSET $4
	`, query, status, limit, offset)
	if err != nil {
		s.logger.Error("list admin sending domains", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domains.")
		return
	}
	defer rows.Close()
	domains := make([]adminSendingDomain, 0, limit)
	for rows.Next() {
		var domain adminSendingDomain
		if err := rows.Scan(
			&domain.ID, &domain.WorkspaceID, &domain.WorkspaceName, &domain.WorkspaceSlug,
			&domain.Domain, &domain.Status, &domain.SPFStatus, &domain.DKIMStatus,
			&domain.DMARCStatus, &domain.VerificationStatus, &domain.OwnerConflict,
			&domain.StalwartAuthenticated, &domain.TransactionalEnabled,
			&domain.AdminDisabledAt, &domain.LastCheckedAt, &domain.CreatedAt,
		); err != nil {
			s.logger.Error("scan admin sending domain", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domains.")
			return
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate admin sending domains", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domains.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": domains,
		"pagination": paginationResponse{
			Total: total, Limit: limit, Offset: offset,
		},
	})
}

func (s *Server) getAdminDomain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var domain adminSendingDomain
	err := s.pool.QueryRow(r.Context(), `
		SELECT d.id::text, d.organization_id::text, o.name, o.slug, d.domain,
		       d.status, d.spf_status, d.dkim_status, d.dmarc_status,
		       d.verification_status, d.owner_conflict, d.stalwart_authenticated,
		       d.transactional_enabled, d.admin_disabled_at, d.last_checked_at, d.created_at,
		       COALESCE(d.dns_check_details, '{}'::jsonb)
		FROM sending_domains d
		JOIN organizations o ON o.id = d.organization_id
		WHERE d.id = $1
	`, id).Scan(
		&domain.ID, &domain.WorkspaceID, &domain.WorkspaceName, &domain.WorkspaceSlug,
		&domain.Domain, &domain.Status, &domain.SPFStatus, &domain.DKIMStatus,
		&domain.DMARCStatus, &domain.VerificationStatus, &domain.OwnerConflict,
		&domain.StalwartAuthenticated, &domain.TransactionalEnabled,
		&domain.AdminDisabledAt, &domain.LastCheckedAt, &domain.CreatedAt, &domain.DNSCheckDetails,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load admin sending domain", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load sending domain details.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": domain})
}

func (s *Server) setAdminDomainDisabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input struct {
		Disabled *bool `json:"disabled"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide a valid domain status request.")
		return
	}
	if input.Disabled == nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Specify whether the domain should be disabled.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE sending_domains
		SET admin_disabled_at = CASE
			WHEN $2 THEN COALESCE(admin_disabled_at, now())
			ELSE NULL
		END,
		updated_at = now()
		WHERE id = $1
	`, id, *input.Disabled)
	if err != nil {
		s.logger.Error("set admin sending-domain disabled state", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update sending-domain status.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	s.getAdminDomain(w, r)
}

func (s *Server) deleteAdminDomain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin admin sending-domain removal", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the sending domain.")
		return
	}
	defer tx.Rollback(r.Context())
	var organizationID, domain string
	var ownerConflict bool
	err = tx.QueryRow(r.Context(), `
		SELECT organization_id::text, domain, owner_conflict
		FROM sending_domains
		WHERE id = $1
		FOR UPDATE
	`, id).Scan(&organizationID, &domain, &ownerConflict)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load admin sending domain for removal", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the sending domain.")
		return
	}
	if ownerConflict {
		httpx.Error(w, http.StatusConflict, "domain_owner_conflict", "Resolve the sending-domain ownership conflict before removing this record.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		DELETE FROM sender_identities
		WHERE organization_id = $1 AND lower(domain) = lower($2)
	`, organizationID, domain); err != nil {
		s.logger.Error("remove admin sending-domain identities", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the sending domain.")
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM sending_domains WHERE id = $1`, id); err != nil {
		s.logger.Error("remove admin sending domain", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the sending domain.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit admin sending-domain removal", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the sending domain.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resolveAdminDomainConflict(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var input struct {
		RightfulDomainID string `json:"rightful_domain_id"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide a valid ownership resolution.")
		return
	}
	if _, err := uuid.Parse(input.RightfulDomainID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The rightful domain ID is invalid.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin domain ownership resolution", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	defer tx.Rollback(r.Context())
	var domain string
	err = tx.QueryRow(r.Context(), `
		SELECT domain FROM sending_domains WHERE id = $1
	`, id).Scan(&domain)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load domain for ownership resolution", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	rows, err := tx.Query(r.Context(), `
		SELECT id::text, organization_id::text, owner_conflict
		FROM sending_domains
		WHERE lower(domain) = lower($1)
		ORDER BY id
		FOR UPDATE
	`, domain)
	if err != nil {
		s.logger.Error("lock domain ownership candidates", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	type ownershipCandidate struct {
		ID             string `json:"id"`
		OrganizationID string `json:"workspace_id"`
		OwnerConflict  bool   `json:"owner_conflict"`
	}
	candidates := make([]ownershipCandidate, 0)
	selectedExists := false
	hasConflict := false
	for rows.Next() {
		var candidate ownershipCandidate
		if err := rows.Scan(&candidate.ID, &candidate.OrganizationID, &candidate.OwnerConflict); err != nil {
			rows.Close()
			s.logger.Error("scan domain ownership candidate", "error", err, "domain_id", id)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
			return
		}
		candidates = append(candidates, candidate)
		if candidate.ID == input.RightfulDomainID {
			selectedExists = true
		}
		if candidate.OwnerConflict {
			hasConflict = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logger.Error("iterate domain ownership candidates", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	rows.Close()
	if !selectedExists {
		httpx.Error(w, http.StatusBadRequest, "invalid_owner_selection", "The selected rightful owner must be one of this domain's records.")
		return
	}
	if !hasConflict || len(candidates) < 2 {
		httpx.Error(w, http.StatusConflict, "domain_conflict_not_found", "This domain has no ownership conflict to resolve.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE sending_domains
		SET owner_conflict = true, updated_at = now()
		WHERE lower(domain) = lower($1)
	`, domain); err != nil {
		s.logger.Error("mark domain conflict candidates", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE sending_domains
		SET owner_conflict = false, updated_at = now()
		WHERE id = $1
	`, input.RightfulDomainID); err != nil {
		s.logger.Error("select rightful domain owner", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit domain ownership resolution", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not resolve domain ownership.")
		return
	}
	r.SetPathValue("id", input.RightfulDomainID)
	s.getAdminDomain(w, r)
}

func (s *Server) recheckAdminDomain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	domain, err := scanDomain(s.pool.QueryRow(r.Context(), `
		SELECT `+domainColumns+`
		FROM sending_domains
		WHERE id = $1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "domain_not_found", "The sending domain could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load admin sending domain for verification", "error", err, "domain_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load domain verification data.")
		return
	}
	s.verifyStalwartDomain(w, r, domain, domain.OrganizationID)
}
