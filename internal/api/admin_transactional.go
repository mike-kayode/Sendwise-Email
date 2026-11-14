package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/httpx"
)

type adminTransactionalMessage struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	WorkspaceID string    `json:"workspace_id"`
	Workspace   string    `json:"workspace_name"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Server) searchAdminTransactionalMessages(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) < 2 || len(search) > 100 {
		httpx.Error(w, http.StatusBadRequest, "invalid_search", "Search must be from 2 to 100 characters.")
		return
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
		WHERE (m.id::text = $1 OR m.message_id = $1
			OR m.organization_id::text ILIKE '%' || $1 || '%'
			OR o.name ILIKE '%' || $1 || '%' OR o.slug ILIKE '%' || $1 || '%')
	`
	var total int
	if err := s.pool.QueryRow(r.Context(), `
		SELECT count(*)
		FROM transactional_messages m
		JOIN organizations o ON o.id = m.organization_id
	`+filter, search).Scan(&total); err != nil {
		s.logger.Error("count admin transactional message matches", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not search transactional message metadata.")
		return
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT m.id::text, m.message_id, m.organization_id::text, o.name,
		       m.status, m.created_at
		FROM transactional_messages m
		JOIN organizations o ON o.id = m.organization_id
	`+filter+`
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $2 OFFSET $3
	`, search, limit, offset)
	if err != nil {
		s.logger.Error("search admin transactional message metadata", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not search transactional message metadata.")
		return
	}
	defer rows.Close()
	messages := make([]adminTransactionalMessage, 0, limit)
	for rows.Next() {
		var message adminTransactionalMessage
		if err := rows.Scan(
			&message.ID, &message.MessageID, &message.WorkspaceID,
			&message.Workspace, &message.Status, &message.CreatedAt,
		); err != nil {
			s.logger.Error("scan admin transactional message metadata", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not search transactional message metadata.")
			return
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate admin transactional message metadata", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not search transactional message metadata.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"data": messages,
		"pagination": paginationResponse{
			Total: total, Limit: limit, Offset: offset,
		},
	})
}
