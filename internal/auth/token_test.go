package auth

import (
	"strings"
	"testing"
	"time"
)

func TestAccessTokenRoundTrip(t *testing.T) {
	manager := NewManager("a-long-test-secret-value", "test-issuer", time.Minute)
	token, err := manager.IssueAccess("user-1", "org-1", RoleManager)
	if err != nil {
		t.Fatalf("IssueAccess() error = %v", err)
	}

	claims, err := manager.Parse(token)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if claims.Subject != "user-1" || claims.OrganizationID != "org-1" || claims.Role != RoleManager {
		t.Fatalf("unexpected access claims: %#v", claims)
	}
	if claims.Issuer != "test-issuer" || claims.ExpiresAt == nil || claims.ID == "" {
		t.Fatalf("registered claims missing issuer, expiry, or id: %#v", claims.RegisteredClaims)
	}
}

func TestParseRejectsInvalidTokens(t *testing.T) {
	manager := NewManager("a-long-test-secret-value", "test-issuer", time.Minute)
	token, err := manager.IssueAccess("user-1", "org-1", RoleOwner)
	if err != nil {
		t.Fatalf("IssueAccess() error = %v", err)
	}

	parts := strings.Split(token, ".")
	replacement := "A"
	if strings.HasPrefix(parts[1], replacement) {
		replacement = "B"
	}
	tampered := parts[0] + "." + replacement + parts[1][1:] + "." + parts[2]

	tests := []struct {
		name    string
		manager *Manager
		token   string
	}{
		{name: "wrong signing secret", manager: NewManager("different-test-secret-value", "test-issuer", time.Minute), token: token},
		{name: "wrong issuer", manager: NewManager("a-long-test-secret-value", "other-issuer", time.Minute), token: token},
		{name: "tampered token", manager: manager, token: tampered},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.manager.Parse(test.token); err == nil {
				t.Fatal("Parse() accepted an invalid token")
			}
		})
	}
}

func TestParseRejectsExpiredToken(t *testing.T) {
	manager := NewManager("a-long-test-secret-value", "test-issuer", -time.Minute)
	token, err := manager.IssueAccess("user-1", "org-1", RoleOwner)
	if err != nil {
		t.Fatalf("IssueAccess() error = %v", err)
	}
	if _, err := manager.Parse(token); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("Parse() error = %v, want expired token error", err)
	}
}
