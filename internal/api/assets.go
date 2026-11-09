package api

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

const maxEmailAssetBytes = 8 << 20

func (s *Server) uploadEmailAsset(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxEmailAssetBytes+(1<<20))
	if err := r.ParseMultipartForm(maxEmailAssetBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "asset_too_large", "Images must be 8 MiB or smaller.")
			return
		}
		httpx.Error(w, http.StatusBadRequest, "invalid_upload", "Choose a PNG, JPEG, or GIF image to upload.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "missing_image", "Choose an image file to upload.")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxEmailAssetBytes+1))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_upload", "The image could not be read.")
		return
	}
	if len(content) == 0 || len(content) > maxEmailAssetBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "asset_too_large", "Images must be between 1 byte and 8 MiB.")
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 10000 || config.Height > 10000 {
		httpx.Error(w, http.StatusBadRequest, "invalid_image", "Upload a valid PNG, JPEG, or GIF image no larger than 10000 by 10000 pixels.")
		return
	}
	contentType := map[string]string{
		"png":  "image/png",
		"jpeg": "image/jpeg",
		"gif":  "image/gif",
	}[format]
	if contentType == "" {
		httpx.Error(w, http.StatusUnsupportedMediaType, "unsupported_image", "Only PNG, JPEG, and GIF images are supported.")
		return
	}

	var id string
	if err := s.pool.QueryRow(r.Context(), `
		INSERT INTO email_assets (organization_id, content_type, content)
		VALUES ($1, $2, $3)
		RETURNING id
	`, s.organizationID(r), contentType, content).Scan(&id); err != nil {
		s.logger.Error("store uploaded email image", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "asset_upload_failed", "The image could not be stored.")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{
		"id":  id,
		"url": "/api/v1/public/assets/" + id,
	})
}

func (s *Server) getPublicEmailAsset(w http.ResponseWriter, r *http.Request) {
	if _, err := uuid.Parse(r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	var contentType string
	var content []byte
	err := s.pool.QueryRow(r.Context(), `
		SELECT content_type, content
		FROM email_assets
		WHERE id = $1
	`, r.PathValue("id")).Scan(&contentType, &content)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		s.logger.Error("load public email image", "error", err)
		http.Error(w, "The image could not be loaded.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(content)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		s.logger.Error("write public email image", "error", err)
	}
}
