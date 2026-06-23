package user

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

var ErrInvalidRole = errors.New("invalid role")

// Account lockout policy (F-MVP-01): 5 consecutive failures lock for 15 min.
const (
	MaxFailedLogins = 5
	LockoutDuration = 15 * time.Minute
)

type User struct {
	ID                  uuid.UUID
	Email               string
	PasswordHash        string
	Role                Role
	Active              bool
	FailedLoginAttempts int
	LockedUntil         *time.Time
	// TokenVersion is the revocation epoch embedded in issued access tokens and
	// bumped whenever the user's effective access changes (e.g. an ACL grant is
	// added or revoked). The Auth middleware rejects a token whose version is
	// stale, making revocation immediate (ADR 0003).
	TokenVersion int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func New(email, passwordHash string, role Role) (*User, error) {
	if !role.IsValid() {
		return nil, ErrInvalidRole
	}
	return &User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		Active:       true,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}, nil
}

// IsLocked reports whether the account is currently locked at the given time.
func (u *User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

// RecordFailedLogin increments the failure counter and locks the account once
// the threshold is reached.
func (u *User) RecordFailedLogin(now time.Time) {
	u.FailedLoginAttempts++
	if u.FailedLoginAttempts >= MaxFailedLogins {
		until := now.Add(LockoutDuration)
		u.LockedUntil = &until
	}
	u.UpdatedAt = now
}

// ResetFailedLogins clears the failure counter and any active lock after a
// successful authentication.
func (u *User) ResetFailedLogins() {
	u.FailedLoginAttempts = 0
	u.LockedUntil = nil
	u.UpdatedAt = time.Now().UTC()
}

func (r Role) IsValid() bool {
	return r == RoleAdmin || r == RoleOperator || r == RoleViewer
}

func (u *User) IsAdmin() bool    { return u.Role == RoleAdmin }
func (u *User) IsOperator() bool { return u.Role == RoleOperator || u.Role == RoleAdmin }
func (u *User) CanView() bool    { return u.Active }

// BumpTokenVersion advances the revocation epoch, invalidating every access
// token issued before this point.
func (u *User) BumpTokenVersion() {
	u.TokenVersion++
	u.UpdatedAt = time.Now().UTC()
}
