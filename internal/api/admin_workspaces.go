package api

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

func (s *Server) setAdminWorkspaceSuspended(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID is invalid.")
		return
	}
	var input struct {
		ExpectedStatus string `json:"expected_status"`
		Status         string `json:"status"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "Specify a valid workspace status.")
		return
	}
	status := input.Status
	if status != "active" && status != "suspended" {
		httpx.Error(w, http.StatusBadRequest, "invalid_workspace_status", "Workspace status must be active or suspended.")
		return
	}
	if input.ExpectedStatus != "active" && input.ExpectedStatus != "suspended" {
		httpx.Error(w, http.StatusBadRequest, "invalid_expected_status", "The current workspace status is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE organizations SET status = $2, updated_at = now()
		WHERE id = $1 AND status = $3
	`, id, status, input.ExpectedStatus)
	if err != nil {
		s.logger.Error("set admin workspace suspension", "error", err, "workspace_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update workspace status.")
		return
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(r.Context(), `
			SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)
		`, id).Scan(&exists); err != nil {
			s.logger.Error("check admin workspace existence", "error", err, "workspace_id", id)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update workspace status.")
			return
		}
		if !exists {
			httpx.Error(w, http.StatusNotFound, "workspace_not_found", "Workspace not found.")
			return
		}
		var currentStatus string
		if err := s.pool.QueryRow(r.Context(), `
			SELECT status FROM organizations WHERE id = $1
		`, id).Scan(&currentStatus); err != nil {
			s.logger.Error("load workspace status after admin update conflict", "error", err, "workspace_id", id)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update workspace status.")
			return
		}
		if currentStatus == "closed" {
			httpx.Error(w, http.StatusConflict, "workspace_closed", "A closed workspace cannot be changed.")
			return
		}
		httpx.Error(w, http.StatusConflict, "workspace_status_changed", "Workspace status changed; reload it before retrying.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": status})
}

func (s *Server) adminWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID is invalid.")
		return
	}
	var status string
	if err := s.pool.QueryRow(r.Context(), `
		SELECT status FROM organizations WHERE id = $1
	`, id).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "workspace_not_found", "Workspace not found.")
		return
	} else if err != nil {
		s.logger.Error("load admin workspace status", "error", err, "workspace_id", id)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load workspace status.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": status})
}
