package kit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testSessionStore struct {
	active bool
	err    error
}

func (s testSessionStore) Active(context.Context, string, string) (bool, error) {
	return s.active, s.err
}

func TestSessionMiddlewareFailsClosed(t *testing.T) {
	base := NewManager("test-secret", "test-issuer", time.Minute)
	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	})
	for _, test := range []struct {
		name       string
		sessionID  string
		store      SessionStore
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unreachable store",
			sessionID:  "session-id",
			store:      testSessionStore{err: errors.New("identity unavailable")},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "session_check_unavailable",
		},
		{
			name:       "missing session identifier",
			store:      testSessionStore{active: true},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "session_revoked",
		},
		{
			name:       "inactive session",
			sessionID:  "session-id",
			store:      testSessionStore{active: false},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "session_revoked",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			nextCalled = false
			token, err := base.IssueAccess("user-id", "user@example.test", "org-id", "owner", test.sessionID)
			if err != nil {
				t.Fatalf("issue access token: %v", err)
			}
			manager := base.WithSessionStore(test.store)
			handler := manager.Middleware(next)
			request := httptest.NewRequest(http.MethodGet, "/resource", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), test.wantCode) {
				t.Fatalf("response = %d %s, want status %d and code %q",
					recorder.Code, recorder.Body.String(), test.wantStatus, test.wantCode)
			}
			if nextCalled {
				t.Fatal("request reached the protected handler")
			}
		})
	}
}

func TestOptionalSessionMiddlewareFailsClosed(t *testing.T) {
	base := NewManager("test-secret", "test-issuer", time.Minute)
	token, err := base.IssueAccess("user-id", "user@example.test", "org-id", "owner", "session-id")
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	manager := base.WithSessionStore(testSessionStore{err: errors.New("identity unavailable")})
	nextCalled := false
	handler := manager.OptionalSessionMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))
	request := httptest.NewRequest(http.MethodGet, "/resource", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "session_check_unavailable") {
		t.Fatalf("response = %d %s, want 503 session_check_unavailable", recorder.Code, recorder.Body.String())
	}
	if nextCalled {
		t.Fatal("request reached the protected handler")
	}
}
