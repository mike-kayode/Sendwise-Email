package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/httpx"
	"github.com/khayode/email-platform/internal/transactional"
)

type transactionalSMTPCredentialResponse struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Username       string     `json:"username"`
	RequestsPerSec int        `json:"requests_per_second"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	Password       string     `json:"password,omitempty"`
}

func (s *Server) listTransactionalSMTPCredentials(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, name, COALESCE(username, ''), requests_per_second, last_used_at, revoked_at, created_at
		FROM transactional_smtp_credentials
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, s.organizationID(r))
	if err != nil {
		s.logger.Error("list transactional SMTP credentials", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list SMTP credentials.")
		return
	}
	defer rows.Close()
	credentials := make([]transactionalSMTPCredentialResponse, 0)
	for rows.Next() {
		var credential transactionalSMTPCredentialResponse
		if err := rows.Scan(&credential.ID, &credential.Name, &credential.Username,
			&credential.RequestsPerSec, &credential.LastUsedAt, &credential.RevokedAt, &credential.CreatedAt); err != nil {
			s.logger.Error("scan transactional SMTP credential", "error", err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list SMTP credentials.")
			return
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		s.logger.Error("iterate transactional SMTP credentials", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not list SMTP credentials.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": credentials})
}

func (s *Server) createTransactionalSMTPCredential(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 100 || strings.ContainsAny(input.Name, "\r\n") {
		httpx.Error(w, http.StatusBadRequest, "invalid_name", "A credential name between 1 and 100 characters is required.")
		return
	}
	username, password, passwordHash, err := transactional.NewSMTPSecret()
	if err != nil {
		s.logger.Error("generate transactional SMTP credential", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create SMTP credential.")
		return
	}
	requestsPerSecond := s.cfg.TransactionalRequestsPerSecond
	if requestsPerSecond < 1 {
		requestsPerSecond = 1
	}
	var credential transactionalSMTPCredentialResponse
	err = s.pool.QueryRow(r.Context(), `
		INSERT INTO transactional_smtp_credentials
			(organization_id, name, username, secret_hash, requests_per_second)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, name, username, requests_per_second, last_used_at, revoked_at, created_at
	`, s.organizationID(r), input.Name, username, passwordHash, requestsPerSecond).Scan(
		&credential.ID, &credential.Name, &credential.Username, &credential.RequestsPerSec,
		&credential.LastUsedAt, &credential.RevokedAt, &credential.CreatedAt,
	)
	if err != nil {
		s.logger.Error("create transactional SMTP credential", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not create SMTP credential.")
		return
	}
	credential.Password = password
	s.recordTransactionalAudit(r, "transactional.smtp_credential_created", "")
	httpx.JSON(w, http.StatusCreated, credential)
}

func (s *Server) revokeTransactionalSMTPCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_smtp_credential_id", "The SMTP credential ID is invalid.")
		return
	}
	tag, err := s.pool.Exec(r.Context(), `
		UPDATE transactional_smtp_credentials
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE id = $1 AND organization_id = $2
	`, id, s.organizationID(r))
	if err != nil {
		s.logger.Error("revoke transactional SMTP credential", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not revoke SMTP credential.")
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "smtp_credential_not_found", "The SMTP credential could not be found.")
		return
	}
	s.recordTransactionalAudit(r, "transactional.smtp_credential_revoked", "")
	httpx.NoContent(w)
}
