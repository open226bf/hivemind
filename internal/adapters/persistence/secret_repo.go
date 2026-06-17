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

type SecretRepository struct {
	db     *gorm.DB
	cipher Cipher
}

func NewSecretRepository(db *gorm.DB, cipher Cipher) *SecretRepository {
	return &SecretRepository{db: db, cipher: cipher}
}

func (r *SecretRepository) Save(ctx context.Context, s *secret.Secret, v *secret.SecretVersion, value []byte) error {
	enc, err := r.cipher.Encrypt(string(value))
	if err != nil {
		return fmt.Errorf("encrypt secret value: %w", err)
	}
	vm := secretVersionToModel(v)
	vm.EncryptedValue = enc

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(secretToModel(s)).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: secret name %q already exists", domainerrors.ErrConflict, s.Name)
			}
			return fmt.Errorf("save secret: %w", err)
		}
		if err := tx.Create(vm).Error; err != nil {
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

func (r *SecretRepository) List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*secret.Secret, int64, error) {
	var models []secretModel
	var count int64

	q := scopeCluster(r.db.WithContext(ctx).Model(&secretModel{}), clusterID)
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
func (r *SecretRepository) Update(ctx context.Context, s *secret.Secret, newVersion *secret.SecretVersion, value []byte) error {
	enc, err := r.cipher.Encrypt(string(value))
	if err != nil {
		return fmt.Errorf("encrypt secret value: %w", err)
	}
	vm := secretVersionToModel(newVersion)
	vm.EncryptedValue = enc

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(secretToModel(s)).Error; err != nil {
			return fmt.Errorf("update secret: %w", err)
		}
		if err := tx.Create(vm).Error; err != nil {
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

// GetValue returns the decrypted value of the secret's current version.
func (r *SecretRepository) GetValue(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var sm secretModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&sm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find secret: %w", err)
	}

	var vm secretVersionModel
	err = r.db.WithContext(ctx).
		Where("secret_id = ? AND version = ?", sm.ID, sm.CurrentVersion).
		First(&vm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find secret version: %w", err)
	}

	plain, err := r.cipher.Decrypt(vm.EncryptedValue)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret value: %w", err)
	}
	return []byte(plain), nil
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
		ClusterID:      clusterIDColumn(s.ClusterID),
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
		ClusterID:      parseClusterID(m.ClusterID),
		Name:           m.Name,
		CurrentVersion: m.CurrentVersion,
		TargetPath:     m.TargetPath,
		Checksum:       m.Checksum,
		CreatedBy:      createdBy,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
