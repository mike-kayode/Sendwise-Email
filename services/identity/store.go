package identity

import (
	"context"
	"errors"
	"time"
)

// Store is the Identity service data-access contract. The pgx implementation
// owns the identity schema; tests use an in-memory fake.
type Store interface {
	Ping(ctx context.Context) error
	Close()

	CreateUserWithRegistration(ctx context.Context, email, passwordHash, name, organizationName, correlationID string) (*User, error)
	CreateUser(ctx context.Context, email, passwordHash, name string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByID(ctx context.Context, id string) (*User, error)
	SetPassword(ctx context.Context, userID, passwordHash string) error
	RecordLogin(ctx context.Context, userID string) error

	CreateVerificationToken(ctx context.Context, userID, email, tokenHash, purpose string, expiresAt time.Time) error
	ConsumeVerificationToken(ctx context.Context, tokenHash, purpose string) (userID, email string, err error)
	MarkEmailVerified(ctx context.Context, userID, email, correlationID string) error

	CreateSession(ctx context.Context, params SessionCreate) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	// LookupRefreshToken resolves a presented refresh token. reused=true means
	// the hash matches a superseded token, which indicates replay/theft.
	LookupRefreshToken(ctx context.Context, tokenHash string) (session *Session, reused bool, err error)
	RotateRefreshToken(ctx context.Context, sessionID, presentedHash, newHash string, expiresAt time.Time) error
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeSessionByToken(ctx context.Context, tokenHash string) error
	RevokeAllUserSessions(ctx context.Context, userID string) error

	WriteAuthAudit(ctx context.Context, userID *string, action, ipAddress, userAgent string, metadata map[string]any) error
}

// Domain errors shared by implementations.
var (
	ErrNotFound       = errors.New("not found")
	ErrEmailInUse     = errors.New("email already registered")
	ErrInvalidToken   = errors.New("invalid or expired token")
	ErrRefreshReuse   = errors.New("refresh token reuse detected")
	ErrTokenConsumed  = errors.New("token already used")
	ErrNoOrganization = errors.New("user has no organization membership")
)

// User is an identity account. PasswordHash never leaves the service.
type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	PasswordHash    string     `json:"-"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Session is a refresh-token session with its organization snapshot.
type Session struct {
	ID                       string
	UserID                   string
	OrganizationID           string
	OrganizationName         string
	OrganizationSlug         string
	Role                     string
	RefreshTokenHash         string
	PreviousRefreshTokenHash string
	ExpiresAt                time.Time
	RevokedAt                *time.Time
}

// SessionCreate parameters for CreateSession.
type SessionCreate struct {
	UserID           string
	OrganizationID   string
	OrganizationName string
	OrganizationSlug string
	Role             string
	DeviceInfo       string
	IPAddress        string
	UserAgent        string
	RefreshTokenHash string
	ExpiresAt        time.Time
}
