package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/khayode/email-platform/internal/httpx"
)

type form struct {
	ID             string      `json:"id"`
	OrganizationID string      `json:"organization_id"`
	Name           string      `json:"name"`
	Slug           string      `json:"slug"`
	Description    string      `json:"description"`
	Status         string      `json:"status"`
	ListID         string      `json:"list_id,omitempty"`
	SuccessMessage string      `json:"success_message"`
	RedirectURL    string      `json:"redirect_url,omitempty"`
	ConsentText    string      `json:"consent_text"`
	SpamProtection bool        `json:"spam_protection"`
	DoubleOptIn    bool        `json:"double_opt_in"`
	Fields         []formField `json:"fields,omitempty"`
	TagIDs         []string    `json:"tag_ids,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
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
	Status         string           `json:"status"`
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

// listForms returns all forms for the authenticated organization.
func (s *Server) listForms(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, slug, description, status, COALESCE(list_id::text, ''), success_message,
		       COALESCE(redirect_url, ''), consent_text, spam_protection, double_opt_in,
		       created_at, updated_at
		FROM forms
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list forms", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load forms.")
		return
	}
	defer rows.Close()

	forms := make([]form, 0)
	for rows.Next() {
		var f form
		if err := rows.Scan(&f.ID, &f.Name, &f.Slug, &f.Description, &f.Status, &f.ListID,
			&f.SuccessMessage, &f.RedirectURL, &f.ConsentText, &f.SpamProtection, &f.DoubleOptIn,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			s.logger.Error("scan form", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load forms.")
			return
		}
		forms = append(forms, f)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": forms})
}

func (s *Server) createForm(w http.ResponseWriter, r *http.Request)  { httpx.NoContent(w) }
func (s *Server) getForm(w http.ResponseWriter, r *http.Request)     { httpx.NoContent(w) }
func (s *Server) updateForm(w http.ResponseWriter, r *http.Request)  { httpx.NoContent(w) }
func (s *Server) deleteForm(w http.ResponseWriter, r *http.Request)  { httpx.NoContent(w) }
func (s *Server) publishForm(w http.ResponseWriter, r *http.Request) { httpx.NoContent(w) }
