package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestPostgresLogoutRevokesAccessToken(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	store := NewStoreFromPool(pool)
	userID := uuid.NewString()
	organizationID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status, email_verified_at)
		VALUES ($1, $2, 'integration-hash', 'Session Test', 'active', now())
	`, userID, userID+"@example.test"); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	session, err := store.CreateSession(ctx, SessionCreate{
		UserID: userID, OrganizationID: organizationID, Role: "owner",
		RefreshTokenHash: HashToken("logout-refresh-token"), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create identity session: %v", err)
	}
	tokens := kit.NewManager("integration-session-secret", "email-platform", time.Minute)
	accessToken, err := tokens.IssueAccess(userID, userID+"@example.test", organizationID, "owner", session.ID)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	handler := NewHandler(store, tokens, nil, NewLogMailer(kit.NewTestLogger()), Config{
		BaseConfig:     kit.BaseConfig{JWTSecret: "integration-session-secret", JWTIssuer: "email-platform"},
		AccessTokenTTL: time.Minute,
	}, kit.NewTestLogger())

	request := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("before logout /auth/me status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "session_revoked") {
		t.Fatalf("after logout /auth/me = %d %s, want 401 session_revoked", response.Code, response.Body.String())
	}
}

func TestPostgresRefreshRotationAndReuseRevokesSessionFamily(t *testing.T) {
	pool := testutil.NewPostgresWithMigrations(t, "migrations")
	ctx := context.Background()
	store := NewStoreFromPool(pool)
	userID := uuid.NewString()
	organizationID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status, email_verified_at)
		VALUES ($1, $2, 'integration-hash', 'Refresh Test', 'active', now())
	`, userID, userID+"@example.test"); err != nil {
		t.Fatalf("seed identity user: %v", err)
	}
	first, err := store.CreateSession(ctx, SessionCreate{
		UserID: userID, OrganizationID: organizationID, Role: "owner",
		RefreshTokenHash: HashToken("refresh-family-old"), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create refresh session: %v", err)
	}
	second, err := store.CreateSession(ctx, SessionCreate{
		UserID: userID, OrganizationID: organizationID, Role: "owner",
		RefreshTokenHash: HashToken("refresh-family-sibling"), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create sibling session: %v", err)
	}
	logger := kit.NewTestLogger()
	handler := NewHandler(store,
		kit.NewManager("integration-session-secret", "email-platform", time.Minute),
		nil, NewLogMailer(logger),
		Config{
			BaseConfig:      kit.BaseConfig{JWTSecret: "integration-session-secret", JWTIssuer: "email-platform"},
			AccessTokenTTL:  time.Minute,
			RefreshTokenTTL: time.Hour,
			CookieSecure:    true,
		},
		logger,
	)
	routes := handler.Routes()
	refresh := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/auth/refresh",
			strings.NewReader(`{"refresh_token":"`+token+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, request)
		return response
	}

	rotated := refresh("refresh-family-old")
	if rotated.Code != http.StatusOK {
		t.Fatalf("normal refresh status = %d, body = %s", rotated.Code, rotated.Body.String())
	}
	cookies := rotated.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "refresh-family-old" || !cookies[0].HttpOnly ||
		!cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("rotated refresh cookie = %#v; want changed HttpOnly, Secure, SameSite=Lax cookie", cookies)
	}
	var response sessionResponse
	if err := json.Unmarshal(rotated.Body.Bytes(), &response); err != nil || response.AccessToken == "" {
		t.Fatalf("decode refresh response: token=%q err=%v", response.AccessToken, err)
	}

	reused := refresh("refresh-family-old")
	if reused.Code != http.StatusUnauthorized || !strings.Contains(reused.Body.String(), "refresh_token_reuse") {
		t.Fatalf("old refresh token status = %d, body = %s; want 401 refresh_token_reuse", reused.Code, reused.Body.String())
	}
	for _, sessionID := range []string{first.ID, second.ID} {
		session, err := store.GetSession(ctx, sessionID)
		if err != nil {
			t.Fatalf("get session %s after reuse: %v", sessionID, err)
		}
		if session.RevokedAt == nil {
			t.Errorf("session %s remained active after refresh-token reuse", sessionID)
		}
	}
}
