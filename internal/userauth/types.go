// Package userauth implements wallet-backed user identity and revocable
// PostgreSQL sessions. It is intentionally separate from operator API keys and
// request quotas in internal/auth.
package userauth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// SessionCookieName is the only browser Cookie understood by the user
	// authentication boundary.
	SessionCookieName = "etherview_session"

	opaqueValueBytes = 32
	opaqueValueChars = 43
)

var (
	// ErrChallengeInvalid is the common class for challenge failures. Expired
	// and consumed values retain stable sub-errors for the HTTP contract while
	// callers can still match the common class.
	ErrChallengeInvalid  = errors.New("authentication challenge is invalid")
	ErrChallengeExpired  = fmt.Errorf("%w: expired", ErrChallengeInvalid)
	ErrChallengeConsumed = fmt.Errorf("%w: consumed", ErrChallengeInvalid)
	ErrSignatureInvalid  = errors.New("wallet signature is invalid")
	ErrSessionInvalid    = errors.New("user session is invalid")
	ErrCSRFInvalid       = errors.New("CSRF token is invalid")
	ErrUserDisabled      = errors.New("user is disabled")
	ErrUserNotFound      = errors.New("user not found")
	ErrInvalidInput      = errors.New("user authentication input is invalid")

	errStoredStateInvalid = errors.New("stored user authentication state is invalid")
)

type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

func (role Role) valid() bool {
	return role == RoleUser || role == RoleAdmin
}

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

func (status Status) valid() bool {
	return status == StatusActive || status == StatusDisabled
}

type User struct {
	ID          string
	ChainID     uint64
	Address     string
	DisplayName *string
	Role        Role
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
}

type Challenge struct {
	ID         string
	ChainID    uint64
	Address    string
	Message    string
	Nonce      string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

type Session struct {
	ID         string
	User       User
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time

	csrfDigest [opaqueValueBytes]byte
}

// Credentials contains the only plaintext session material. Callers may
// return it to the browser once but must never persist or log it.
type Credentials struct {
	Token     string
	CSRFToken string
}

type LoginResult struct {
	Session     Session
	Credentials Credentials

	csrfValue [opaqueValueBytes]byte
	valid     bool
}

func (result LoginResult) Authentication() Authentication {
	return Authentication{
		Session: result.Session, CSRFToken: result.Credentials.CSRFToken,
		csrfValue: result.csrfValue, valid: result.valid,
	}
}

type Authentication struct {
	Session   Session
	CSRFToken string

	csrfValue [opaqueValueBytes]byte
	valid     bool
}

// ValidateCSRF checks a single session-bound token in constant time.
func (authentication Authentication) ValidateCSRF(presented string) error {
	value, err := decodeOpaqueValue(presented)
	if !authentication.valid || err != nil ||
		!constantTimeEqual(value[:], authentication.csrfValue[:]) {
		return ErrCSRFInvalid
	}
	return nil
}

// SessionMaterial is handed to persistence only after a challenge signature
// succeeds. It contains digests, never browser credentials.
type SessionMaterial struct {
	ID          string
	TokenDigest [opaqueValueBytes]byte
	CSRFDigest  [opaqueValueBytes]byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// AuthenticationRepository is the writer-authoritative persistence contract
// needed by Service.
type AuthenticationRepository interface {
	CreateChallenge(context.Context, Challenge) (Challenge, error)
	Challenge(context.Context, string) (Challenge, error)
	CompleteLogin(context.Context, Challenge, string, SessionMaterial) (Session, error)
	ActiveSession(context.Context, [opaqueValueBytes]byte, time.Time, time.Time) (Session, error)
	RevokeSession(context.Context, [opaqueValueBytes]byte, time.Time) (bool, error)
}

type UserPageAfter struct {
	CreatedAt time.Time
	ID        string
}

type AdminUserUpdate struct {
	Role   *Role
	Status *Status
}

type AdminUserUpdateResult struct {
	User            User
	RevokedSessions uint64
}

type CleanupResult struct {
	Challenges int64
	Sessions   int64
}
