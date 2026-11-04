package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	OrganizationID string `json:"org,omitempty"`
	Role           string `json:"role,omitempty"`
	CampaignID     string `json:"cmp,omitempty"`
	ContactID      string `json:"cid,omitempty"`
	URL            string `json:"url,omitempty"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewManager(secret, issuer string, ttl time.Duration) *Manager {
	return &Manager{secret: []byte(secret), issuer: issuer, ttl: ttl}
}

func (m *Manager) TTL() time.Duration {
	return m.ttl
}

func (m *Manager) IssueAccess(userID, organizationID, role string) (string, error) {
	return m.issue(Claims{
		OrganizationID: organizationID,
		Role:           role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	}, m.ttl)
}

func (m *Manager) IssueTracking(jobID, organizationID, campaignID, contactID string) (string, error) {
	return m.issue(Claims{
		OrganizationID: organizationID,
		CampaignID:     campaignID,
		ContactID:      contactID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: jobID,
		},
	}, 30*24*time.Hour)
}

func (m *Manager) IssueClick(jobID, organizationID, campaignID, contactID, targetURL string) (string, error) {
	return m.issue(Claims{
		OrganizationID: organizationID,
		CampaignID:     campaignID,
		ContactID:      contactID,
		URL:            targetURL,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: jobID,
		},
	}, 30*24*time.Hour)
}

func (m *Manager) IssueUnsubscribe(organizationID, contactID string) (string, error) {
	return m.issue(Claims{
		OrganizationID: organizationID,
		ContactID:      contactID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: contactID,
		},
	}, 365*24*time.Hour)
}

func (m *Manager) issue(claims Claims, ttl time.Duration) (string, error) {
	now := time.Now()
	claims.Issuer = m.issuer
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.NotBefore = jwt.NewNumericDate(now.Add(-30 * time.Second))
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	claims.ID = uuid.NewString()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

func (m *Manager) Parse(raw string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %q", token.Header["alg"])
			}
			return m.secret, nil
		},
		jwt.WithIssuer(m.issuer),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	return claims, nil
}
