package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/httpx"
)

func (s *Server) previewTemplate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Design json.RawMessage `json:"design"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if len(input.Design) == 0 || !json.Valid(input.Design) {
		httpx.Error(w, http.StatusBadRequest, "invalid_design", "Provide a valid template design to preview.")
		return
	}
	html, text, err := delivery.NewRenderer().RenderDesign(input.Design, map[string]string{
		"first_name":           "Kayode",
		"last_name":            "Preview",
		"email":                "preview@example.test",
		"organization":         "Your workspace",
		"app_base_url":         strings.TrimRight(s.cfg.AppBaseURL, "/"),
		"unsubscribe_url":      strings.TrimRight(s.cfg.AppBaseURL, "/") + "/unsubscribe-preview",
		"sender_company":       "Your company",
		"sender_website":       "https://example.test",
		"sender_support_email": "support@example.test",
		"sender_social_links":  "[]",
	})
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_design", "The template could not be rendered.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"html": html, "text": text})
}
