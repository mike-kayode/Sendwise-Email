package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/httpx"
)

type subscribeInput struct {
	Email          string `json:"email"`
	OrganizationID string `json:"organization_id"`
	ListID         string `json:"list_id"`
	Source         string `json:"source"`
}

func (s *Server) publicSubscribe(w http.ResponseWriter, r *http.Request) {
	var input subscribeInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}

	email, err := normalizeEmail(input.Email)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}

	orgID := strings.TrimSpace(input.OrganizationID)
	listID := strings.TrimSpace(input.ListID)

	if orgID == "" && listID != "" {
		_ = s.pool.QueryRow(r.Context(), `
			SELECT organization_id FROM contact_lists WHERE id = $1
		`, listID).Scan(&orgID)
	}
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_organization", "organization_id or list_id is required.")
		return
	}

	var exists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1 AND status = 'active')
	`, orgID).Scan(&exists); err != nil || !exists {
		httpx.Error(w, http.StatusNotFound, "form_not_found", "This subscription form is not available.")
		return
	}

	var suppressed bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM suppressions
			WHERE organization_id = $1 AND lower(email) = $2
		)
	`, orgID, email).Scan(&suppressed); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process the subscription.")
		return
	}
	if suppressed {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "subscribed"})
		return
	}

	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "public_form"
	}
	if len(source) > 128 {
		httpx.Error(w, http.StatusBadRequest, "invalid_source", "The subscription source is too long.")
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process the subscription.")
		return
	}
	defer tx.Rollback(r.Context())

	var contactID, status string
	err = tx.QueryRow(r.Context(), `
		INSERT INTO contacts (organization_id, email, status, source)
		VALUES ($1, $2, 'subscribed', $3)
		ON CONFLICT (organization_id, lower(email)) DO UPDATE
		SET status = CASE
				WHEN contacts.status IN ('complained', 'suppressed', 'unsubscribed', 'bounced') THEN contacts.status
				ELSE 'subscribed'
			END,
			updated_at = now()
		RETURNING id, status
	`, orgID, email, source).Scan(&contactID, &status)
	if err != nil {
		s.logger.Error("public subscribe", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process the subscription.")
		return
	}

	if status == "subscribed" {
		evidence, err := json.Marshal(map[string]string{
			"method":          "public_form",
			"organization_id": orgID,
			"list_id":         listID,
		})
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record subscription consent.")
			return
		}
		consentSource, metadata, err := (&contactConsentInput{
			Source:   source,
			Purpose:  "marketing",
			Evidence: evidence,
		}).validated()
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record subscription consent.")
			return
		}
		if err := recordSubscribeConsent(r.Context(), tx, r, orgID, contactID, consentSource, metadata); err != nil {
			s.logger.Error("record public subscribe consent", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record subscription consent.")
			return
		}

		if listID != "" {
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO contact_list_members (organization_id, contact_id, list_id)
				SELECT $1, $2, id FROM contact_lists WHERE id = $3 AND organization_id = $1
				ON CONFLICT (contact_id, list_id) DO NOTHING
			`, orgID, contactID, listID); err != nil {
				s.logger.Error("add subscribed contact to list", "error", err)
				httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process the subscription.")
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit public subscribe", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process the subscription.")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]string{"status": "subscribed"})
}

func (s *Server) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	ok, message := s.unsubscribe(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Email preferences</title></head>
<body style="margin:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;background:#f8fafc;color:#0f172a;">
<main style="max-width:520px;margin:80px auto;padding:32px;background:#fff;border:1px solid #e2e8f0;border-radius:12px;">
<h1 style="margin:0 0 12px;font-size:24px;">` + map[bool]string{true: "You are unsubscribed", false: "Could not unsubscribe"}[ok] + `</h1>
<p style="margin:0;color:#475569;line-height:1.6;">` + message + `</p>
</main></body></html>`))
}

func (s *Server) unsubscribeAPI(w http.ResponseWriter, r *http.Request) {
	ok, message := s.unsubscribe(r)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "invalid_unsubscribe", message)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}

func (s *Server) unsubscribe(r *http.Request) (bool, string) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		_ = r.ParseForm()
		token = strings.TrimSpace(r.FormValue("token"))
	}
	if token == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
		token = strings.TrimSpace(body.Token)
	}
	if token == "" {
		return false, "The unsubscribe link is missing a token."
	}

	claims, err := s.tokens.Parse(token)
	if err != nil || claims.ContactID == "" || claims.OrganizationID == "" {
		return false, "This unsubscribe link is invalid or has expired."
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}
	defer tx.Rollback(r.Context())

	var email, currentStatus string
	err = tx.QueryRow(r.Context(), `
		SELECT email, status FROM contacts
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE
	`, claims.ContactID, claims.OrganizationID).Scan(&email, &currentStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.logger.Error("load contact for unsubscribe", "error", err)
		return false, "We could not process the unsubscribe request. Please try again."
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return true, "This contact is no longer on the list."
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE contacts SET status = 'unsubscribed', updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, claims.ContactID, claims.OrganizationID); err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, $2, 'unsubscribe', 'unsubscribe_link')
		ON CONFLICT DO NOTHING
	`, claims.OrganizationID, email); err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}

	if _, err := tx.Exec(r.Context(), `
		INSERT INTO consent_events (organization_id, contact_id, event_type, source, ip_address, user_agent)
		VALUES ($1, $2, 'unsubscribe', 'unsubscribe_link', $3, $4)
	`, claims.OrganizationID, claims.ContactID, clientIP(r), r.UserAgent()); err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE delivery_jobs
		SET status = 'cancelled', last_error = 'unsubscribed', updated_at = now()
		WHERE organization_id = $1 AND contact_id = $2
		  AND status IN ('queued', 'processing', 'deferred')
	`, claims.OrganizationID, claims.ContactID); err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE campaign_recipients
		SET status = 'unsubscribed'
		WHERE organization_id = $1 AND contact_id = $2 AND status = 'pending'
	`, claims.OrganizationID, claims.ContactID); err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}

	if err := tx.Commit(r.Context()); err != nil {
		return false, "We could not process the unsubscribe request. Please try again."
	}

	return true, "You will no longer receive marketing emails from this sender."
}

func (s *Server) trackOpen(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSuffix(r.PathValue("token"), ".gif")
	claims, err := s.tokens.Parse(token)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_tracking_token", "Invalid tracking token.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, user_agent, ip_address)
		VALUES ($1, $2, $3, $4, 'opened', $5, $6)
	`, claims.OrganizationID, nullIfEmpty(claims.CampaignID), nullIfEmpty(claims.ContactID),
		nullIfEmpty(claims.Subject), r.UserAgent(), clientIP(r),
	); err != nil {
		s.logger.Warn("record open event", "error", err)
	}

	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	_, _ = w.Write(delivery.TinyPixel())
}

func (s *Server) trackClick(w http.ResponseWriter, r *http.Request) {
	claims, err := s.tokens.Parse(r.PathValue("token"))
	if err != nil || claims.URL == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_tracking_token", "Invalid tracking token.")
		return
	}

	target, err := url.Parse(claims.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_target", "Invalid redirect target.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if _, err := s.pool.Exec(ctx, `
		INSERT INTO message_events
			(organization_id, campaign_id, contact_id, delivery_job_id, event_type, url, user_agent, ip_address)
		VALUES ($1, $2, $3, $4, 'clicked', $5, $6, $7)
	`, claims.OrganizationID, nullIfEmpty(claims.CampaignID), nullIfEmpty(claims.ContactID),
		nullIfEmpty(claims.Subject), claims.URL, r.UserAgent(), clientIP(r),
	); err != nil {
		s.logger.Warn("record click event", "error", err)
	}

	http.Redirect(w, r, claims.URL, http.StatusFound)
}

func clientIP(r *http.Request) any {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
		if net.ParseIP(first) != nil {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if net.ParseIP(host) != nil {
		return host
	}
	return nil
}
