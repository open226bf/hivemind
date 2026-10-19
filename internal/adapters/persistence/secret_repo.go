package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
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

	return dbFor(ctx, r.db).Transaction(func(tx *gorm.DB) error {
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
	err := dbFor(ctx, r.db).Where("id = ?", id.String()).First(&m).Error
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

	q := scopeACL(scopeCluster(dbFor(ctx, r.db).Model(&secretModel{}), clusterID), "")
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

	return dbFor(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(secretToModel(s)).Error; err != nil {
			return fmt.Errorf("update secret: %w", err)
		}
		if err := tx.Create(vm).Error; err != nil {
			return fmt.Errorf("save secret version: %w", err)
		}
		return nil
	})
}

// SetVersionSwarmID records the id the orchestrator assigned to a secret
// version. Each version is its own Swarm object (secrets are immutable there),
// so the id is stored on the version rather than the secret.
func (r *SecretRepository) SetVersionSwarmID(ctx context.Context, secretID uuid.UUID, version int, swarmID string) error {
	res := dbFor(ctx, r.db).Model(&secretVersionModel{}).
		Where("secret_id = ? AND version = ?", secretID.String(), version).
		Update("swarm_secret_id", swarmID)
	if res.Error != nil {
		return fmt.Errorf("set secret version swarm id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

// ListVersionSwarmIDs returns the orchestrator id of every version of a secret,
// so deleting the secret can remove the objects it owns on the cluster. Versions
// created before secrets were materialised up front have an empty id.
func (r *SecretRepository) ListVersionSwarmIDs(ctx context.Context, secretID uuid.UUID) ([]string, error) {
	var ids []string
	if err := dbFor(ctx, r.db).Model(&secretVersionModel{}).
		Where("secret_id = ?", secretID.String()).
		Order("version ASC").Pluck("swarm_secret_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list secret version swarm ids: %w", err)
	}
	return ids, nil
}

func (r *SecretRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return dbFor(ctx, r.db).Transaction(func(tx *gorm.DB) error {
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
	err := dbFor(ctx, r.db).Where("id = ?", id.String()).First(&sm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find secret: %w", err)
	}

	var vm secretVersionModel
	err = dbFor(ctx, r.db).
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
	if err := dbFor(ctx, r.db).Model(&serviceSecretModel{}).
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
