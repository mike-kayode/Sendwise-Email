package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

const contactColumns = `
	id, organization_id, email,
	COALESCE(first_name, ''), COALESCE(last_name, ''),
	status, custom_fields, COALESCE(source, ''),
	created_at, updated_at
`

type contact struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id,omitempty"`
	Email          string          `json:"email"`
	FirstName      string          `json:"first_name"`
	LastName       string          `json:"last_name"`
	Status         string          `json:"status"`
	CustomFields   json.RawMessage `json:"custom_fields"`
	Source         string          `json:"source,omitempty"`
	Tags           []contactTag    `json:"tags,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func scanContact(row pgx.Row) (*contact, error) {
	var c contact
	var custom []byte
	if err := row.Scan(
		&c.ID, &c.OrganizationID, &c.Email,
		&c.FirstName, &c.LastName, &c.Status,
		&custom, &c.Source, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(custom) == 0 {
		custom = []byte("{}")
	}
	c.CustomFields = json.RawMessage(custom)
	return &c, nil
}

func (s *Server) listContacts(w http.ResponseWriter, r *http.Request) {
	orgID := s.organizationID(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, offset := pagination(r)

	countQuery := `
		SELECT count(*)
		FROM contacts
		WHERE organization_id = $1
		  AND ($2 = '' OR email ILIKE '%' || $2 || '%'
		       OR COALESCE(first_name, '') ILIKE '%' || $2 || '%'
		       OR COALESCE(last_name, '') ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR status = $3)
	`
	var total int
	if err := s.pool.QueryRow(r.Context(), countQuery, orgID, search, status).Scan(&total); err != nil {
		s.logger.Error("count contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load contacts.")
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT `+listMemberContactColumns+`,
		       COALESCE((
		           SELECT jsonb_agg(jsonb_build_object('id', t.id, 'name', t.name, 'color', t.color) ORDER BY t.name)
		           FROM contact_tags ct
		           JOIN tags t ON t.id = ct.tag_id AND t.organization_id = ct.organization_id
		           WHERE ct.contact_id = c.id AND ct.organization_id = c.organization_id
		       ), '[]'::jsonb)
		FROM contacts c
		WHERE c.organization_id = $1
		  AND ($2 = '' OR email ILIKE '%' || $2 || '%'
		       OR COALESCE(first_name, '') ILIKE '%' || $2 || '%'
		       OR COALESCE(last_name, '') ILIKE '%' || $2 || '%')
		  AND ($3 = '' OR c.status = $3)
		ORDER BY c.created_at DESC
		LIMIT $4 OFFSET $5
	`, orgID, search, status, limit, offset)
	if err != nil {
		s.logger.Error("list contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load contacts.")
		return
	}
	defer rows.Close()

	contacts := make([]contact, 0, limit)
	for rows.Next() {
		c, err := scanContactWithTags(rows)
		if err != nil {
			s.logger.Error("scan contact", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load contacts.")
			return
		}
		contacts = append(contacts, *c)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load contacts.")
		return
	}

	httpx.JSON(w, http.StatusOK, listResponse[contact]{
		Data:       contacts,
		Pagination: paginationResponse{Total: total, Limit: limit, Offset: offset},
	})
}

type contactInput struct {
	Email        string               `json:"email"`
	FirstName    string               `json:"first_name"`
	LastName     string               `json:"last_name"`
	Status       string               `json:"status"`
	CustomFields json.RawMessage      `json:"custom_fields"`
	Source       string               `json:"source"`
	Consent      *contactConsentInput `json:"consent"`
}

func (s *Server) createContact(w http.ResponseWriter, r *http.Request) {
	var input contactInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}

	email, err := normalizeEmail(input.Email)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}

	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "pending"
	}
	if !validContactStatus(status) {
		httpx.Error(w, http.StatusBadRequest, "invalid_status", "The contact status is invalid.")
		return
	}
	if status != "pending" && status != "subscribed" {
		httpx.Error(w, http.StatusBadRequest, "invalid_status", "New contacts must be pending or subscribed with consent evidence.")
		return
	}
	var consentSource string
	var consentMetadata []byte
	if status == "subscribed" {
		consentSource, consentMetadata, err = input.Consent.validated()
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "consent_required", err.Error())
			return
		}
	} else if input.Consent != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_consent", "Consent evidence can only be provided when subscribing a contact.")
		return
	}

	custom := input.CustomFields
	if len(custom) == 0 {
		custom = json.RawMessage(`{}`)
	}
	if !json.Valid(custom) {
		httpx.Error(w, http.StatusBadRequest, "invalid_custom_fields", "Custom fields must be a JSON object.")
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the contact.")
		return
	}
	defer tx.Rollback(r.Context())

	if status == "subscribed" {
		var suppressed bool
		if err := tx.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM suppressions
				WHERE organization_id = $1 AND lower(email) = lower($2)
			)
		`, s.organizationID(r), email).Scan(&suppressed); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not validate contact consent.")
			return
		}
		if suppressed {
			httpx.Error(w, http.StatusConflict, "contact_suppressed", "A suppressed contact cannot be subscribed.")
			return
		}
	}

	row := tx.QueryRow(r.Context(), `
		INSERT INTO contacts (organization_id, email, first_name, last_name, status, custom_fields, source)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)
		RETURNING `+contactColumns,
		s.organizationID(r), email,
		nullIfEmpty(input.FirstName), nullIfEmpty(input.LastName),
		status, []byte(custom), nullIfEmpty(input.Source),
	)
	c, err := scanContact(row)
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "contact_exists", "A contact with this email already exists.")
		return
	}
	if err != nil {
		s.logger.Error("create contact", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the contact.")
		return
	}
	if status == "subscribed" {
		if err := recordSubscribeConsent(r.Context(), tx, r, s.organizationID(r), c.ID, consentSource, consentMetadata); err != nil {
			s.logger.Error("record contact consent", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record contact consent.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit contact create", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the contact.")
		return
	}

	httpx.JSON(w, http.StatusCreated, c)
}

func (s *Server) getContact(w http.ResponseWriter, r *http.Request) {
	c, err := scanContact(s.pool.QueryRow(r.Context(), `
		SELECT `+contactColumns+`
		FROM contacts
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "The contact could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get contact", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the contact.")
		return
	}
	httpx.JSON(w, http.StatusOK, c)
}

type contactUpdateInput struct {
	Email        *string              `json:"email"`
	FirstName    *string              `json:"first_name"`
	LastName     *string              `json:"last_name"`
	Status       *string              `json:"status"`
	CustomFields json.RawMessage      `json:"custom_fields"`
	Source       *string              `json:"source"`
	Consent      *contactConsentInput `json:"consent"`
}

func (s *Server) updateContact(w http.ResponseWriter, r *http.Request) {
	var input contactUpdateInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}

	var email any
	if input.Email != nil {
		normalized, err := normalizeEmail(*input.Email)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
			return
		}
		email = normalized
	}

	var status any
	var err error
	var consentSource string
	var consentMetadata []byte
	subscribing := false
	if input.Status != nil {
		if !validContactStatus(*input.Status) {
			httpx.Error(w, http.StatusBadRequest, "invalid_status", "The contact status is invalid.")
			return
		}
		if *input.Status != "pending" && *input.Status != "subscribed" {
			httpx.Error(w, http.StatusBadRequest, "invalid_status", "Contact status can only be set to pending or subscribed through this endpoint.")
			return
		}
		subscribing = *input.Status == "subscribed"
		if subscribing {
			consentSource, consentMetadata, err = input.Consent.validated()
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "consent_required", err.Error())
				return
			}
		} else if input.Consent != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_consent", "Consent evidence can only be provided when subscribing a contact.")
			return
		}
		status = *input.Status
	} else if input.Consent != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_consent", "A subscribed status is required with consent evidence.")
		return
	}

	var custom any
	if len(input.CustomFields) > 0 {
		if !json.Valid(input.CustomFields) {
			httpx.Error(w, http.StatusBadRequest, "invalid_custom_fields", "Custom fields must be a JSON object.")
			return
		}
		custom = []byte(input.CustomFields)
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the contact.")
		return
	}
	defer tx.Rollback(r.Context())

	if subscribing {
		var targetEmail string
		err := tx.QueryRow(r.Context(), `
			SELECT COALESCE($3, email) FROM contacts
			WHERE id = $1 AND organization_id = $2
			FOR UPDATE
		`, r.PathValue("id"), s.organizationID(r), email).Scan(&targetEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "contact_not_found", "The contact could not be found.")
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not validate contact consent.")
			return
		}
		var suppressed bool
		if err := tx.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM suppressions
				WHERE organization_id = $1 AND lower(email) = lower($2)
			)
		`, s.organizationID(r), targetEmail).Scan(&suppressed); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not validate contact consent.")
			return
		}
		if suppressed {
			httpx.Error(w, http.StatusConflict, "contact_suppressed", "A suppressed contact cannot be subscribed.")
			return
		}
	}

	c, err := scanContact(tx.QueryRow(r.Context(), `
		UPDATE contacts
		SET email = COALESCE($3, email),
		    first_name = COALESCE($4, first_name),
		    last_name = COALESCE($5, last_name),
		    status = COALESCE($6, status),
		    custom_fields = COALESCE($7::jsonb, custom_fields),
		    source = COALESCE($8, source),
		    updated_at = now()
		WHERE id = $1 AND organization_id = $2
		RETURNING `+contactColumns,
		r.PathValue("id"), s.organizationID(r),
		email, input.FirstName, input.LastName, status, custom, input.Source,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "The contact could not be found.")
		return
	}
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "contact_exists", "A contact with this email already exists.")
		return
	}
	if err != nil {
		s.logger.Error("update contact", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the contact.")
		return
	}
	if subscribing {
		if err := recordSubscribeConsent(r.Context(), tx, r, s.organizationID(r), c.ID, consentSource, consentMetadata); err != nil {
			s.logger.Error("record contact consent", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record contact consent.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit contact update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the contact.")
		return
	}

	httpx.JSON(w, http.StatusOK, c)
}

func (s *Server) deleteContact(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM contacts WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		s.logger.Error("delete contact", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the contact.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "The contact could not be found.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) exportContacts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT `+contactColumns+`
		FROM contacts
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("export contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export contacts.")
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contacts.csv"`)

	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "email", "first_name", "last_name", "status", "custom_fields", "source", "created_at"})

	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			s.logger.Error("scan exported contact", "error", err)
			return
		}
		_ = writer.Write([]string{
			c.ID, c.Email, c.FirstName, c.LastName, c.Status,
			string(c.CustomFields), c.Source, c.CreatedAt.Format(time.RFC3339),
		})
	}
	writer.Flush()
}

func validContactStatus(status string) bool {
	switch status {
	case "pending", "subscribed", "unsubscribed", "bounced", "complained", "suppressed":
		return true
	default:
		return false
	}
}

func cell(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return record[index]
}
