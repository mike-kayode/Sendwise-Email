package auth

import (
	"fmt"
	"log/slog"
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
	SessionID      string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret       []byte
	issuer       string
	ttl          time.Duration
	sessionStore SessionStore
	logger       *slog.Logger
}

func NewManager(secret, issuer string, ttl time.Duration) *Manager {
	return NewManagerWithStore(secret, issuer, ttl, nil)
}

// NewManagerWithStore creates a Manager that consults sessionStore on every
// request. If sessionStore is nil, the middleware skips revocation checks.
func NewManagerWithStore(secret, issuer string, ttl time.Duration, sessionStore SessionStore) *Manager {
	return &Manager{
		secret:       []byte(secret),
		issuer:       issuer,
		ttl:          ttl,
		sessionStore: sessionStore,
	}
}

func (m *Manager) TTL() time.Duration {
	return m.ttl
}

// WithSessionStore returns a copy of the manager with the given session store.
func (m *Manager) WithSessionStore(sessionStore SessionStore) *Manager {
	return &Manager{
		secret:       m.secret,
		issuer:       m.issuer,
		ttl:          m.ttl,
		sessionStore: sessionStore,
	}
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

// IssueAccessWithSession is like IssueAccess but also creates a session record
// and returns the session ID to embed in the access token.
func (m *Manager) IssueAccessWithSession(userID, organizationID, role string) (string, string, error) {
	sessionID := uuid.NewString()
	token, err := m.issue(Claims{
		OrganizationID: organizationID,
		Role:           role,
		SessionID:      sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	}, m.ttl)
	return token, sessionID, err
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

func (m *Manager) issue(claims Claims, ttl time.Duration, sessionID ...string) (string, error) {
	if len(sessionID) > 0 {
		claims.SessionID = sessionID[0]
	}
	// sessionID is copied into claims.SessionID above
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
