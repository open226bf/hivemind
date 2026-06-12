package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/crypto"
	"github.com/orange/hivemind/pkg/domainerrors"
)

// EnsureAdmin creates the initial admin account on first boot (F-MVP-01).
// It is idempotent: if the email already exists, it does nothing.
// Returns created=true only when a new account was inserted.
func EnsureAdmin(ctx context.Context, users ports.UserRepository, email, password string) (created bool, err error) {
	if email == "" || password == "" {
		return false, errors.New("admin email and password are required for bootstrap")
	}

	_, err = users.FindByEmail(ctx, email)
	if err == nil {
		return false, nil // already exists
	}
	if !errors.Is(err, domainerrors.ErrNotFound) {
		return false, fmt.Errorf("lookup admin: %w", err)
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return false, fmt.Errorf("hash admin password: %w", err)
	}

	admin, err := user.New(email, hash, user.RoleAdmin)
	if err != nil {
		return false, err
	}
	if err := users.Save(ctx, admin); err != nil {
		return false, fmt.Errorf("save admin: %w", err)
	}
	return true, nil
}
