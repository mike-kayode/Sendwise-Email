package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

type adminSuppression struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceName string    `json:"workspace_name"`
	WorkspaceSlug string    `json:"workspace_slug"`
	Email         string    `json:"email"`
	Reason        string    `json:"reason"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Server) listAdminSuppressions(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if len(search) > 254 || len(workspaceID) > 36 {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", "Suppression filters are too long.")
		return
	}
	if workspaceID != "" {
		if _, err := uuid.Parse(workspaceID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID is invalid.")
			return
		}
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			httpx.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			httpx.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	filter := `
		WHERE ($1 = '' OR s.email ILIKE '%' || $1 || '%'
			OR o.name ILIKE '%' || $1 || '%' OR o.slug ILIKE '%' || $1 || '%'
			OR o.id::text = $1 OR s.id::text = $1)
		  AND ($2 = '' OR s.organization_id = $2::uuid)
	`
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM suppressions s JOIN organizations o ON o.id = s.organization_id
	`+filter, search, workspaceID).Scan(&total); err != nil {
		s.logger.Error("count admin suppressions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load suppressions.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT s.id::text, s.organization_id::text, o.name, o.slug,
		       s.email, s.reason, COALESCE(s.source, ''), s.created_at
		FROM suppressions s JOIN organizations o ON o.id = s.organization_id
	`+filter+`
		ORDER BY s.created_at DESC, s.id DESC
		LIMIT $3 OFFSET $4
	`, search, workspaceID, limit, offset)
	if err != nil {
		s.logger.Error("list admin suppressions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load suppressions.")
		return
	}
	defer rows.Close()
	items := make([]adminSuppression, 0, limit)
	for rows.Next() {
		var item adminSuppression
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.WorkspaceName,
			&item.WorkspaceSlug, &item.Email, &item.Reason, &item.Source, &item.CreatedAt); err != nil {
			s.logger.Error("scan admin suppression", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load suppressions.")
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate admin suppressions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load suppressions.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": items,
		"pagination": paginationResponse{
			Total: total, Limit: limit, Offset: offset,
		},
	})
}

func (s *Server) createAdminSuppression(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID string `json:"workspace_id"`
		Email       string `json:"email"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Provide a valid suppression request.")
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Email = strings.TrimSpace(input.Email)
	if _, err := uuid.Parse(input.WorkspaceID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID is invalid.")
		return
	}
	address, err := mail.ParseAddress(input.Email)
	if err != nil || !strings.EqualFold(address.Address, input.Email) || len(input.Email) > 254 {
		httpx.Error(w, http.StatusBadRequest, "invalid_email", "Provide a valid email address.")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		s.logger.Error("begin admin suppression transaction", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not add suppression.")
		return
	}
	defer tx.Rollback(r.Context())
	var item adminSuppression
	err = tx.QueryRow(r.Context(), `
		INSERT INTO suppressions (organization_id, email, reason, source)
		VALUES ($1, lower($2), 'manual', 'admin')
		ON CONFLICT (organization_id, lower(email)) DO NOTHING
		RETURNING id::text, organization_id::text, email, reason, COALESCE(source, ''), created_at
	`, input.WorkspaceID, input.Email).Scan(
		&item.ID, &item.WorkspaceID, &item.Email, &item.Reason, &item.Source, &item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusConflict, "suppression_exists", "This address is already suppressed in that workspace.")
		return
	}
	if err != nil {
		s.logger.Error("insert admin suppression", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not add suppression.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.logger.Error("commit admin suppression", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not add suppression.")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"data": item})
}

func (s *Server) deleteAdminSuppression(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_suppression_id", "The suppression ID is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `DELETE FROM suppressions WHERE id = $1`, id)
	if err != nil {
		s.logger.Error("delete admin suppression", "error", err, "suppression_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not remove suppression.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "suppression_not_found", "Suppression not found.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
