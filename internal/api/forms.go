package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/httpx"
)

const (
	maxFormFields       = 30
	formRateLimit       = 10
	formConfirmationTTL = 48 * time.Hour
)

var formCustomKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)
var errInvalidFormAssociation = errors.New("invalid form association")

type form struct {
	ID              string      `json:"id"`
	OrganizationID  string      `json:"organization_id"`
	Name            string      `json:"name"`
	Slug            string      `json:"slug"`
	Description     string      `json:"description"`
	Status          string      `json:"status"`
	ListID          string      `json:"list_id,omitempty"`
	SuccessMessage  string      `json:"success_message"`
	RedirectURL     string      `json:"redirect_url,omitempty"`
	ConsentText     string      `json:"consent_text"`
	SpamProtection  bool        `json:"spam_protection"`
	DoubleOptIn     bool        `json:"double_opt_in"`
	Fields          []formField `json:"fields"`
	TagIDs          []string    `json:"tag_ids"`
	SubmissionCount int         `json:"submission_count,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type formField struct {
	ID          string          `json:"id"`
	FieldType   string          `json:"field_type"`
	Label       string          `json:"label"`
	Placeholder string          `json:"placeholder,omitempty"`
	Required    bool            `json:"required"`
	Position    int             `json:"position"`
	CustomKey   string          `json:"custom_key,omitempty"`
	Options     json.RawMessage `json:"options,omitempty"`
}

type formSubmission struct {
	ID        string          `json:"id"`
	FormID    string          `json:"form_id"`
	Data      json.RawMessage `json:"data"`
	Consent   bool            `json:"consent"`
	CreatedAt time.Time       `json:"created_at"`
}

type formInput struct {
	Name           string           `json:"name"`
	Description    string           `json:"description"`
	ListID         string           `json:"list_id"`
	SuccessMessage string           `json:"success_message"`
	RedirectURL    string           `json:"redirect_url"`
	ConsentText    string           `json:"consent_text"`
	SpamProtection *bool            `json:"spam_protection"`
	DoubleOptIn    *bool            `json:"double_opt_in"`
	Fields         []formFieldInput `json:"fields"`
	TagIDs         []string         `json:"tag_ids"`
}

type formFieldInput struct {
	FieldType   string          `json:"field_type"`
	Label       string          `json:"label"`
	Placeholder string          `json:"placeholder"`
	Required    bool            `json:"required"`
	Position    int             `json:"position"`
	CustomKey   string          `json:"custom_key"`
	Options     json.RawMessage `json:"options"`
}

type formQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Server) listForms(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT f.id::text, f.organization_id::text, f.name, f.slug, f.description, f.status,
		       COALESCE(f.list_id::text, ''), f.success_message, COALESCE(f.redirect_url, ''),
		       f.consent_text, f.spam_protection, f.double_opt_in, f.created_at, f.updated_at,
		       count(fs.id)
		FROM forms f
		LEFT JOIN form_submissions fs ON fs.form_id = f.id
		WHERE f.organization_id = $1
		GROUP BY f.id
		ORDER BY f.created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list forms", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load forms.")
		return
	}
	defer rows.Close()

	forms := make([]form, 0)
	for rows.Next() {
		var item form
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.Name, &item.Slug,
			&item.Description, &item.Status, &item.ListID, &item.SuccessMessage,
			&item.RedirectURL, &item.ConsentText, &item.SpamProtection, &item.DoubleOptIn,
			&item.CreatedAt, &item.UpdatedAt, &item.SubmissionCount); err != nil {
			s.logger.Error("scan form", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load forms.")
			return
		}
		item.Fields = []formField{}
		item.TagIDs = []string{}
		forms = append(forms, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate forms", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load forms.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": forms})
}

func (s *Server) createForm(w http.ResponseWriter, r *http.Request) {
	input, fields, err := decodeFormInput(w, r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_form", err.Error())
		return
	}
	orgID := s.organizationID(r)
	if err := s.validateFormAssociations(r.Context(), orgID, input.ListID, input.TagIDs); err != nil {
		s.writeFormAssociationError(w, err)
		return
	}

	slug := slugify(input.Name)
	if slug == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_name", "Form name must contain a letter or number.")
		return
	}
	var slugExists, slugExistsInOrg bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM forms WHERE slug = $1 AND organization_id = $2)
	`, slug, orgID).Scan(&slugExistsInOrg); err != nil {
		s.logger.Error("check form slug", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
		return
	}
	if slugExistsInOrg {
		httpx.Error(w, http.StatusConflict, "form_slug_exists", "A form with this name already exists.")
		return
	}
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM forms WHERE slug = $1)
	`, slug).Scan(&slugExists); err != nil {
		s.logger.Error("check global form slug", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
		return
	}
	if slugExists {
		slug += "--org-" + strings.ReplaceAll(orgID, "-", "")
		for {
			var candidateExistsInOrg, candidateExists bool
			if err := s.pool.QueryRow(r.Context(), `
				SELECT EXISTS (SELECT 1 FROM forms WHERE slug = $1 AND organization_id = $2)
			`, slug, orgID).Scan(&candidateExistsInOrg); err != nil {
				s.logger.Error("check namespaced form slug", "error", err)
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
				return
			}
			if candidateExistsInOrg {
				httpx.Error(w, http.StatusConflict, "form_slug_exists", "A form with this name already exists.")
				return
			}
			if err := s.pool.QueryRow(r.Context(), `
				SELECT EXISTS (SELECT 1 FROM forms WHERE slug = $1)
			`, slug).Scan(&candidateExists); err != nil {
				s.logger.Error("check namespaced form slug", "error", err)
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
				return
			}
			if !candidateExists {
				break
			}
			slug += "-" + uuid.NewString()
		}
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
		return
	}
	defer tx.Rollback(r.Context())

	item := form{OrganizationID: orgID}
	err = tx.QueryRow(r.Context(), `
		INSERT INTO forms (
			organization_id, name, slug, description, list_id, success_message,
			redirect_url, consent_text, spam_protection, double_opt_in
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6, $7, $8, $9, $10)
		RETURNING id::text, name, slug, description, status, COALESCE(list_id::text, ''),
		          success_message, COALESCE(redirect_url, ''), consent_text,
		          spam_protection, double_opt_in, created_at, updated_at
	`, orgID, input.Name, slug, input.Description, input.ListID, input.SuccessMessage,
		input.RedirectURL, input.ConsentText, input.SpamProtection, input.DoubleOptIn).Scan(
		&item.ID, &item.Name, &item.Slug, &item.Description, &item.Status, &item.ListID,
		&item.SuccessMessage, &item.RedirectURL, &item.ConsentText, &item.SpamProtection,
		&item.DoubleOptIn, &item.CreatedAt, &item.UpdatedAt,
	)
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "form_slug_exists", "A form with a similar name already exists.")
		return
	}
	if err != nil {
		s.logger.Error("create form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
		return
	}
	if err := replaceFormAssociations(r.Context(), tx, item.ID, orgID, fields, input.TagIDs); err != nil {
		s.logger.Error("save form fields", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save form fields.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the form.")
		return
	}
	item.Fields = formFieldsFromInputs(fields)
	item.TagIDs = input.TagIDs
	httpx.JSON(w, http.StatusCreated, item)
}

func (s *Server) getForm(w http.ResponseWriter, r *http.Request) {
	item, err := loadForm(r.Context(), s.pool, r.PathValue("id"), s.organizationID(r))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "The form could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the form.")
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) updateForm(w http.ResponseWriter, r *http.Request) {
	input, fields, err := decodeFormInput(w, r)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_form", err.Error())
		return
	}
	orgID := s.organizationID(r)
	if err := s.validateFormAssociations(r.Context(), orgID, input.ListID, input.TagIDs); err != nil {
		s.writeFormAssociationError(w, err)
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the form.")
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `
		UPDATE forms
		SET name = $3, description = $4, list_id = NULLIF($5, '')::uuid,
		    success_message = $6, redirect_url = $7, consent_text = $8,
		    spam_protection = $9, double_opt_in = $10, updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), orgID, input.Name, input.Description, input.ListID,
		input.SuccessMessage, input.RedirectURL, input.ConsentText,
		input.SpamProtection, input.DoubleOptIn)
	if err != nil {
		s.logger.Error("update form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the form.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "The form could not be found.")
		return
	}
	if err := replaceFormAssociations(r.Context(), tx, r.PathValue("id"), orgID, fields, input.TagIDs); err != nil {
		s.logger.Error("update form fields", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save form fields.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit form update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the form.")
		return
	}
	item, err := loadForm(r.Context(), s.pool, r.PathValue("id"), orgID)
	if err != nil {
		s.logger.Error("load updated form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "The form was saved but could not be loaded.")
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) deleteForm(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM forms WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		s.logger.Error("delete form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the form.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "The form could not be found.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) publishForm(w http.ResponseWriter, r *http.Request) {
	s.setFormStatus(w, r, "published")
}

func (s *Server) unpublishForm(w http.ResponseWriter, r *http.Request) {
	s.setFormStatus(w, r, "unpublished")
}

func (s *Server) setFormStatus(w http.ResponseWriter, r *http.Request, status string) {
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE forms SET status = $3, updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r), status)
	if err != nil {
		s.logger.Error("change form status", "status", status, "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the form status.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "The form could not be found.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": status})
}

func (s *Server) listFormSubmissions(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	orgID, formID := s.organizationID(r), r.PathValue("id")
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*) FROM form_submissions
		WHERE form_id = $1 AND organization_id = $2
	`, formID, orgID).Scan(&total); err != nil {
		s.logger.Error("count form submissions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load submissions.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, form_id::text, data, consent, created_at
		FROM form_submissions
		WHERE form_id = $1 AND organization_id = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`, formID, orgID, limit, offset)
	if err != nil {
		s.logger.Error("list form submissions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load submissions.")
		return
	}
	defer rows.Close()
	submissions := make([]formSubmission, 0)
	for rows.Next() {
		var submission formSubmission
		if err := rows.Scan(&submission.ID, &submission.FormID, &submission.Data,
			&submission.Consent, &submission.CreatedAt); err != nil {
			s.logger.Error("scan form submission", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load submissions.")
			return
		}
		submissions = append(submissions, submission)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate form submissions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load submissions.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": submissions, "pagination": map[string]int{"total": total, "limit": limit, "offset": offset},
	})
}

func (s *Server) publicForm(w http.ResponseWriter, r *http.Request) {
	var formID, orgID string
	err := s.pool.QueryRow(r.Context(), `
		SELECT id::text, organization_id::text
		FROM forms
		WHERE slug = $1 AND status = 'published'
	`, r.PathValue("slug")).Scan(&formID, &orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "This form is not available.")
		return
	}
	if err != nil {
		s.logger.Error("find public form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load this form.")
		return
	}
	item, err := loadForm(r.Context(), s.pool, formID, orgID)
	if err != nil {
		s.logger.Error("load public form", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load this form.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"name": item.Name, "slug": item.Slug, "description": item.Description,
			"success_message": item.SuccessMessage, "redirect_url": item.RedirectURL,
			"consent_text": item.ConsentText, "spam_protection": item.SpamProtection,
			"double_opt_in": item.DoubleOptIn,
			"fields":        item.Fields,
		},
	})
}

type publicFormSubmissionInput struct {
	Data    map[string]string `json:"data"`
	Consent bool              `json:"consent"`
	Website string            `json:"website"`
}

func (s *Server) submitPublicForm(w http.ResponseWriter, r *http.Request) {
	var item form
	err := s.pool.QueryRow(r.Context(), `
		SELECT id::text, organization_id::text
		FROM forms
		WHERE slug = $1 AND status = 'published'
	`, r.PathValue("slug")).Scan(&item.ID, &item.OrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "This form is not available.")
		return
	}
	if err != nil {
		s.logger.Error("find form for submission", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not submit this form.")
		return
	}
	item, err = loadForm(r.Context(), s.pool, item.ID, item.OrganizationID)
	if err != nil {
		s.logger.Error("load form for submission", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not submit this form.")
		return
	}

	ip := remoteClientIP(r)
	allowed, err := s.allowFormSubmission(r.Context(), item.ID, ip)
	if err != nil {
		s.logger.Error("check form submission rate", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not submit this form.")
		return
	}
	if !allowed {
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "Too many submissions. Please try again in a minute.")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input publicFormSubmissionInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_submission", "The submission is invalid.")
		return
	}
	if item.SpamProtection && strings.TrimSpace(input.Website) != "" {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "submitted"})
		return
	}

	values := make(map[string]string, len(item.Fields))
	knownKeys := make(map[string]bool, len(item.Fields))
	var email, firstName, lastName string
	customFields := make(map[string]string)
	for _, field := range item.Fields {
		key := formFieldKey(field.FieldType, field.CustomKey)
		knownKeys[key] = true
		value := strings.TrimSpace(input.Data[key])
		if field.Required && value == "" {
			httpx.Error(w, http.StatusBadRequest, "required_field", fmt.Sprintf("%s is required.", field.Label))
			return
		}
		if value != "" && field.FieldType == "email" {
			normalized, err := normalizeEmail(value)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
				return
			}
			value, email = normalized, normalized
		}
		if field.FieldType == "first_name" {
			firstName = value
		} else if field.FieldType == "last_name" {
			lastName = value
		} else if field.FieldType != "email" && value != "" {
			customFields[key] = value
		}
		values[key] = value
	}
	if email == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_email", "A valid email address is required.")
		return
	}
	for key := range input.Data {
		if !knownKeys[key] {
			httpx.Error(w, http.StatusBadRequest, "unknown_field", "The submission contains an unknown field.")
			return
		}
	}
	if item.ConsentText != "" && !input.Consent {
		httpx.Error(w, http.StatusBadRequest, "consent_required", "Please accept the consent statement to continue.")
		return
	}

	dataJSON, err := json.Marshal(values)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save this submission.")
		return
	}
	customJSON, err := json.Marshal(customFields)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save this submission.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save this submission.")
		return
	}
	defer tx.Rollback(r.Context())

	var suppressed bool
	if err := tx.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM suppressions WHERE organization_id = $1 AND lower(email) = $2
		)
	`, item.OrganizationID, email).Scan(&suppressed); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process this submission.")
		return
	}
	var contactID, contactStatus string
	if suppressed {
		err := tx.QueryRow(r.Context(), `
			SELECT id::text FROM contacts
			WHERE organization_id = $1 AND lower(email) = $2
		`, item.OrganizationID, email).Scan(&contactID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			s.logger.Error("load suppressed form contact", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process this submission.")
			return
		}
	} else {
		targetStatus := "subscribed"
		if item.DoubleOptIn {
			targetStatus = "pending"
		}
		err = tx.QueryRow(r.Context(), `
			INSERT INTO contacts (
				organization_id, email, first_name, last_name, status, custom_fields, source
			)
			VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6::jsonb, 'signup_form')
			ON CONFLICT (organization_id, lower(email)) DO UPDATE
			SET first_name = COALESCE(NULLIF(EXCLUDED.first_name, ''), contacts.first_name),
			    last_name = COALESCE(NULLIF(EXCLUDED.last_name, ''), contacts.last_name),
			    custom_fields = contacts.custom_fields || EXCLUDED.custom_fields,
			    status = CASE
			        WHEN contacts.status IN ('unsubscribed', 'bounced', 'complained', 'suppressed')
			            THEN contacts.status
			        WHEN contacts.status = 'subscribed' THEN contacts.status
			        ELSE EXCLUDED.status
			    END,
			    updated_at = now()
			RETURNING id::text, status
		`, item.OrganizationID, email, firstName, lastName, targetStatus, string(customJSON)).Scan(&contactID, &contactStatus)
		if err != nil {
			s.logger.Error("upsert form contact", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save the contact.")
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO form_submissions (form_id, organization_id, contact_id, data, consent, ip_address, user_agent)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4::jsonb, $5, $6, $7)
	`, item.ID, item.OrganizationID, contactID, string(dataJSON), input.Consent, ip, r.UserAgent()); err != nil {
		s.logger.Error("record form submission", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save this submission.")
		return
	}

	if !suppressed {
		if input.Consent {
			metadata, err := json.Marshal(map[string]string{"form_id": item.ID, "form_slug": item.Slug})
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record consent.")
				return
			}
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO consent_events (organization_id, contact_id, event_type, source, ip_address, user_agent, metadata)
				VALUES ($1, $2, 'form', 'signup_form', $3, $4, $5::jsonb)
			`, item.OrganizationID, contactID, ip, r.UserAgent(), string(metadata)); err != nil {
				s.logger.Error("record form consent", "error", err)
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record consent.")
				return
			}
		}
		if contactStatus == "subscribed" {
			if err := addFormContactAssociations(r.Context(), tx, item.ID, item.OrganizationID, contactID); err != nil {
				s.logger.Error("apply form associations", "error", err)
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not add the contact to the form audience.")
				return
			}
		}
	}

	var token, confirmURL string
	if !suppressed && item.DoubleOptIn && contactStatus == "pending" {
		token, err = newFormConfirmationToken()
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create a confirmation link.")
			return
		}
		hash := sha256.Sum256([]byte(token))
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO form_confirmation_tokens (form_id, organization_id, contact_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, now() + ($5 * interval '1 second'))
		`, item.ID, item.OrganizationID, contactID, hex.EncodeToString(hash[:]), formConfirmationTTL.Seconds()); err != nil {
			s.logger.Error("store form confirmation token", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create a confirmation link.")
			return
		}
		confirmURL = strings.TrimRight(s.cfg.AppBaseURL, "/") + "/f/" +
			url.PathEscape(item.Slug) + "/confirm?token=" + url.QueryEscape(token)
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit form submission", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save this submission.")
		return
	}

	if confirmURL != "" {
		if err := s.sendFormConfirmation(r.Context(), email, item.Name, confirmURL); err != nil {
			s.logger.Error("send form confirmation email", "error", err)
			httpx.Error(w, http.StatusBadGateway, "confirmation_email_failed", "Your submission was saved, but we could not send the confirmation email. Please try again.")
			return
		}
		httpx.JSON(w, http.StatusAccepted, map[string]any{
			"status": "awaiting_confirmation", "message": "Check your inbox to confirm your subscription.",
		})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"status": "submitted", "success_message": item.SuccessMessage, "redirect_url": item.RedirectURL,
	})
}

func (s *Server) confirmPublicForm(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" || len(token) > 256 {
		httpx.Error(w, http.StatusBadRequest, "invalid_confirmation", "The confirmation link is invalid or expired.")
		return
	}
	hash := sha256.Sum256([]byte(token))
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not confirm the subscription.")
		return
	}
	defer tx.Rollback(r.Context())

	var formID, orgID, contactID, successMessage string
	var expiresAt time.Time
	err = tx.QueryRow(r.Context(), `
		SELECT ft.form_id::text, ft.organization_id::text, ft.contact_id::text,
		       f.success_message, ft.expires_at
		FROM form_confirmation_tokens ft
		JOIN forms f ON f.id = ft.form_id
		WHERE ft.token_hash = $1 AND ft.confirmed_at IS NULL AND f.slug = $2
		FOR UPDATE OF ft
	`, hex.EncodeToString(hash[:]), r.PathValue("slug")).Scan(&formID, &orgID, &contactID, &successMessage, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "invalid_confirmation", "The confirmation link is invalid or expired.")
		return
	}
	if err != nil {
		s.logger.Error("load form confirmation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not confirm the subscription.")
		return
	}
	if time.Now().After(expiresAt) {
		httpx.Error(w, http.StatusGone, "confirmation_expired", "The confirmation link has expired.")
		return
	}

	var status string
	if err := tx.QueryRow(r.Context(), `
		UPDATE contacts c
		SET status = 'subscribed', updated_at = now()
		WHERE c.id = $1 AND c.organization_id = $2 AND c.status = 'pending'
		  AND NOT EXISTS (
		      SELECT 1 FROM suppressions s
		      WHERE s.organization_id = c.organization_id AND lower(s.email) = lower(c.email)
		  )
		RETURNING c.status
	`, contactID, orgID).Scan(&status); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.Error("confirm form contact", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not confirm the subscription.")
		return
	}
	if status == "subscribed" {
		if err := addFormContactAssociations(r.Context(), tx, formID, orgID, contactID); err != nil {
			s.logger.Error("apply confirmed form associations", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not finish the subscription.")
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE form_confirmation_tokens SET confirmed_at = now()
		WHERE token_hash = $1 AND confirmed_at IS NULL
	`, hex.EncodeToString(hash[:])); err != nil {
		s.logger.Error("consume form confirmation token", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not confirm the subscription.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit form confirmation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not confirm the subscription.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "confirmed", "success_message": successMessage})
}

func loadForm(ctx context.Context, q formQueryer, formID, orgID string) (form, error) {
	var item form
	err := q.QueryRow(ctx, `
		SELECT id::text, organization_id::text, name, slug, description, status,
		       COALESCE(list_id::text, ''), success_message, COALESCE(redirect_url, ''),
		       consent_text, spam_protection, double_opt_in, created_at, updated_at
		FROM forms WHERE id = $1 AND organization_id = $2
	`, formID, orgID).Scan(
		&item.ID, &item.OrganizationID, &item.Name, &item.Slug, &item.Description,
		&item.Status, &item.ListID, &item.SuccessMessage, &item.RedirectURL,
		&item.ConsentText, &item.SpamProtection, &item.DoubleOptIn, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return form{}, err
	}
	item.Fields = make([]formField, 0)
	rows, err := q.Query(ctx, `
		SELECT id::text, field_type, label, placeholder, required, position,
		       COALESCE(custom_key, ''), options
		FROM form_fields WHERE form_id = $1 AND organization_id = $2
		ORDER BY position
	`, formID, orgID)
	if err != nil {
		return form{}, err
	}
	for rows.Next() {
		var field formField
		if err := rows.Scan(&field.ID, &field.FieldType, &field.Label, &field.Placeholder,
			&field.Required, &field.Position, &field.CustomKey, &field.Options); err != nil {
			rows.Close()
			return form{}, err
		}
		item.Fields = append(item.Fields, field)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return form{}, err
	}
	rows.Close()

	item.TagIDs = make([]string, 0)
	tagRows, err := q.Query(ctx, `
		SELECT tag_id::text FROM form_tags
		WHERE form_id = $1 AND organization_id = $2 ORDER BY tag_id
	`, formID, orgID)
	if err != nil {
		return form{}, err
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var tagID string
		if err := tagRows.Scan(&tagID); err != nil {
			return form{}, err
		}
		item.TagIDs = append(item.TagIDs, tagID)
	}
	return item, tagRows.Err()
}

func decodeFormInput(w http.ResponseWriter, r *http.Request) (formInput, []formFieldInput, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input formInput
	if err := httpx.Decode(w, r, &input); err != nil {
		return formInput{}, nil, errors.New("the request body is invalid")
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.SuccessMessage = strings.TrimSpace(input.SuccessMessage)
	input.ConsentText = strings.TrimSpace(input.ConsentText)
	input.RedirectURL = strings.TrimSpace(input.RedirectURL)
	if input.Name == "" || len(input.Name) > 120 {
		return formInput{}, nil, errors.New("form name is required and must be 120 characters or fewer")
	}
	if len(input.Description) > 2000 || len(input.SuccessMessage) > 1000 || len(input.ConsentText) > 1000 {
		return formInput{}, nil, errors.New("form text exceeds the allowed length")
	}
	if input.SuccessMessage == "" {
		input.SuccessMessage = "Thank you for subscribing!"
	}
	if input.ConsentText == "" {
		input.ConsentText = ""
	}
	if input.RedirectURL != "" {
		redirectURL, err := url.ParseRequestURI(input.RedirectURL)
		if err != nil || (redirectURL.Scheme != "https" && redirectURL.Scheme != "http") || redirectURL.Host == "" {
			return formInput{}, nil, errors.New("redirect URL must be an absolute HTTP or HTTPS URL")
		}
	}
	if input.SpamProtection == nil {
		enabled := true
		input.SpamProtection = &enabled
	}
	if input.DoubleOptIn == nil {
		disabled := false
		input.DoubleOptIn = &disabled
	}
	if len(input.Fields) == 0 || len(input.Fields) > maxFormFields {
		return formInput{}, nil, fmt.Errorf("a form must have between 1 and %d fields", maxFormFields)
	}
	fields := make([]formFieldInput, len(input.Fields))
	copy(fields, input.Fields)
	emailFields := 0
	seenKeys := make(map[string]bool, len(fields))
	for i := range fields {
		field := &fields[i]
		field.FieldType = strings.TrimSpace(field.FieldType)
		field.Label = strings.TrimSpace(field.Label)
		field.CustomKey = strings.TrimSpace(field.CustomKey)
		if field.Label == "" || len(field.Label) > 120 {
			return formInput{}, nil, errors.New("every field needs a label of 120 characters or fewer")
		}
		switch field.FieldType {
		case "email":
			emailFields++
		case "first_name", "last_name", "phone", "company", "country":
		case "custom":
			if !formCustomKeyPattern.MatchString(field.CustomKey) {
				return formInput{}, nil, errors.New("custom fields need a key starting with a letter and containing only letters, numbers, or underscores")
			}
		default:
			return formInput{}, nil, errors.New("field type is not supported")
		}
		if field.FieldType != "custom" {
			field.CustomKey = ""
		}
		key := formFieldKey(field.FieldType, field.CustomKey)
		if seenKeys[strings.ToLower(key)] {
			return formInput{}, nil, errors.New("field types and custom keys must be unique")
		}
		seenKeys[strings.ToLower(key)] = true
		field.Position = i
		if len(field.Placeholder) > 200 {
			return formInput{}, nil, errors.New("field placeholder is too long")
		}
		if len(field.Options) == 0 || !json.Valid(field.Options) {
			field.Options = json.RawMessage("[]")
		}
	}
	if emailFields != 1 {
		return formInput{}, nil, errors.New("a form must contain exactly one email field")
	}
	input.ListID = strings.TrimSpace(input.ListID)
	return input, fields, nil
}

func (s *Server) validateFormAssociations(ctx context.Context, orgID, listID string, tagIDs []string) error {
	if listID != "" {
		listUUID, err := uuid.Parse(listID)
		if err != nil {
			return fmt.Errorf("%w: the selected list ID is invalid", errInvalidFormAssociation)
		}
		listID = listUUID.String()
		var exists bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM contact_lists WHERE id = $1 AND organization_id = $2)
		`, listID, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("check form list ownership: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: the selected list does not belong to this organization", errInvalidFormAssociation)
		}
	}
	seen := make(map[string]bool, len(tagIDs))
	for _, tagID := range tagIDs {
		tagUUID, err := uuid.Parse(strings.TrimSpace(tagID))
		if err != nil {
			return fmt.Errorf("%w: one or more selected tags are invalid", errInvalidFormAssociation)
		}
		tagID = tagUUID.String()
		if seen[tagID] {
			return fmt.Errorf("%w: a tag may only be selected once", errInvalidFormAssociation)
		}
		seen[tagID] = true
		var exists bool
		if err := s.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM tags WHERE id = $1 AND organization_id = $2)
		`, tagID, orgID).Scan(&exists); err != nil {
			return fmt.Errorf("check form tag ownership: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: all selected tags must belong to this organization", errInvalidFormAssociation)
		}
	}
	return nil
}

func (s *Server) writeFormAssociationError(w http.ResponseWriter, err error) {
	if errors.Is(err, errInvalidFormAssociation) {
		httpx.Error(w, http.StatusBadRequest, "invalid_association", strings.TrimPrefix(err.Error(), errInvalidFormAssociation.Error()+": "))
		return
	}
	s.logger.Error("validate form associations", "error", err)
	httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not validate form associations.")
}

func replaceFormAssociations(ctx context.Context, tx pgx.Tx, formID, orgID string, fields []formFieldInput, tagIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM form_fields WHERE form_id = $1 AND organization_id = $2`, formID, orgID); err != nil {
		return err
	}
	for _, field := range fields {
		if _, err := tx.Exec(ctx, `
			INSERT INTO form_fields (
				form_id, organization_id, field_type, label, placeholder, required,
				position, custom_key, options
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9::jsonb)
		`, formID, orgID, field.FieldType, field.Label, field.Placeholder, field.Required,
			field.Position, field.CustomKey, string(field.Options)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM form_tags WHERE form_id = $1 AND organization_id = $2`, formID, orgID); err != nil {
		return err
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO form_tags (form_id, tag_id, organization_id)
			SELECT $1, id, $3 FROM tags WHERE id = $2 AND organization_id = $3
			ON CONFLICT DO NOTHING
		`, formID, strings.TrimSpace(tagID), orgID); err != nil {
			return err
		}
	}
	return nil
}

func addFormContactAssociations(ctx context.Context, tx pgx.Tx, formID, orgID, contactID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO contact_list_members (organization_id, contact_id, list_id, source)
		SELECT $1, $2, f.list_id, 'signup_form'
		FROM forms f
		WHERE f.id = $3 AND f.organization_id = $1 AND f.list_id IS NOT NULL
		ON CONFLICT (contact_id, list_id) DO NOTHING
	`, orgID, contactID, formID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO contact_tags (organization_id, contact_id, tag_id)
		SELECT $1, $2, ft.tag_id
		FROM form_tags ft
		WHERE ft.form_id = $3 AND ft.organization_id = $1
		ON CONFLICT (contact_id, tag_id) DO NOTHING
	`, orgID, contactID, formID)
	return err
}

func formFieldKey(fieldType, customKey string) string {
	if fieldType == "custom" {
		return customKey
	}
	return fieldType
}

func formFieldsFromInputs(fields []formFieldInput) []formField {
	result := make([]formField, len(fields))
	for i, field := range fields {
		result[i] = formField{
			FieldType: field.FieldType, Label: field.Label, Placeholder: field.Placeholder,
			Required: field.Required, Position: field.Position, CustomKey: field.CustomKey,
			Options: field.Options,
		}
	}
	return result
}

func (s *Server) allowFormSubmission(ctx context.Context, formID, ip string) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO form_submission_rate_limits (form_id, ip_address, window_started_at, request_count)
		VALUES ($1, $2, date_trunc('minute', now()), 1)
		ON CONFLICT (form_id, ip_address) DO UPDATE
		SET request_count = CASE
		        WHEN form_submission_rate_limits.window_started_at < date_trunc('minute', now()) THEN 1
		        ELSE form_submission_rate_limits.request_count + 1
		    END,
		    window_started_at = date_trunc('minute', now())
		RETURNING request_count
	`, formID, ip).Scan(&count)
	return count <= formRateLimit, err
}

func remoteClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	var remoteIP net.IP
	if err == nil {
		remoteIP = net.ParseIP(host)
	}
	if remoteIP == nil {
		remoteIP = net.ParseIP(r.RemoteAddr)
	}
	if remoteIP != nil && (remoteIP.IsPrivate() || remoteIP.IsLoopback()) {
		forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(forwarded) - 1; i >= 0; i-- {
			if candidate := net.ParseIP(strings.TrimSpace(forwarded[i])); candidate != nil {
				return candidate.String()
			}
		}
	}
	if remoteIP != nil {
		return remoteIP.String()
	}
	return "0.0.0.0"
}

func newFormConfirmationToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", err
	}
	return hex.EncodeToString(token), nil
}

func (s *Server) sendFormConfirmation(ctx context.Context, to, formName, confirmURL string) error {
	if s.formConfirmationSender == nil {
		return errors.New("form confirmation email provider is not configured")
	}
	return s.formConfirmationSender(ctx, to, formName, confirmURL)
}

func sendFormConfirmationEmail(ctx context.Context, cfg config.Config, to, formName, confirmURL string) error {
	if cfg.ResendAPIKey == "" || cfg.ResendFromEmail == "" {
		return errors.New("Resend API key and sender email are required for double opt-in")
	}
	var body bytes.Buffer
	if err := htmltemplate.Must(htmltemplate.New("confirmation").Parse(
		`<p>You requested to join the mailing list for {{.FormName}}.</p><p><a href="{{.URL}}">Confirm subscription</a></p><p>If you did not request this, you can ignore this message.</p>`,
	)).Execute(&body, map[string]string{"FormName": formName, "URL": confirmURL}); err != nil {
		return fmt.Errorf("render form confirmation: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"from": fmt.Sprintf("%s <%s>", cfg.ResendFromName, cfg.ResendFromEmail),
		"to":   []string{to}, "subject": "Confirm your subscription", "html": body.String(),
	})
	if err != nil {
		return fmt.Errorf("encode form confirmation email: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create form confirmation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.ResendAPIKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send form confirmation email: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("email provider returned status %d", response.StatusCode)
	}
	return nil
}
