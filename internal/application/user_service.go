package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/crypto"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

var (
	// ErrLastAdmin guards the cluster from losing its last admin: the last active
	// admin cannot be deleted, demoted or deactivated (F-V1-01).
	ErrLastAdmin = errors.New("cannot remove or demote the last active admin")
	// ErrSelfDelete prevents an admin from deleting their own account.
	ErrSelfDelete = errors.New("you cannot delete your own account")
	// ErrSelfDemote prevents an admin from demoting their own account.
	ErrSelfDemote = errors.New("you cannot change your own role")
	// ErrWeakPassword is returned when a password is below the minimum length.
	ErrWeakPassword = errors.New("password must be at least 8 characters")
	// ErrEmailRequired is returned when an email is empty.
	ErrEmailRequired = errors.New("email is required")
)

const minPasswordLength = 8

// UserService implements the user management use cases behind the /users API.
type UserService struct {
	users ports.UserRepository
}

func NewUserService(users ports.UserRepository) *UserService {
	return &UserService{users: users}
}

// CreateUserInput is the data needed to create a user.
type CreateUserInput struct {
	Email    string
	Password string
	Role     user.Role
}

// UpdateUserInput carries optional changes. Nil fields are left unchanged.
type UpdateUserInput struct {
	Role     *user.Role
	Active   *bool
	Password *string
}

func (s *UserService) List(ctx context.Context, p pagination.Page) ([]*user.User, int64, error) {
	return s.users.List(ctx, p)
}

func (s *UserService) Create(ctx context.Context, in CreateUserInput) (*user.User, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	if email == "" {
		return nil, ErrEmailRequired
	}
	if len(in.Password) < minPasswordLength {
		return nil, ErrWeakPassword
	}
	if !in.Role.IsValid() {
		return nil, user.ErrInvalidRole
	}

	if _, err := s.users.FindByEmail(ctx, email); err == nil {
		return nil, fmt.Errorf("%w: email already in use", domainerrors.ErrConflict)
	} else if !errors.Is(err, domainerrors.ErrNotFound) {
		return nil, err
	}

	hash, err := crypto.HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	u, err := user.New(email, hash, in.Role)
	if err != nil {
		return nil, err
	}
	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Update changes a user's role, active state and/or password. actingUserID is
// the authenticated caller, used to forbid self-demotion (F-V1-01).
func (s *UserService) Update(ctx context.Context, actingUserID, id uuid.UUID, in UpdateUserInput) (*user.User, error) {
	u, err := s.users.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Determine whether this change removes admin privileges from the target.
	losesAdmin := u.Role == user.RoleAdmin &&
		((in.Role != nil && *in.Role != user.RoleAdmin) || (in.Active != nil && !*in.Active))

	if losesAdmin {
		if id == actingUserID && in.Role != nil && *in.Role != user.RoleAdmin {
			return nil, ErrSelfDemote
		}
		if err := s.ensureNotLastAdmin(ctx); err != nil {
			return nil, err
		}
	}

	if in.Role != nil {
		if !in.Role.IsValid() {
			return nil, user.ErrInvalidRole
		}
		u.Role = *in.Role
	}
	if in.Active != nil {
		u.Active = *in.Active
	}
	if in.Password != nil {
		if len(*in.Password) < minPasswordLength {
			return nil, ErrWeakPassword
		}
		hash, err := crypto.HashPassword(*in.Password)
		if err != nil {
			return nil, fmt.Errorf("hash password: %w", err)
		}
		u.PasswordHash = hash
	}

	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// Delete removes a user. An admin cannot delete their own account, and the last
// active admin can never be deleted (F-V1-01).
func (s *UserService) Delete(ctx context.Context, actingUserID, id uuid.UUID) error {
	if id == actingUserID {
		return ErrSelfDelete
	}
	u, err := s.users.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if u.Role == user.RoleAdmin && u.Active {
		if err := s.ensureNotLastAdmin(ctx); err != nil {
			return err
		}
	}
	return s.users.Delete(ctx, id)
}

func (s *UserService) ensureNotLastAdmin(ctx context.Context) error {
	n, err := s.users.CountActiveAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}
