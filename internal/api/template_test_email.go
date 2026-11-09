package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/httpx"
)

type templateTestEmailInput struct {
	Recipient        string `json:"recipient"`
	SenderIdentityID string `json:"sender_identity_id"`
}

func (s *Server) sendTemplateTestEmail(w http.ResponseWriter, r *http.Request) {
	var input templateTestEmailInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Recipient = strings.TrimSpace(input.Recipient)
	recipient, err := mail.ParseAddress(input.Recipient)
	if err != nil || recipient.Address == "" || strings.ContainsAny(input.Recipient, "\r\n") {
		httpx.Error(w, http.StatusBadRequest, "invalid_recipient", "Enter a valid test recipient email address.")
		return
	}

	var name string
	var design json.RawMessage
	var templateSenderID *string
	err = s.pool.QueryRow(r.Context(), `
		SELECT name, design, sender_identity_id
		FROM templates
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)).Scan(&name, &design, &templateSenderID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "template_not_found", "The template could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load template for test email", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the template.")
		return
	}

	identityID := strings.TrimSpace(input.SenderIdentityID)
	if identityID == "" && templateSenderID != nil {
		identityID = *templateSenderID
	}
	if identityID == "" {
		var defaultIdentityID string
		err := s.pool.QueryRow(r.Context(), `
			SELECT id FROM sender_identities
			WHERE organization_id = $1 AND is_default
		`, s.organizationID(r)).Scan(&defaultIdentityID)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusBadRequest, "sender_identity_required", "Choose a verified sender identity before sending a test email.")
			return
		}
		if err != nil {
			s.logger.Error("load default sender for test email", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sender identity.")
			return
		}
		identityID = defaultIdentityID
	}

	identity, err := s.loadVerifiedSenderIdentity(r.Context(), identityID, s.organizationID(r))
	if err != nil {
		s.writeSenderIdentityLookupError(w, err)
		return
	}
	profile, err := loadSenderProfile(r.Context(), s.pool, s.organizationID(r))
	if err != nil {
		s.logger.Error("load sender profile for test email", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the sender profile.")
		return
	}
	if !senderProfileComplete(profile) {
		httpx.Error(w, http.StatusBadRequest, "sender_profile_incomplete", "Complete the sender profile before sending a test email.")
		return
	}

	var organization string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT name FROM organizations WHERE id = $1
	`, s.organizationID(r)).Scan(&organization); err != nil {
		s.logger.Error("load organization for test email", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the workspace name.")
		return
	}

	htmlBody, textBody, err := delivery.NewRenderer().RenderDesign(design, map[string]string{
		"first_name":            recipient.Name,
		"email":                 recipient.Address,
		"organization":          organization,
		"app_base_url":          strings.TrimRight(s.cfg.AppBaseURL, "/"),
		"unsubscribe_url":       strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings",
		"website":               profile.Website,
		"support_email":         profile.SupportEmail,
		"sender_company":        profile.CompanyName,
		"sender_postal_address": senderPostalAddress(profile),
	})
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_template_design", "The template could not be rendered: "+err.Error())
		return
	}
	htmlBody = appendTestComplianceFooter(htmlBody, profile)
	textBody += "\n\n" + profile.CompanyName + "\n" + senderPostalAddress(profile) + "\n" +
		profile.Website + "\n" + profile.SupportEmail

	dkim, err := delivery.LoadDKIMConfig(r.Context(), s.pool, s.organizationID(r), identity.Email, s.cfg.DKIMEncryptionKey)
	if err != nil {
		s.logger.Error("load DKIM signing key for test email", "error", err)
		httpx.Error(w, http.StatusConflict, "dkim_not_configured", "The selected sending domain does not have a usable DKIM signing key.")
		return
	}
	from := identity.Email
	if identity.DisplayName != "" {
		from = fmt.Sprintf("%s <%s>", identity.DisplayName, identity.Email)
	}
	transport := delivery.NewSMTPTransport(
		s.cfg.SMTPAddr,
		s.cfg.SMTPHost,
		s.cfg.SMTPUsername,
		s.cfg.SMTPPassword,
		s.cfg.Env == "development" || s.cfg.Env == "test",
	)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := transport.Send(ctx, delivery.Message{
		ID:      name + "-" + time.Now().UTC().Format("20060102150405"),
		From:    from,
		To:      recipient.Address,
		Subject: "[Sendwise test] " + name,
		HTML:    htmlBody,
		Text:    textBody,
		Headers: map[string]string{"X-Sendwise-Test": "true"},
		DKIM:    dkim,
	})
	if err != nil {
		s.logger.Error("send template test email", "error", err)
		httpx.Error(w, http.StatusBadGateway, "test_email_rejected", "Stalwart did not accept the test message: "+err.Error())
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{
		"status":     "accepted by SMTP relay",
		"message_id": result.MessageID,
	})
}

func senderPostalAddress(profile senderProfile) string {
	return strings.Join([]string{
		profile.Street,
		profile.City,
		profile.Region,
		profile.PostalCode,
		profile.Country,
	}, ", ")
}

func appendTestComplianceFooter(body string, profile senderProfile) string {
	footer := fmt.Sprintf(
		`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin-top:32px;border-top:1px solid #e2e8f0;"><tr><td style="padding-top:16px;text-align:center;font-family:Arial,sans-serif;font-size:12px;line-height:1.6;color:#64748b;"><strong>%s</strong><br>%s<br><a href="%s">%s</a><br>%s</td></tr></table>`,
		html.EscapeString(profile.CompanyName),
		html.EscapeString(senderPostalAddress(profile)),
		html.EscapeString(profile.Website),
		html.EscapeString(profile.Website),
		html.EscapeString(profile.SupportEmail),
	)
	if index := strings.LastIndex(strings.ToLower(body), "</body>"); index >= 0 {
		return body[:index] + footer + body[index:]
	}
	return body + footer
}
