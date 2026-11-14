package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khayode/email-platform/internal/kit"
)

func TestValidatePublicContact(t *testing.T) {
	valid := publicContactRequest{
		Name:    "Ada Lovelace",
		Email:   "ada@example.com",
		Topic:   "Plans and pricing",
		Message: "I would like to know more about the available plans.",
	}
	if err := validatePublicContact(valid); err != nil {
		t.Fatalf("valid contact rejected: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*publicContactRequest)
		wantErr string
	}{
		{
			name:    "short name",
			mutate:  func(request *publicContactRequest) { request.Name = "A" },
			wantErr: "Name",
		},
		{
			name:    "display-name email",
			mutate:  func(request *publicContactRequest) { request.Email = "Ada <ada@example.com>" },
			wantErr: "email",
		},
		{
			name:    "unknown topic",
			mutate:  func(request *publicContactRequest) { request.Topic = "Other" },
			wantErr: "topic",
		},
		{
			name:    "short message",
			mutate:  func(request *publicContactRequest) { request.Message = "Too short" },
			wantErr: "Message",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			err := validatePublicContact(request)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validate error = %v, want text containing %q", err, test.wantErr)
			}
		})
	}
}

func TestPublicContactHoneypotIsAcceptedWithoutStorage(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/contact",
		strings.NewReader(`{"website":"https://spam.example"}`))
	response := httptest.NewRecorder()

	server.submitPublicContact(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusAccepted, response.Body)
	}
}

func TestPublicContactRejectsInvalidJSONBeforeDatabaseAccess(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/contact", strings.NewReader(`{"email":`))
	response := httptest.NewRecorder()

	server.submitPublicContact(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, http.StatusBadRequest, response.Body)
	}
}

func TestPublicContactRateLimit(t *testing.T) {
	handler := kit.RateLimit(kit.NewRateLimiter(1, 1))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	for i, want := range []int{http.StatusAccepted, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/public/contact", nil)
		request.RemoteAddr = "192.0.2.10:12345"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("request %d status = %d, want %d", i+1, response.Code, want)
		}
	}
}
