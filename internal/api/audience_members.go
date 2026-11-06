package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khayode/email-platform/internal/httpx"
)

const listMemberContactColumns = `
	c.id, c.organization_id, c.email,
	COALESCE(c.first_name, ''), COALESCE(c.last_name, ''),
	c.status, c.custom_fields, COALESCE(c.source, ''),
	c.created_at, c.updated_at
`

func scanContactWithTags(row pgx.Row) (*contact, error) {
	var item contact
	var custom, rawTags []byte
	if err := row.Scan(
		&item.ID, &item.OrganizationID, &item.Email, &item.FirstName, &item.LastName,
		&item.Status, &custom, &item.Source, &item.CreatedAt, &item.UpdatedAt, &rawTags,
	); err != nil {
		return nil, err
	}
	if len(custom) == 0 {
		custom = []byte("{}")
	}
	if len(rawTags) == 0 {
		rawTags = []byte("[]")
	}
	item.CustomFields = json.RawMessage(custom)
	if err := json.Unmarshal(rawTags, &item.Tags); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Server) getAudienceList(w http.ResponseWriter, r *http.Request) {
	var item audienceList
	err := s.pool.QueryRow(r.Context(), `
		SELECT l.id, l.name, l.description, count(m.contact_id), l.created_at, l.updated_at
		FROM contact_lists l
		LEFT JOIN contact_list_members m ON m.list_id = l.id AND m.organization_id = l.organization_id
		WHERE l.id = $1 AND l.organization_id = $2
		GROUP BY l.id
	`, r.PathValue("id"), s.organizationID(r)).Scan(
		&item.ID, &item.Name, &item.Description, &item.ContactCount, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get list", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the list.")
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) updateAudienceList(w http.ResponseWriter, r *http.Request) {
	var input audienceListInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "List name is required.")
		return
	}
	result, err := s.pool.Exec(r.Context(), `
		UPDATE contact_lists SET name = $3, description = $4, updated_at = now()
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r), input.Name, strings.TrimSpace(input.Description))
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "list_exists", "A list with this name already exists.")
		return
	}
	if err != nil {
		s.logger.Error("update list", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the list.")
		return
	}
	if result.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) listAudienceListContacts(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	orgID, listID := s.organizationID(r), r.PathValue("id")
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM contact_list_members m
		JOIN contacts c ON c.id = m.contact_id AND c.organization_id = m.organization_id
		WHERE m.organization_id = $1 AND m.list_id = $2
	`, orgID, listID).Scan(&total); err != nil {
		if isInvalidUUID(err) {
			httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
			return
		}
		s.logger.Error("count list contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load list contacts.")
		return
	}
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM contact_lists WHERE id = $1 AND organization_id = $2)
	`, listID, orgID).Scan(&exists); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load list contacts.")
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}

	rows, err := s.pool.Query(r.Context(), `
		SELECT `+listMemberContactColumns+`
		FROM contacts c
		JOIN contact_list_members m ON m.contact_id = c.id AND m.organization_id = c.organization_id
		WHERE m.organization_id = $1 AND m.list_id = $2
		ORDER BY c.created_at DESC, c.id
		LIMIT $3 OFFSET $4
	`, orgID, listID, limit, offset)
	if err != nil {
		s.logger.Error("list contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load list contacts.")
		return
	}
	defer rows.Close()
	items := make([]contact, 0, limit)
	for rows.Next() {
		item, err := scanContact(rows)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load list contacts.")
			return
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load list contacts.")
		return
	}
	httpx.JSON(w, http.StatusOK, listResponse[contact]{
		Data: items, Pagination: paginationResponse{Total: total, Limit: limit, Offset: offset},
	})
}

type listMembershipInput struct {
	ContactIDs []string `json:"contact_ids"`
}

func (s *Server) addAudienceListContacts(w http.ResponseWriter, r *http.Request) {
	var input listMembershipInput
	if err := httpx.Decode(w, r, &input); err != nil || len(input.ContactIDs) == 0 || len(input.ContactIDs) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_contacts", "Provide between 1 and 500 contact IDs.")
		return
	}
	input.ContactIDs = uniqueStrings(input.ContactIDs)
	if len(input.ContactIDs) == 0 {
		httpx.Error(w, http.StatusBadRequest, "invalid_contacts", "Provide at least one valid contact ID.")
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update list membership.")
		return
	}
	defer tx.Rollback(ctx)

	orgID, listID := s.organizationID(r), r.PathValue("id")
	var listExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contact_lists WHERE id = $1 AND organization_id = $2)`, listID, orgID).Scan(&listExists); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_list", "The selected list is invalid.")
		return
	}
	if !listExists {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}
	var contactCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM contacts WHERE organization_id = $1 AND id = ANY($2::uuid[])
	`, orgID, input.ContactIDs).Scan(&contactCount); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_contacts", "One or more contact IDs are invalid.")
		return
	}
	if contactCount != len(input.ContactIDs) {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "One or more contacts could not be found.")
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO contact_list_members (organization_id, contact_id, list_id, source)
		SELECT $1, contact_id, $2, 'manual'
		FROM unnest($3::uuid[]) AS ids(contact_id)
		ON CONFLICT (contact_id, list_id) DO NOTHING
	`, orgID, listID, input.ContactIDs); err != nil {
		s.logger.Error("add list contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update list membership.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update list membership.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) removeAudienceListContact(w http.ResponseWriter, r *http.Request) {
	orgID, listID, contactID := s.organizationID(r), r.PathValue("id"), r.PathValue("contactID")
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM contact_lists WHERE id = $1 AND organization_id = $2)
		   AND EXISTS (SELECT 1 FROM contacts WHERE id = $3 AND organization_id = $2)
	`, listID, orgID, contactID).Scan(&exists); err != nil {
		httpx.Error(w, http.StatusNotFound, "membership_not_found", "The list membership could not be found.")
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "membership_not_found", "The list membership could not be found.")
		return
	}
	if _, err := s.pool.Exec(r.Context(), `
		DELETE FROM contact_list_members
		WHERE organization_id = $1 AND list_id = $2 AND contact_id = $3
	`, orgID, listID, contactID); err != nil {
		s.logger.Error("remove list contact", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the contact.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) exportAudienceListContacts(w http.ResponseWriter, r *http.Request) {
	orgID, listID := s.organizationID(r), r.PathValue("id")
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM contact_lists WHERE id = $1 AND organization_id = $2)
	`, listID, orgID).Scan(&exists); err != nil {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT c.email, COALESCE(c.first_name, ''), COALESCE(c.last_name, ''), c.status
		FROM contacts c
		JOIN contact_list_members m ON m.contact_id = c.id AND m.organization_id = c.organization_id
		WHERE m.organization_id = $1 AND m.list_id = $2
		ORDER BY c.email
	`, orgID, listID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export list contacts.")
		return
	}
	defer rows.Close()
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"email", "first_name", "last_name", "status"}); err != nil {
		s.logger.Error("write list CSV header", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export list contacts.")
		return
	}
	for rows.Next() {
		var record []string
		var email, firstName, lastName, status string
		if err := rows.Scan(&email, &firstName, &lastName, &status); err != nil {
			s.logger.Error("scan list CSV contact", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export list contacts.")
			return
		}
		record = []string{email, firstName, lastName, status}
		if err := writer.Write(record); err != nil {
			s.logger.Error("write list CSV contact", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export list contacts.")
			return
		}
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate list CSV contacts", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export list contacts.")
		return
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		s.logger.Error("flush list CSV", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not export list contacts.")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="list-contacts.csv"`)
	if _, err := w.Write(output.Bytes()); err != nil {
		s.logger.Error("send list CSV", "error", err)
	}
}

type contactTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *Server) listContactTags(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r)
	orgID, contactID := s.organizationID(r), r.PathValue("id")
	var contactExists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM contacts WHERE id = $1 AND organization_id = $2)
	`, contactID, orgID).Scan(&contactExists); err != nil || !contactExists {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "The contact could not be found.")
		return
	}
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*) FROM contact_tags WHERE organization_id = $1 AND contact_id = $2
	`, orgID, contactID).Scan(&total); err != nil {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "The contact could not be found.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT t.id, t.name, t.color
		FROM tags t JOIN contact_tags ct ON ct.tag_id = t.id
		WHERE ct.organization_id = $1 AND ct.contact_id = $2 AND t.organization_id = $1
		ORDER BY t.name LIMIT $3 OFFSET $4
	`, orgID, contactID, limit, offset)
	if err != nil {
		s.logger.Error("list contact tags", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load contact tags.")
		return
	}
	defer rows.Close()
	items := make([]contactTag, 0, limit)
	for rows.Next() {
		var item contactTag
		if err := rows.Scan(&item.ID, &item.Name, &item.Color); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load contact tags.")
			return
		}
		items = append(items, item)
	}
	httpx.JSON(w, http.StatusOK, listResponse[contactTag]{
		Data: items, Pagination: paginationResponse{Total: total, Limit: limit, Offset: offset},
	})
}

type bulkTagInput struct {
	TagID      string   `json:"tag_id"`
	ContactIDs []string `json:"contact_ids"`
}

func (s *Server) assignContactTag(w http.ResponseWriter, r *http.Request) {
	s.updateContactTag(w, r, r.PathValue("id"), r.PathValue("tagID"), true)
}

func (s *Server) unassignContactTag(w http.ResponseWriter, r *http.Request) {
	s.updateContactTag(w, r, r.PathValue("id"), r.PathValue("tagID"), false)
}

func (s *Server) bulkAssignContactTag(w http.ResponseWriter, r *http.Request) {
	var input bulkTagInput
	if err := httpx.Decode(w, r, &input); err != nil || strings.TrimSpace(input.TagID) == "" || len(input.ContactIDs) == 0 || len(input.ContactIDs) > 500 {
		httpx.Error(w, http.StatusBadRequest, "invalid_tag_assignment", "Provide a tag ID and between 1 and 500 contact IDs.")
		return
	}
	input.ContactIDs = uniqueStrings(input.ContactIDs)
	if len(input.ContactIDs) == 0 {
		httpx.Error(w, http.StatusBadRequest, "invalid_tag_assignment", "Provide at least one valid contact ID.")
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not assign the tag.")
		return
	}
	defer tx.Rollback(ctx)
	orgID := s.organizationID(r)
	var tagExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tags WHERE id = $1 AND organization_id = $2)`, input.TagID, orgID).Scan(&tagExists); err != nil || !tagExists {
		httpx.Error(w, http.StatusNotFound, "tag_not_found", "The tag could not be found.")
		return
	}
	var contactCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE organization_id = $1 AND id = ANY($2::uuid[])`, orgID, input.ContactIDs).Scan(&contactCount); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_contacts", "One or more contact IDs are invalid.")
		return
	}
	if contactCount != len(input.ContactIDs) {
		httpx.Error(w, http.StatusNotFound, "contact_not_found", "One or more contacts could not be found.")
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO contact_tags (organization_id, contact_id, tag_id)
		SELECT $1, contact_id, $2 FROM unnest($3::uuid[]) AS ids(contact_id)
		ON CONFLICT (contact_id, tag_id) DO NOTHING
	`, orgID, input.TagID, input.ContactIDs); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not assign the tag.")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not assign the tag.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) updateContactTag(w http.ResponseWriter, r *http.Request, contactID, tagID string, assign bool) {
	orgID := s.organizationID(r)
	var exists bool
	if err := s.pool.QueryRow(r.Context(), `
		SELECT EXISTS (SELECT 1 FROM contacts WHERE id = $1 AND organization_id = $3)
		   AND EXISTS (SELECT 1 FROM tags WHERE id = $2 AND organization_id = $3)
	`, contactID, tagID, orgID).Scan(&exists); err != nil || !exists {
		httpx.Error(w, http.StatusNotFound, "tag_or_contact_not_found", "The contact or tag could not be found.")
		return
	}
	if assign {
		_, err := s.pool.Exec(r.Context(), `
			INSERT INTO contact_tags (organization_id, contact_id, tag_id)
			VALUES ($1, $2, $3) ON CONFLICT (contact_id, tag_id) DO NOTHING
		`, orgID, contactID, tagID)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not assign the tag.")
			return
		}
	} else if _, err := s.pool.Exec(r.Context(), `
		DELETE FROM contact_tags WHERE organization_id = $1 AND contact_id = $2 AND tag_id = $3
	`, orgID, contactID, tagID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove the tag.")
		return
	}
	httpx.NoContent(w)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
