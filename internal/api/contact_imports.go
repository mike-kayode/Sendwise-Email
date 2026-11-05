package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/contacts"
	"github.com/khayode/email-platform/internal/httpx"
)

const (
	maxCSVImportBytes  = 16 << 20
	maxCSVRequestBytes = maxCSVImportBytes + (1 << 20)
	maxCSVMappingBytes = 64 << 10
)

type contactImport struct {
	ID            string          `json:"id"`
	Status        string          `json:"status"`
	FileName      string          `json:"file_name"`
	ListID        string          `json:"list_id,omitempty"`
	TotalRows     int             `json:"total_rows"`
	ProcessedRows int             `json:"processed_rows"`
	Imported      int             `json:"imported"`
	Skipped       int             `json:"skipped"`
	Invalid       int             `json:"invalid"`
	Errors        json.RawMessage `json:"errors"`
	LastError     string          `json:"last_error,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
}

func (s *Server) importContacts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCSVRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "file_too_large", "CSV requests are limited to 16 MiB plus multipart overhead.")
			return
		}
		httpx.Error(w, http.StatusBadRequest, "invalid_upload", "Upload a CSV file no larger than 16 MiB using the 'file' field.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "missing_file", "Upload a CSV file using the 'file' field.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxCSVImportBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_upload", "The CSV upload could not be read.")
		return
	}
	if len(data) > maxCSVImportBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "file_too_large", "CSV files are limited to 16 MiB.")
		return
	}

	var mapping *contacts.CSVColumnMapping
	if rawMapping := r.FormValue("mapping"); rawMapping != "" {
		if len(rawMapping) > maxCSVMappingBytes {
			httpx.Error(w, http.StatusBadRequest, "invalid_mapping", "The column mapping is too large.")
			return
		}
		decoder := json.NewDecoder(strings.NewReader(rawMapping))
		decoder.DisallowUnknownFields()
		var parsed contacts.CSVColumnMapping
		if err := decoder.Decode(&parsed); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_mapping", "The column mapping is invalid JSON.")
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			httpx.Error(w, http.StatusBadRequest, "invalid_mapping", "The column mapping must contain one JSON object.")
			return
		}
		mapping = &parsed
	}

	if _, resolved, err := contacts.NewCSVReader(bytes.NewReader(data), mapping); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_csv_or_mapping", err.Error())
		return
	} else {
		mapping = &resolved
	}

	mappingJSON, err := json.Marshal(mapping)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not save the import mapping.")
		return
	}
	fileName := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	fileName = strings.TrimSpace(fileName)
	if fileName == "" || fileName == "." {
		fileName = "contacts.csv"
	}
	if len(fileName) > 255 {
		fileName = fileName[:255]
	}
	fileName = strings.ToValidUTF8(fileName, "_")

	listID := strings.TrimSpace(r.FormValue("list_id"))
	if listID != "" {
		var listExists bool
		if err := s.pool.QueryRow(r.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM contact_lists
				WHERE id = $1::uuid AND organization_id = $2
			)
		`, listID, s.organizationID(r)).Scan(&listExists); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_list", "The selected list is invalid.")
			return
		}
		if !listExists {
			httpx.Error(w, http.StatusNotFound, "list_not_found", "The selected list could not be found.")
			return
		}
	}

	var jobID string
	if err := s.pool.QueryRow(r.Context(), `
		INSERT INTO contact_imports (organization_id, created_by, file_name, source_data, mapping, list_id)
		VALUES ($1, $2, $3, $4, $5::jsonb, NULLIF($6, '')::uuid)
		RETURNING id
	`, s.organizationID(r), s.claims(r).Subject, fileName, data, mappingJSON, listID).Scan(&jobID); err != nil {
		s.logger.Error("queue contact import", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not queue the contact import.")
		return
	}

	httpx.JSON(w, http.StatusAccepted, map[string]string{
		"id":     jobID,
		"status": "queued",
	})
}

func (s *Server) getContactImport(w http.ResponseWriter, r *http.Request) {
	var item contactImport
	var rawErrors []byte
	err := s.pool.QueryRow(r.Context(), `
		SELECT id, status, file_name, COALESCE(list_id::text, ''), total_rows, processed_rows,
		       imported_count, skipped_count, invalid_count, errors,
		       COALESCE(last_error, ''), created_at, started_at, completed_at
		FROM contact_imports
		WHERE id = $1 AND organization_id = $2
	`, r.PathValue("id"), s.organizationID(r)).Scan(
		&item.ID, &item.Status, &item.FileName, &item.ListID, &item.TotalRows, &item.ProcessedRows,
		&item.Imported, &item.Skipped, &item.Invalid, &rawErrors,
		&item.LastError, &item.CreatedAt, &item.StartedAt, &item.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "import_not_found", "The contact import could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("load contact import", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the contact import.")
		return
	}
	if len(rawErrors) == 0 {
		rawErrors = []byte("[]")
	}
	item.Errors = json.RawMessage(rawErrors)
	httpx.JSON(w, http.StatusOK, item)
}
