package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/httpx"
)

const maxBulkContactIDs = 500

type bulkContactInput struct {
	ContactIDs []string             `json:"contact_ids"`
	Action     string               `json:"action"`
	Consent    *contactConsentInput `json:"consent"`
}

func (s *Server) bulkContactAction(w http.ResponseWriter, r *http.Request) {
	var input bulkContactInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if len(input.ContactIDs) == 0 || len(input.ContactIDs) > maxBulkContactIDs {
		httpx.Error(w, http.StatusBadRequest, "invalid_contact_ids", "Select between 1 and 500 contacts.")
		return
	}
	if input.Action != "delete" && input.Action != "subscribe" && input.Action != "unsubscribe" {
		httpx.Error(w, http.StatusBadRequest, "invalid_action", "Choose delete, subscribe, or unsubscribe.")
		return
	}

	contactIDs := make([]uuid.UUID, 0, len(input.ContactIDs))
	seen := make(map[uuid.UUID]struct{}, len(input.ContactIDs))
	for _, rawID := range input.ContactIDs {
		id, err := uuid.Parse(strings.TrimSpace(rawID))
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_contact_ids", "One or more selected contact IDs are invalid.")
			return
		}
		if _, exists := seen[id]; exists {
			httpx.Error(w, http.StatusBadRequest, "invalid_contact_ids", "A contact may only be selected once.")
			return
		}
		seen[id] = struct{}{}
		contactIDs = append(contactIDs, id)
	}

	consentSource := ""
	var consentMetadata []byte
	if input.Action == "subscribe" {
		var err error
		consentSource, consentMetadata, err = input.Consent.validated()
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "consent_required", err.Error())
			return
		}
	} else if input.Consent != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_consent", "Consent evidence can only be provided when subscribing contacts.")
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the selected contacts.")
		return
	}
	defer tx.Rollback(r.Context())

	if input.Action == "delete" {
		result, err := tx.Exec(r.Context(), `
			DELETE FROM contacts
			WHERE organization_id = $1 AND id = ANY($2::uuid[])
		`, s.organizationID(r), contactIDs)
		if err != nil {
			s.logger.Error("bulk delete contacts", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the selected contacts.")
			return
		}
		if int(result.RowsAffected()) != len(contactIDs) {
			httpx.Error(w, http.StatusNotFound, "contact_not_found", "One or more selected contacts could not be found.")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			s.logger.Error("commit bulk contact delete", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the selected contacts.")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]int{"affected": len(contactIDs)})
		return
	}

	rows, err := tx.Query(r.Context(), `
		SELECT id, email, status FROM contacts
		WHERE organization_id = $1 AND id = ANY($2::uuid[])
		FOR UPDATE
	`, s.organizationID(r), contactIDs)
	if err != nil {
		s.logger.Error("load selected contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the selected contacts.")
		return
	}
	type selectedContact struct {
		id     uuid.UUID
		email  string
		status string
	}
	selected := make([]selectedContact, 0, len(contactIDs))
	for rows.Next() {
		var item selectedContact
		if err := rows.Scan(&item.id, &item.email, &item.status); err != nil {
			rows.Close()
			s.logger.Error("scan selected contact", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the selected contacts.")
			return
		}
		selected = append(selected, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.logger.Error("iterate selected contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the selected contacts.")
		return
	}
	rows.Close()
	if len(selected) != len(contactIDs) {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "One or more selected contacts could not be found.")
		return
	}

	emails := make([]string, 0, len(selected))
	changedIDs := make([]uuid.UUID, 0, len(selected))
	targetStatus := "subscribed"
	if input.Action == "unsubscribe" {
		targetStatus = "unsubscribed"
	}
	for _, item := range selected {
		if item.status == "bounced" || item.status == "complained" || item.status == "suppressed" {
			httpx.Error(w, http.StatusConflict, "contact_suppressed", "Bounced, complained, or suppressed contacts cannot be changed through bulk subscription actions.")
			return
		}
		if item.status != targetStatus {
			changedIDs = append(changedIDs, item.id)
		}
		if input.Action == "subscribe" {
			emails = append(emails, item.email)
		}
	}
	if input.Action == "subscribe" {
		var suppressed bool
		if err := tx.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM suppressions
				WHERE organization_id = $1 AND lower(email) = ANY($2::text[])
			)
		`, s.organizationID(r), emails).Scan(&suppressed); err != nil {
			s.logger.Error("check bulk contact suppressions", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not validate contact consent.")
			return
		}
		if suppressed {
			httpx.Error(w, http.StatusConflict, "contact_suppressed", "A suppressed contact cannot be subscribed.")
			return
		}
	}

	if len(changedIDs) > 0 {
		if _, err := tx.Exec(r.Context(), `
			UPDATE contacts SET status = $3, updated_at = now()
			WHERE organization_id = $1 AND id = ANY($2::uuid[])
		`, s.organizationID(r), changedIDs, targetStatus); err != nil {
			s.logger.Error("bulk update contact status", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the selected contacts.")
			return
		}
		if input.Action == "subscribe" {
			for _, item := range selected {
				if item.status == "subscribed" {
					continue
				}
				if err := recordSubscribeConsent(r.Context(), tx, r, s.organizationID(r), item.id.String(), consentSource, consentMetadata); err != nil {
					s.logger.Error("record bulk contact consent", "error", err)
					httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not record subscription consent.")
					return
				}
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit bulk contact update", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the selected contacts.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]int{"affected": len(changedIDs)})
}
