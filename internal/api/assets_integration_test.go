package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailAssetUploadAndPublicReadIntegration(t *testing.T) {
	if integrationPool == nil {
		t.Skip("no database available")
	}
	server := newIntegrationServer(t)
	user := seedIntegrationUser(
		t, "email-asset-owner@example.test", "email-asset-owner-password", "Asset Owner", "Asset Workspace",
	)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode fixture PNG: %v", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "pixel.png")
	if err != nil {
		t.Fatalf("create multipart image part: %v", err)
	}
	if _, err := part.Write(imageBytes.Bytes()); err != nil {
		t.Fatalf("write multipart image: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart form: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+user.AccessToken)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	assertStatus(t, response, http.StatusCreated)
	var uploaded struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode uploaded asset: %v", err)
	}
	if uploaded.ID == "" || uploaded.URL != "/api/v1/public/assets/"+uploaded.ID {
		t.Fatalf("unexpected uploaded asset response: %+v", uploaded)
	}

	read := httptest.NewRecorder()
	server.Handler().ServeHTTP(read, httptest.NewRequest(http.MethodGet, uploaded.URL, nil))
	assertStatus(t, read, http.StatusOK)
	if read.Header().Get("Content-Type") != "image/png" || read.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected public asset headers: %v", read.Header())
	}
	if !bytes.Equal(read.Body.Bytes(), imageBytes.Bytes()) {
		t.Fatal("public asset response did not match the uploaded image")
	}

	invalidBody := strings.NewReader("--bad\r\nContent-Disposition: form-data; name=\"file\"; filename=\"bad.svg\"\r\nContent-Type: image/svg+xml\r\n\r\n<svg/>\r\n--bad--\r\n")
	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/v1/assets", invalidBody)
	invalidRequest.Header.Set("Content-Type", "multipart/form-data; boundary=bad")
	invalidRequest.Header.Set("Authorization", "Bearer "+user.AccessToken)
	invalidResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("SVG upload status = %d, want 400; body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
}
