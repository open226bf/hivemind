package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

type UserRepository struct{ db *gorm.DB }

func NewUserRepository(db *gorm.DB) *UserRepository { return &UserRepository{db: db} }

func (r *UserRepository) Save(ctx context.Context, u *user.User) error {
	if err := r.db.WithContext(ctx).Create(userToModel(u)).Error; err != nil {
		return fmt.Errorf("save user: %w", err)
	}
	return nil
}

func (r *UserRepository) FindByID(ctx context.Context, id uuid.UUID) (*user.User, error) {
	var m userModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return userToDomain(&m)
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	var m userModel
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return userToDomain(&m)
}

func (r *UserRepository) List(ctx context.Context, p pagination.Page) ([]*user.User, int64, error) {
	var models []userModel
	var count int64

	q := r.db.WithContext(ctx).Model(&userModel{})
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}

	users := make([]*user.User, 0, len(models))
	for i := range models {
		u, err := userToDomain(&models[i])
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, count, nil
}

func (r *UserRepository) Update(ctx context.Context, u *user.User) error {
	if err := r.db.WithContext(ctx).Save(userToModel(u)).Error; err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

func (r *UserRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&userModel{})
	if res.Error != nil {
		return fmt.Errorf("delete user: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *UserRepository) CountActiveAdmins(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&userModel{}).
		Where("role = ? AND active = ?", string(user.RoleAdmin), true).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return count, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func userToModel(u *user.User) *userModel {
	return &userModel{
		ID:                  u.ID.String(),
		Email:               u.Email,
		PasswordHash:        u.PasswordHash,
		Role:                string(u.Role),
		Active:              u.Active,
		FailedLoginAttempts: u.FailedLoginAttempts,
		LockedUntil:         u.LockedUntil,
		CreatedAt:           u.CreatedAt,
		UpdatedAt:           u.UpdatedAt,
	}
}

func userToDomain(m *userModel) (*user.User, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return nil, fmt.Errorf("parse user id: %w", err)
	}
	return &user.User{
		ID:                  id,
		Email:               m.Email,
		PasswordHash:        m.PasswordHash,
		Role:                user.Role(m.Role),
		Active:              m.Active,
		FailedLoginAttempts: m.FailedLoginAttempts,
		LockedUntil:         m.LockedUntil,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}, nil
}
