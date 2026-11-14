package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
	"github.com/khayode/email-platform/internal/kit"
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
		httpx.Error(w, http.StatusBadRequest, "invalid_upload", "Choose a PNG, JPEG, GIF, or WebP image to upload.")
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
	assetID, assetURL, err := s.storeEmailAsset(r.Context(), s.organizationID(r), content)
	if errors.Is(err, errInvalidEmailImage) {
		httpx.Error(w, http.StatusBadRequest, "invalid_image", "Upload a valid PNG, JPEG, GIF, or WebP image no larger than 10000 by 10000 pixels.")
		return
	}
	if errors.Is(err, errUnsupportedEmailImage) {
		httpx.Error(w, http.StatusUnsupportedMediaType, "unsupported_image", "Only PNG, JPEG, GIF, or WebP images are supported.")
		return
	}
	if err != nil {
		s.logger.Error("store uploaded email image", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "asset_upload_failed", "The image could not be stored.")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{
		"id":  assetID,
		"url": assetURL,
	})
}

func (s *Server) importRemoteEmailAsset(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	content, err := fetchRemoteEmailImage(r.Context(), input.URL)
	if err != nil {
		httpx.Error(w, http.StatusUnprocessableEntity, "image_import_failed", "The remote image could not be safely fetched. Keep the original URL or replace the image.")
		return
	}
	_, assetURL, err := s.storeEmailAsset(r.Context(), s.organizationID(r), content)
	if errors.Is(err, errInvalidEmailImage) || errors.Is(err, errUnsupportedEmailImage) {
		httpx.Error(w, http.StatusUnsupportedMediaType, "unsupported_image", "Only valid PNG, JPEG, GIF, or WebP images up to 8 MiB are supported.")
		return
	}
	if err != nil {
		s.logger.Error("store imported email image", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "asset_upload_failed", "The remote image could not be stored.")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"url": assetURL})
}

var (
	errInvalidEmailImage     = errors.New("invalid email image")
	errUnsupportedEmailImage = errors.New("unsupported email image")
)

func (s *Server) storeEmailAsset(ctx context.Context, organizationID string, content []byte) (string, string, error) {
	contentType, err := emailImageContentType(content)
	if err != nil {
		return "", "", err
	}
	var id string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO email_assets (organization_id, content_type, content)
		VALUES ($1, $2, $3)
		RETURNING id
	`, organizationID, contentType, content).Scan(&id); err != nil {
		return "", "", err
	}
	return id, "/api/v1/public/assets/" + id, nil
}

func emailImageContentType(content []byte) (string, error) {
	if len(content) == 0 || len(content) > maxEmailAssetBytes {
		return "", errInvalidEmailImage
	}
	format := ""
	width, height := 0, 0
	if len(content) >= 30 && string(content[:4]) == "RIFF" && string(content[8:12]) == "WEBP" {
		format = "webp"
		switch string(content[12:16]) {
		case "VP8X":
			width = 1 + int(content[24]) + (int(content[25]) << 8) + (int(content[26]) << 16)
			height = 1 + int(content[27]) + (int(content[28]) << 8) + (int(content[29]) << 16)
		case "VP8L":
			if content[20] != 0x2f {
				return "", errInvalidEmailImage
			}
			width = 1 + (int(content[22]&0x3f) << 8) + int(content[21])
			height = 1 + (int(content[24]&0x0f) << 10) + (int(content[23]) << 2) + (int(content[22]&0xc0) >> 6)
		case "VP8 ":
			if content[23] != 0x9d || content[24] != 0x01 || content[25] != 0x2a {
				return "", errInvalidEmailImage
			}
			width = int(content[26]) | (int(content[27]&0x3f) << 8)
			height = int(content[28]) | (int(content[29]&0x3f) << 8)
		}
	} else {
		config, decodedFormat, err := image.DecodeConfig(bytes.NewReader(content))
		if err != nil {
			return "", errInvalidEmailImage
		}
		format, width, height = decodedFormat, config.Width, config.Height
	}
	if width < 1 || height < 1 || width > 10000 || height > 10000 {
		return "", errInvalidEmailImage
	}
	contentType := map[string]string{
		"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
	}[format]
	if contentType == "" {
		return "", errUnsupportedEmailImage
	}
	return contentType, nil
}

func fetchRemoteEmailImage(ctx context.Context, rawURL string) ([]byte, error) {
	if err := validateRemoteImageURL(rawURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("remote image host resolved to no addresses")
			}
			preferredIP := ips[0].IP
			for _, resolved := range ips {
				if !kit.IsPublicIP(resolved.IP) {
					return nil, errors.New("remote image host resolved to a non-public address")
				}
				if resolved.IP.To4() != nil {
					preferredIP = resolved.IP
				}
			}
			return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(
				ctx, "tcp", net.JoinHostPort(preferredIP.String(), port),
			)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many image redirects")
			}
			return validateRemoteImageURL(request.URL.String())
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote image returned %s", response.Status)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return nil, errors.New("remote response is not an image")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxEmailAssetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxEmailAssetBytes {
		return nil, errors.New("remote image exceeds size limit")
	}
	contentType, err := emailImageContentType(content)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(mediaType, "image/jpg") {
		mediaType = "image/jpeg"
	}
	if !strings.EqualFold(mediaType, contentType) {
		return nil, errors.New("remote image content type does not match its data")
	}
	return content, nil
}

func validateRemoteImageURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("remote image URL must be an absolute HTTPS URL")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return errors.New("remote image URL must use HTTPS port 443")
	}
	if address, err := netip.ParseAddr(parsed.Hostname()); err == nil && !kit.IsPublicIP(net.IP(address.AsSlice())) {
		return errors.New("remote image URL cannot target a private address")
	}
	return nil
}

func isPublicImageIP(ip net.IP) bool {
	return kit.IsPublicIP(ip)
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
