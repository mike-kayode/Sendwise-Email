package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireRoles(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		wantStatus int
	}{
		{name: "allowed role", role: RoleManager, wantStatus: http.StatusNoContent},
		{name: "viewer denied", role: RoleViewer, wantStatus: http.StatusForbidden},
		{name: "missing role denied", wantStatus: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := RequireRoles(RoleOwner, RoleAdmin, RoleManager)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodPost, "/resource", nil)
			if test.role != "" {
				claims := &Claims{Role: test.role}
				request = request.WithContext(context.WithValue(request.Context(), claimsKey, claims))
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}
