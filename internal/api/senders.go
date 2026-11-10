package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

var senderLocalPartPattern = regexp.MustCompile(`^[a-z0-9._+-]{1,64}$`)

var (
	errSenderDomainMissing     = errors.New("add this domain to the workspace before using the address")
	errSenderDomainConflict    = errors.New("Domain already owned by another workspace.")
	errSenderDomainUnverified  = errors.New("verify this sending domain before using the address")
	errSenderIdentityMissing   = errors.New("sender identity does not belong to this workspace")
	errSenderIdentityMismatch  = errors.New("campaign From address does not match the selected sender identity")
	errSenderProfileIncomplete = errors.New("complete the sender profile before scheduling or sending campaigns")
)

type senderIdentity struct {
	ID          string    `json:"id"`
	Domain      string    `json:"domain"`
	LocalPart   string    `json:"local_part"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	ReplyTo     string    `json:"reply_to"`
	IsDefault   bool      `json:"is_default"`
	Verified    bool      `json:"verified"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Server) listSenderIdentities(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT si.id, si.domain, si.local_part, si.display_name, si.reply_to,
		       si.is_default, COALESCE(sd.status = 'verified' AND NOT sd.owner_conflict, false),
		       si.created_at, si.updated_at
		FROM sender_identities si
		LEFT JOIN sending_domains sd
		  ON sd.organization_id = si.organization_id AND lower(sd.domain) = lower(si.domain)
		WHERE si.organization_id = $1
		ORDER BY si.is_default DESC, lower(si.domain), lower(si.local_part)
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list sender identities", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not load sender identities.")
		return
	}
	defer rows.Close()
	items := make([]senderIdentity, 0)
	for rows.Next() {
		var item senderIdentity
		if err := rows.Scan(
			&item.ID, &item.Domain, &item.LocalPart, &item.DisplayName, &item.ReplyTo,
			&item.IsDefault, &item.Verified, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			s.logger.Error("scan sender identity", "error", err)
			httpx.Error(w, 500, "internal_error", "Could not load sender identities.")
			return
		}
		item.Email = item.LocalPart + "@" + item.Domain
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate sender identities", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not load sender identities.")
		return
	}
	httpx.JSON(w, 200, map[string]any{"data": items})
}

type senderIdentityInput struct {
	Domain      string `json:"domain"`
	LocalPart   string `json:"local_part"`
	DisplayName string `json:"display_name"`
	ReplyTo     string `json:"reply_to"`
	IsDefault   bool   `json:"is_default"`
}

func normalizeSenderIdentityInput(input *senderIdentityInput) (string, error) {
	domain, err := normalizeDomain(input.Domain)
	if err != nil {
		return "", errors.New("enter a valid sending domain")
	}
	input.Domain = domain
	input.LocalPart = strings.ToLower(strings.TrimSpace(input.LocalPart))
	if !senderLocalPartPattern.MatchString(input.LocalPart) {
		return "", errors.New("local part must be 1–64 lowercase letters, digits, dots, underscores, plus signs, or hyphens")
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return "", errors.New("display name is required")
	}
	input.ReplyTo = strings.TrimSpace(input.ReplyTo)
	if input.ReplyTo != "" {
		parsed, err := mail.ParseAddress(input.ReplyTo)
		if err != nil || parsed.Address == "" || strings.ContainsAny(input.ReplyTo, "\r\n") {
			return "", errors.New("enter a valid reply-to email address")
		}
		input.ReplyTo = parsed.String()
	}
	return domain, nil
}

func (s *Server) createSenderIdentity(w http.ResponseWriter, r *http.Request) {
	var input senderIdentityInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, 400, "invalid_body", "The request body is invalid.")
		return
	}
	if _, err := normalizeSenderIdentityInput(&input); err != nil {
		httpx.Error(w, 400, "invalid_sender_identity", err.Error())
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin sender identity create", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not create sender identity.")
		return
	}
	defer tx.Rollback(ctx)

	if err := requireVerifiedOwnedDomain(ctx, tx, s.organizationID(r), input.Domain); err != nil {
		s.writeSenderDomainError(w, err)
		return
	}
	var hasDefault bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sender_identities WHERE organization_id = $1 AND is_default
		)
	`, s.organizationID(r)).Scan(&hasDefault); err != nil {
		s.logger.Error("check default sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not create sender identity.")
		return
	}
	input.IsDefault = input.IsDefault || !hasDefault
	if input.IsDefault {
		if _, err := tx.Exec(ctx, `
			UPDATE sender_identities SET is_default = false, updated_at = now()
			WHERE organization_id = $1 AND is_default
		`, s.organizationID(r)); err != nil {
			s.logger.Error("clear default sender identity", "error", err)
			httpx.Error(w, 500, "internal_error", "Could not create sender identity.")
			return
		}
	}
	item, err := scanSenderIdentity(tx.QueryRow(ctx, `
		INSERT INTO sender_identities
			(organization_id, domain, local_part, display_name, reply_to, is_default)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, domain, local_part, display_name, reply_to, is_default, true, created_at, updated_at
	`, s.organizationID(r), input.Domain, input.LocalPart, input.DisplayName, input.ReplyTo, input.IsDefault))
	if isUniqueViolation(err) {
		httpx.Error(w, 409, "sender_identity_exists", "A sender identity already exists for this address.")
		return
	}
	if err != nil {
		s.logger.Error("create sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not create sender identity.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit sender identity create", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not create sender identity.")
		return
	}
	httpx.JSON(w, 201, item)
}

func (s *Server) updateSenderIdentity(w http.ResponseWriter, r *http.Request) {
	var input senderIdentityInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, 400, "invalid_body", "The request body is invalid.")
		return
	}
	if _, err := normalizeSenderIdentityInput(&input); err != nil {
		httpx.Error(w, 400, "invalid_sender_identity", err.Error())
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin sender identity update", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not update sender identity.")
		return
	}
	defer tx.Rollback(ctx)
	if err := requireVerifiedOwnedDomain(ctx, tx, s.organizationID(r), input.Domain); err != nil {
		s.writeSenderDomainError(w, err)
		return
	}
	if input.IsDefault {
		if _, err := tx.Exec(ctx, `
			UPDATE sender_identities SET is_default = false, updated_at = now()
			WHERE organization_id = $1 AND is_default
		`, s.organizationID(r)); err != nil {
			s.logger.Error("clear default sender identity", "error", err)
			httpx.Error(w, 500, "internal_error", "Could not update sender identity.")
			return
		}
	}
	item, err := scanSenderIdentity(tx.QueryRow(ctx, `
		UPDATE sender_identities
		SET domain = $3, local_part = $4, display_name = $5, reply_to = $6,
		    is_default = $7, updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING id, domain, local_part, display_name, reply_to, is_default, true, created_at, updated_at
	`, r.PathValue("id"), s.organizationID(r), input.Domain, input.LocalPart, input.DisplayName, input.ReplyTo, input.IsDefault))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, 404, "sender_identity_not_found", "The sender identity could not be found.")
		return
	}
	if isUniqueViolation(err) {
		httpx.Error(w, 409, "sender_identity_exists", "A sender identity already exists for this address.")
		return
	}
	if err != nil {
		s.logger.Error("update sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not update sender identity.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit sender identity update", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not update sender identity.")
		return
	}
	httpx.JSON(w, 200, item)
}

func (s *Server) deleteSenderIdentity(w http.ResponseWriter, r *http.Request) {
	result, err := s.pool.Exec(r.Context(), `
		DELETE FROM sender_identities WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		s.logger.Error("delete sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not delete sender identity.")
		return
	}
	if result.RowsAffected() == 0 {
		httpx.Error(w, 404, "sender_identity_not_found", "The sender identity could not be found.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) setDefaultSenderIdentity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("begin default sender update", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not set the default sender.")
		return
	}
	defer tx.Rollback(ctx)
	var domain string
	if err := tx.QueryRow(ctx, `
		SELECT domain FROM sender_identities WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)).Scan(&domain); errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, 404, "sender_identity_not_found", "The sender identity could not be found.")
		return
	} else if err != nil {
		s.logger.Error("load default sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not set the default sender.")
		return
	}
	if err := requireVerifiedOwnedDomain(ctx, tx, s.organizationID(r), domain); err != nil {
		s.writeSenderDomainError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE sender_identities SET is_default = false, updated_at = now()
		WHERE organization_id = $1 AND is_default
	`, s.organizationID(r)); err != nil {
		s.logger.Error("clear existing default sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not set the default sender.")
		return
	}
	item, err := scanSenderIdentity(tx.QueryRow(ctx, `
		UPDATE sender_identities SET is_default = true, updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING id, domain, local_part, display_name, reply_to, is_default, true, created_at, updated_at
	`, r.PathValue("id"), s.organizationID(r)))
	if err != nil {
		s.logger.Error("set default sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not set the default sender.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("commit default sender identity", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not set the default sender.")
		return
	}
	httpx.JSON(w, 200, item)
}

func (s *Server) writeSenderDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSenderDomainMissing),
		errors.Is(err, errSenderDomainConflict),
		errors.Is(err, errSenderDomainUnverified):
		httpx.Error(w, 409, "sending_domain_not_verified", err.Error())
	default:
		s.logger.Error("check sender identity domain", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not verify sending-domain ownership.")
	}
}

func (s *Server) loadVerifiedSenderIdentity(ctx context.Context, id, organizationID string) (*senderIdentity, error) {
	var item senderIdentity
	var domainFound, ownerConflict bool
	err := s.pool.QueryRow(ctx, `
		SELECT si.id, si.domain, si.local_part, si.display_name, si.reply_to,
		       si.is_default, COALESCE(sd.status = 'verified', false), sd.id IS NOT NULL,
		       COALESCE(sd.owner_conflict, false),
		       si.created_at, si.updated_at
		FROM sender_identities si
		LEFT JOIN sending_domains sd
		  ON sd.organization_id = si.organization_id AND lower(sd.domain) = lower(si.domain)
		WHERE si.id = $1 AND si.organization_id = $2
	`, id, organizationID).Scan(
		&item.ID, &item.Domain, &item.LocalPart, &item.DisplayName, &item.ReplyTo,
		&item.IsDefault, &item.Verified, &domainFound, &ownerConflict, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSenderIdentityMissing
	}
	if err != nil {
		return nil, err
	}
	item.Email = item.LocalPart + "@" + item.Domain
	if ownerConflict {
		return nil, errSenderDomainConflict
	}
	if !domainFound {
		return nil, errSenderDomainMissing
	}
	if !item.Verified {
		return nil, errSenderDomainUnverified
	}
	return &item, nil
}

func (s *Server) writeSenderIdentityLookupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSenderIdentityMissing):
		httpx.Error(w, http.StatusBadRequest, "sender_identity_required", "Choose a sender identity that belongs to this workspace.")
	case errors.Is(err, errSenderDomainUnverified),
		errors.Is(err, errSenderDomainConflict),
		errors.Is(err, errSenderDomainMissing):
		s.writeSenderDomainError(w, err)
	case errors.Is(err, errSenderIdentityMismatch):
		httpx.Error(w, http.StatusBadRequest, "sender_identity_mismatch", err.Error())
	case errors.Is(err, errSenderProfileIncomplete):
		httpx.Error(w, http.StatusBadRequest, "sender_profile_incomplete", err.Error())
	default:
		s.logger.Error("load verified sender identity", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not verify the sender identity.")
	}
}

func scanSenderIdentity(row pgx.Row) (*senderIdentity, error) {
	var item senderIdentity
	if err := row.Scan(
		&item.ID, &item.Domain, &item.LocalPart, &item.DisplayName, &item.ReplyTo,
		&item.IsDefault, &item.Verified, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.Email = item.LocalPart + "@" + item.Domain
	return &item, nil
}

func requireVerifiedOwnedDomain(ctx context.Context, tx pgx.Tx, organizationID, domain string) error {
	var status string
	var ownerConflict bool
	err := tx.QueryRow(ctx, `
		SELECT status, owner_conflict
		FROM sending_domains
		WHERE organization_id = $1 AND lower(domain) = lower($2)
	`, organizationID, domain).Scan(&status, &ownerConflict)
	if errors.Is(err, pgx.ErrNoRows) {
		return errSenderDomainMissing
	}
	if err != nil {
		return err
	}
	if ownerConflict {
		return errSenderDomainConflict
	}
	if status != "verified" {
		return errSenderDomainUnverified
	}
	return nil
}

type senderProfile struct {
	CompanyName  string          `json:"company_name"`
	Street       string          `json:"street_address"`
	City         string          `json:"city"`
	Region       string          `json:"region"`
	PostalCode   string          `json:"postal_code"`
	Country      string          `json:"country"`
	Website      string          `json:"website"`
	SupportEmail string          `json:"support_email"`
	FooterText   string          `json:"footer_text"`
	SocialLinks  json.RawMessage `json:"social_links"`
	Complete     bool            `json:"complete"`
	UpdatedAt    *time.Time      `json:"updated_at,omitempty"`
}

func (s *Server) getSenderProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := loadSenderProfile(r.Context(), s.pool, s.organizationID(r))
	if err != nil {
		s.logger.Error("load sender profile", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not load sender profile.")
		return
	}
	httpx.JSON(w, 200, profile)
}

func (s *Server) putSenderProfile(w http.ResponseWriter, r *http.Request) {
	var profile senderProfile
	if err := httpx.Decode(w, r, &profile); err != nil {
		httpx.Error(w, 400, "invalid_body", "The request body is invalid.")
		return
	}
	profile.CompanyName = strings.TrimSpace(profile.CompanyName)
	profile.Street = strings.TrimSpace(profile.Street)
	profile.City = strings.TrimSpace(profile.City)
	profile.Region = strings.TrimSpace(profile.Region)
	profile.PostalCode = strings.TrimSpace(profile.PostalCode)
	profile.Country = strings.TrimSpace(profile.Country)
	profile.Website = strings.TrimSpace(profile.Website)
	profile.SupportEmail = strings.TrimSpace(profile.SupportEmail)
	profile.FooterText = strings.TrimSpace(profile.FooterText)
	if len(profile.SocialLinks) > 0 {
		if !json.Valid(profile.SocialLinks) {
			httpx.Error(w, 400, "invalid_social_links", "Social links must be a JSON array.")
			return
		}
		var socialLinks []map[string]string
		if err := json.Unmarshal(profile.SocialLinks, &socialLinks); err != nil || socialLinks == nil {
			httpx.Error(w, 400, "invalid_social_links", "Social links must be a JSON array.")
			return
		}
		for _, link := range socialLinks {
			parsed, err := url.Parse(strings.TrimSpace(link["url"]))
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				httpx.Error(w, 400, "invalid_social_links", "Each social link must be an HTTP or HTTPS URL.")
				return
			}
		}
	}
	if profile.SupportEmail != "" {
		parsed, err := mail.ParseAddress(profile.SupportEmail)
		if err != nil || parsed.Address == "" {
			httpx.Error(w, 400, "invalid_support_email", "Enter a valid support email address.")
			return
		}
	}
	if profile.Website != "" {
		parsed, err := url.Parse(profile.Website)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			httpx.Error(w, 400, "invalid_website", "Enter a valid HTTP or HTTPS website URL.")
			return
		}
	}
	var socialLinks any
	if len(profile.SocialLinks) > 0 {
		socialLinks = []byte(profile.SocialLinks)
	}
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO sender_profiles (
			organization_id, company_name, street_address, city, region,
			postal_code, country, website, support_email, footer_text, social_links
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE($11::jsonb, '[]'::jsonb))
		ON CONFLICT (organization_id) DO UPDATE SET
			company_name = EXCLUDED.company_name,
			street_address = EXCLUDED.street_address,
			city = EXCLUDED.city,
			region = EXCLUDED.region,
			postal_code = EXCLUDED.postal_code,
			country = EXCLUDED.country,
			website = EXCLUDED.website,
			support_email = EXCLUDED.support_email,
			footer_text = EXCLUDED.footer_text,
			social_links = CASE
				WHEN $11::jsonb IS NULL THEN sender_profiles.social_links
				ELSE EXCLUDED.social_links
			END,
			updated_at = now()
		RETURNING social_links, updated_at
	`, s.organizationID(r), profile.CompanyName, profile.Street, profile.City, profile.Region,
		profile.PostalCode, profile.Country, profile.Website, profile.SupportEmail, profile.FooterText, socialLinks,
	).Scan(&profile.SocialLinks, &profile.UpdatedAt)
	if err != nil {
		s.logger.Error("save sender profile", "error", err)
		httpx.Error(w, 500, "internal_error", "Could not save sender profile.")
		return
	}
	profile.Complete = senderProfileComplete(profile)
	httpx.JSON(w, 200, profile)
}

func loadSenderProfile(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, organizationID string) (senderProfile, error) {
	profile := senderProfile{SocialLinks: json.RawMessage(`[]`)}
	err := queryer.QueryRow(ctx, `
		SELECT company_name, street_address, city, region, postal_code, country,
		       website, support_email, footer_text, social_links, updated_at
		FROM sender_profiles WHERE organization_id = $1
	`, organizationID).Scan(
		&profile.CompanyName, &profile.Street, &profile.City, &profile.Region, &profile.PostalCode,
		&profile.Country, &profile.Website, &profile.SupportEmail, &profile.FooterText,
		&profile.SocialLinks, &profile.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return senderProfile{}, err
	}
	profile.Complete = senderProfileComplete(profile)
	return profile, nil
}

func senderProfileComplete(profile senderProfile) bool {
	if profile.CompanyName == "" || profile.Street == "" || profile.City == "" ||
		profile.PostalCode == "" || profile.Country == "" || profile.Website == "" ||
		profile.SupportEmail == "" {
		return false
	}
	return true
}

func (s *Server) validateCampaignSender(ctx context.Context, organizationID string, c *campaign) (senderProfile, error) {
	if c.SenderIdentityID == nil || strings.TrimSpace(*c.SenderIdentityID) == "" {
		var ownerConflict bool
		err := s.pool.QueryRow(ctx, `
			SELECT COALESCE(bool_or(owner_conflict), false)
			FROM sending_domains
			WHERE organization_id = $1 AND lower(domain) = lower($2)
		`, organizationID, senderDomain(c.SenderEmail)).Scan(&ownerConflict)
		if err != nil {
			return senderProfile{}, err
		}
		if ownerConflict {
			return senderProfile{}, errSenderDomainConflict
		}
		return senderProfile{}, errSenderIdentityMissing
	}
	identity, err := s.loadVerifiedSenderIdentity(ctx, *c.SenderIdentityID, organizationID)
	if err != nil {
		return senderProfile{}, err
	}
	if !strings.EqualFold(identity.Email, strings.TrimSpace(c.SenderEmail)) {
		return senderProfile{}, errSenderIdentityMismatch
	}
	profile, err := loadSenderProfile(ctx, s.pool, organizationID)
	if err != nil {
		return senderProfile{}, err
	}
	if !senderProfileComplete(profile) {
		return senderProfile{}, errSenderProfileIncomplete
	}
	return profile, nil
}
