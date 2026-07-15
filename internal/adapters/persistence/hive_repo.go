package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/hive"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

type HiveRepository struct {
	db     *gorm.DB
	cipher Cipher
}

func NewHiveRepository(db *gorm.DB, cipher Cipher) *HiveRepository {
	return &HiveRepository{db: db, cipher: cipher}
}

func (r *HiveRepository) Save(ctx context.Context, h *hive.Hive) error {
	if err := r.db.WithContext(ctx).Create(hiveToModel(h)).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: hive name %q already exists", domainerrors.ErrConflict, h.Name)
		}
		return fmt.Errorf("save hive: %w", err)
	}
	return nil
}

func (r *HiveRepository) FindByID(ctx context.Context, id uuid.UUID) (*hive.Hive, error) {
	var m hiveModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find hive: %w", err)
	}
	return hiveToDomain(&m), nil
}

func (r *HiveRepository) List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*hive.Hive, int64, error) {
	var models []hiveModel
	var count int64

	q := scopeACL(scopeCluster(r.db.WithContext(ctx).Model(&hiveModel{}), clusterID), "id")
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count hives: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list hives: %w", err)
	}

	out := make([]*hive.Hive, 0, len(models))
	for i := range models {
		out = append(out, hiveToDomain(&models[i]))
	}
	return out, count, nil
}

func (r *HiveRepository) Update(ctx context.Context, h *hive.Hive) error {
	if err := r.db.WithContext(ctx).Save(hiveToModel(h)).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: hive name %q already exists", domainerrors.ErrConflict, h.Name)
		}
		return fmt.Errorf("update hive: %w", err)
	}
	return nil
}

func (r *HiveRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&hiveModel{})
	if res.Error != nil {
		return fmt.Errorf("delete hive: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

// ─── Env vars (hive-global) ───────────────────────────────────────────────────

// SetEnvVars atomically replaces the hive's global env vars. Secret values are
// encrypted at rest, mirroring service env vars.
func (r *HiveRepository) SetEnvVars(ctx context.Context, hiveID uuid.UUID, vars []hive.EnvVar) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("hive_id = ?", hiveID.String()).Delete(&hiveEnvVarModel{}).Error; err != nil {
			return fmt.Errorf("clear hive env vars: %w", err)
		}
		if len(vars) == 0 {
			return nil
		}
		models := make([]hiveEnvVarModel, 0, len(vars))
		for _, v := range vars {
			val := v.Value
			if v.IsSecret {
				enc, err := r.cipher.Encrypt(v.Value)
				if err != nil {
					return fmt.Errorf("encrypt hive env var %s: %w", v.Key, err)
				}
				val = enc
			}
			models = append(models, hiveEnvVarModel{
				ID:       v.ID.String(),
				HiveID:   hiveID.String(),
				Key:      v.Key,
				Value:    val,
				IsSecret: v.IsSecret,
			})
		}
		return tx.Create(&models).Error
	})
}

func (r *HiveRepository) GetEnvVars(ctx context.Context, hiveID uuid.UUID) ([]hive.EnvVar, error) {
	var models []hiveEnvVarModel
	if err := r.db.WithContext(ctx).
		Where("hive_id = ?", hiveID.String()).
		Order("key ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get hive env vars: %w", err)
	}

	vars := make([]hive.EnvVar, 0, len(models))
	for _, m := range models {
		id, _ := uuid.Parse(m.ID)
		hvID, _ := uuid.Parse(m.HiveID)
		val := m.Value
		if m.IsSecret {
			dec, err := r.cipher.Decrypt(m.Value)
			if err != nil {
				return nil, fmt.Errorf("decrypt hive env var %s: %w", m.Key, err)
			}
			val = dec
		}
		vars = append(vars, hive.EnvVar{
			ID:       id,
			HiveID:   hvID,
			Key:      m.Key,
			Value:    val,
			IsSecret: m.IsSecret,
		})
	}
	return vars, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func hiveToModel(h *hive.Hive) *hiveModel {
	return &hiveModel{
		ID:          h.ID.String(),
		ClusterID:   clusterIDColumn(h.ClusterID),
		Name:        h.Name,
		Description: h.Description,
		Color:       h.Color,
		CreatedAt:   h.CreatedAt,
		UpdatedAt:   h.UpdatedAt,
	}
}

func hiveToDomain(m *hiveModel) *hive.Hive {
	id, _ := uuid.Parse(m.ID)
	return &hive.Hive{
		ID:          id,
		ClusterID:   parseClusterID(m.ClusterID),
		Name:        m.Name,
		Description: m.Description,
		Color:       m.Color,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}
