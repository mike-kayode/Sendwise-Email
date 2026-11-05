package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	segmentcontacts "github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/httpx"
)

type audienceList struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	ContactCount int       `json:"contact_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Server) listAudienceLists(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT l.id, l.name, l.description, count(m.contact_id), l.created_at, l.updated_at
		FROM contact_lists l
		LEFT JOIN contact_list_members m ON m.list_id = l.id
		WHERE l.organization_id = $1
		GROUP BY l.id
		ORDER BY l.created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list audiences", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load lists.")
		return
	}
	defer rows.Close()

	lists := make([]audienceList, 0)
	for rows.Next() {
		var l audienceList
		if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.ContactCount, &l.CreatedAt, &l.UpdatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load lists.")
			return
		}
		lists = append(lists, l)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": lists})
}

type audienceListInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) createAudienceList(w http.ResponseWriter, r *http.Request) {
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

	var l audienceList
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO contact_lists (organization_id, name, description)
		VALUES ($1, $2, $3)
		RETURNING id, name, description, created_at, updated_at
	`, s.organizationID(r), input.Name, strings.TrimSpace(input.Description)).Scan(
		&l.ID, &l.Name, &l.Description, &l.CreatedAt, &l.UpdatedAt,
	)
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "list_exists", "A list with this name already exists.")
		return
	}
	if err != nil {
		s.logger.Error("create list", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the list.")
		return
	}

	httpx.JSON(w, http.StatusCreated, l)
}

func (s *Server) deleteAudienceList(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM contact_lists WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the list.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "list_not_found", "The list could not be found.")
		return
	}
	httpx.NoContent(w)
}

type tag struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Color        string    `json:"color"`
	ContactCount int       `json:"contact_count"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT t.id, t.name, t.color, count(ct.contact_id), t.created_at
		FROM tags t
		LEFT JOIN contact_tags ct ON ct.tag_id = t.id
		WHERE t.organization_id = $1
		GROUP BY t.id
		ORDER BY t.name
	`, s.organizationID(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load tags.")
		return
	}
	defer rows.Close()

	tags := make([]tag, 0)
	for rows.Next() {
		var t tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.ContactCount, &t.CreatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load tags.")
			return
		}
		tags = append(tags, t)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": tags})
}

type tagInput struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

var tagColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	var input tagInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Tag name is required.")
		return
	}
	if input.Color == "" {
		input.Color = "#64748b"
	}
	if !tagColorPattern.MatchString(input.Color) {
		httpx.Error(w, http.StatusBadRequest, "invalid_color", "Tag color must be a six-digit hexadecimal color.")
		return
	}

	var t tag
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO tags (organization_id, name, color)
		VALUES ($1, $2, $3)
		RETURNING id, name, color, created_at
	`, s.organizationID(r), input.Name, input.Color).Scan(&t.ID, &t.Name, &t.Color, &t.CreatedAt)
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "tag_exists", "A tag with this name already exists.")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the tag.")
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
	var input tagInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Tag name is required.")
		return
	}
	if input.Color != "" && !tagColorPattern.MatchString(input.Color) {
		httpx.Error(w, http.StatusBadRequest, "invalid_color", "Tag color must be a six-digit hexadecimal color.")
		return
	}

	result, err := s.pool.Exec(r.Context(), `
		UPDATE tags
		SET name = $3, color = COALESCE(NULLIF($4, ''), color)
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r), input.Name, input.Color)
	if isUniqueViolation(err) {
		httpx.Error(w, http.StatusConflict, "tag_exists", "A tag with this name already exists.")
		return
	}
	if err != nil {
		s.logger.Error("update tag", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the tag.")
		return
	}
	if result.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "tag_not_found", "The tag could not be found.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	result, err := s.pool.Exec(r.Context(), `
		DELETE FROM tags WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		s.logger.Error("delete tag", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the tag.")
		return
	}
	if result.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "tag_not_found", "The tag could not be found.")
		return
	}
	httpx.NoContent(w)
}

type segment struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (s *Server) listSegments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, description, rules, created_at, updated_at
		FROM segments
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, s.organizationID(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segments.")
		return
	}
	defer rows.Close()

	segments := make([]segment, 0)
	for rows.Next() {
		var item segment
		var rules []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &rules, &item.CreatedAt, &item.UpdatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load segments.")
			return
		}
		item.Rules = json.RawMessage(rules)
		segments = append(segments, item)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": segments})
}

type segmentInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
}

func (s *Server) createSegment(w http.ResponseWriter, r *http.Request) {
	var input segmentInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Segment name is required.")
		return
	}
	if len(input.Rules) == 0 {
		httpx.Error(w, http.StatusBadRequest, "invalid_rules", "At least one segment condition is required.")
		return
	}
	if _, _, err := segmentcontacts.CompileSegmentRules(input.Rules, 1); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_rules", err.Error())
		return
	}

	var item segment
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO segments (organization_id, name, description, rules)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING id, name, description, rules, created_at, updated_at
	`, s.organizationID(r), input.Name, strings.TrimSpace(input.Description), []byte(input.Rules)).Scan(
		&item.ID, &item.Name, &item.Description, &item.Rules, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		s.logger.Error("create segment", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the segment.")
		return
	}
	httpx.JSON(w, http.StatusCreated, item)
}

func (s *Server) deleteSegment(w http.ResponseWriter, r *http.Request) {
	tag, err := s.pool.Exec(r.Context(), `
		DELETE FROM segments WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the segment.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "segment_not_found", "The segment could not be found.")
		return
	}
	httpx.NoContent(w)
}

type emailTemplate struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Category  string          `json:"category"`
	Design    json.RawMessage `json:"design"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, name, category, design, created_at, updated_at
		FROM templates
		WHERE organization_id = $1
		ORDER BY updated_at DESC
	`, s.organizationID(r))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load templates.")
		return
	}
	defer rows.Close()

	templates := make([]emailTemplate, 0)
	for rows.Next() {
		var item emailTemplate
		var design []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Category, &design, &item.CreatedAt, &item.UpdatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load templates.")
			return
		}
		item.Design = json.RawMessage(design)
		templates = append(templates, item)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"data": templates})
}

type templateInput struct {
	Name     string          `json:"name"`
	Category string          `json:"category"`
	Design   json.RawMessage `json:"design"`
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var input templateInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Template name is required.")
		return
	}
	if input.Category == "" {
		input.Category = "general"
	}
	if len(input.Design) == 0 {
		input.Design = json.RawMessage(`{"version":1,"blocks":[]}`)
	}
	if !json.Valid(input.Design) {
		httpx.Error(w, http.StatusBadRequest, "invalid_design", "Template design must be valid JSON.")
		return
	}

	var item emailTemplate
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO templates (organization_id, created_by, name, category, design)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING id, name, category, design, created_at, updated_at
	`, s.organizationID(r), s.claims(r).Subject, input.Name, input.Category, []byte(input.Design)).Scan(
		&item.ID, &item.Name, &item.Category, &item.Design, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		s.logger.Error("create template", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create the template.")
		return
	}
	httpx.JSON(w, http.StatusCreated, item)
}
