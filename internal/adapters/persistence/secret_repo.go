package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/secret"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

type SecretRepository struct{ db *gorm.DB }

func NewSecretRepository(db *gorm.DB) *SecretRepository { return &SecretRepository{db: db} }

func (r *SecretRepository) Save(ctx context.Context, s *secret.Secret, v *secret.SecretVersion) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(secretToModel(s)).Error; err != nil {
			return fmt.Errorf("save secret: %w", err)
		}
		if err := tx.Create(secretVersionToModel(v)).Error; err != nil {
			return fmt.Errorf("save secret version: %w", err)
		}
		return nil
	})
}

func (r *SecretRepository) FindByID(ctx context.Context, id uuid.UUID) (*secret.Secret, error) {
	var m secretModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find secret: %w", err)
	}
	return secretToDomain(&m), nil
}

func (r *SecretRepository) List(ctx context.Context, p pagination.Page) ([]*secret.Secret, int64, error) {
	var models []secretModel
	var count int64

	q := r.db.WithContext(ctx).Model(&secretModel{})
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count secrets: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list secrets: %w", err)
	}

	out := make([]*secret.Secret, 0, len(models))
	for i := range models {
		out = append(out, secretToDomain(&models[i]))
	}
	return out, count, nil
}

// Update rotates a secret: updates the parent record and inserts the new version.
func (r *SecretRepository) Update(ctx context.Context, s *secret.Secret, newVersion *secret.SecretVersion) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(secretToModel(s)).Error; err != nil {
			return fmt.Errorf("update secret: %w", err)
		}
		if err := tx.Create(secretVersionToModel(newVersion)).Error; err != nil {
			return fmt.Errorf("save secret version: %w", err)
		}
		return nil
	})
}

func (r *SecretRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sid := id.String()
		if err := tx.Where("secret_id = ?", sid).Delete(&secretVersionModel{}).Error; err != nil {
			return fmt.Errorf("delete secret versions: %w", err)
		}
		res := tx.Where("id = ?", sid).Delete(&secretModel{})
		if res.Error != nil {
			return fmt.Errorf("delete secret: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return domainerrors.ErrNotFound
		}
		return nil
	})
}

func (r *SecretRepository) IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&serviceSecretModel{}).
		Where("secret_id = ?", id.String()).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check secret attachment: %w", err)
	}
	return count > 0, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func secretToModel(s *secret.Secret) *secretModel {
	return &secretModel{
		ID:             s.ID.String(),
		Name:           s.Name,
		CurrentVersion: s.CurrentVersion,
		TargetPath:     s.TargetPath,
		Checksum:       s.Checksum,
		CreatedBy:      s.CreatedBy.String(),
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

func secretVersionToModel(v *secret.SecretVersion) *secretVersionModel {
	return &secretVersionModel{
		ID:            v.ID.String(),
		SecretID:      v.SecretID.String(),
		Version:       v.Version,
		SwarmSecretID: v.SwarmSecretID,
		Checksum:      v.Checksum,
		CreatedAt:     v.CreatedAt,
	}
}

func secretToDomain(m *secretModel) *secret.Secret {
	id, _ := uuid.Parse(m.ID)
	createdBy, _ := uuid.Parse(m.CreatedBy)
	return &secret.Secret{
		ID:             id,
		Name:           m.Name,
		CurrentVersion: m.CurrentVersion,
		TargetPath:     m.TargetPath,
		Checksum:       m.Checksum,
		CreatedBy:      createdBy,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
