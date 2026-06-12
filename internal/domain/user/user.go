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

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	Role         Role
	Active       bool
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

func (r Role) IsValid() bool {
	return r == RoleAdmin || r == RoleOperator || r == RoleViewer
}

func (u *User) IsAdmin() bool    { return u.Role == RoleAdmin }
func (u *User) IsOperator() bool { return u.Role == RoleOperator || u.Role == RoleAdmin }
func (u *User) CanView() bool    { return u.Active }
