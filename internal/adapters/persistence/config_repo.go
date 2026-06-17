package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

type ConfigRepository struct{ db *gorm.DB }

func NewConfigRepository(db *gorm.DB) *ConfigRepository { return &ConfigRepository{db: db} }

func (r *ConfigRepository) Save(ctx context.Context, c *config.Config, v *config.ConfigVersion) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(configToModel(c)).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				return fmt.Errorf("%w: config name %q already exists", domainerrors.ErrConflict, c.Name)
			}
			return fmt.Errorf("save config: %w", err)
		}
		if err := tx.Create(configVersionToModel(v)).Error; err != nil {
			return fmt.Errorf("save config version: %w", err)
		}
		return nil
	})
}

func (r *ConfigRepository) FindByID(ctx context.Context, id uuid.UUID) (*config.Config, error) {
	var m configModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find config: %w", err)
	}
	return configToDomain(&m), nil
}

func (r *ConfigRepository) ListVersions(ctx context.Context, configID uuid.UUID) ([]*config.ConfigVersion, error) {
	var models []configVersionModel
	if err := r.db.WithContext(ctx).
		Where("config_id = ?", configID.String()).
		Order("version DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list config versions: %w", err)
	}
	out := make([]*config.ConfigVersion, 0, len(models))
	for i := range models {
		out = append(out, configVersionToDomain(&models[i]))
	}
	return out, nil
}

func (r *ConfigRepository) List(ctx context.Context, p pagination.Page) ([]*config.Config, int64, error) {
	var models []configModel
	var count int64

	q := r.db.WithContext(ctx).Model(&configModel{})
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count configs: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list configs: %w", err)
	}

	out := make([]*config.Config, 0, len(models))
	for i := range models {
		out = append(out, configToDomain(&models[i]))
	}
	return out, count, nil
}

// Update adds a new version and bumps the config's current_version.
func (r *ConfigRepository) Update(ctx context.Context, c *config.Config, newVersion *config.ConfigVersion) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(configToModel(c)).Error; err != nil {
			return fmt.Errorf("update config: %w", err)
		}
		if err := tx.Create(configVersionToModel(newVersion)).Error; err != nil {
			return fmt.Errorf("save config version: %w", err)
		}
		return nil
	})
}

func (r *ConfigRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		cid := id.String()
		if err := tx.Where("config_id = ?", cid).Delete(&configVersionModel{}).Error; err != nil {
			return fmt.Errorf("delete config versions: %w", err)
		}
		res := tx.Where("id = ?", cid).Delete(&configModel{})
		if res.Error != nil {
			return fmt.Errorf("delete config: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return domainerrors.ErrNotFound
		}
		return nil
	})
}

func (r *ConfigRepository) IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&serviceConfigModel{}).
		Where("config_id = ?", id.String()).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check config attachment: %w", err)
	}
	return count > 0, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func configToModel(c *config.Config) *configModel {
	return &configModel{
		ID:             c.ID.String(),
		ClusterID:      clusterIDColumn(c.ClusterID),
		Name:           c.Name,
		TargetPath:     c.TargetPath,
		CurrentVersion: c.CurrentVersion,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

func configVersionToModel(v *config.ConfigVersion) *configVersionModel {
	createdBy := ""
	if v.CreatedBy != uuid.Nil {
		createdBy = v.CreatedBy.String()
	}
	return &configVersionModel{
		ID:            v.ID.String(),
		ConfigID:      v.ConfigID.String(),
		Version:       v.Version,
		Content:       v.Content,
		SwarmConfigID: v.SwarmConfigID,
		Comment:       v.Comment,
		CreatedBy:     createdBy,
		CreatedAt:     v.CreatedAt,
	}
}

func configToDomain(m *configModel) *config.Config {
	id, _ := uuid.Parse(m.ID)
	return &config.Config{
		ID:             id,
		ClusterID:      parseClusterID(m.ClusterID),
		Name:           m.Name,
		TargetPath:     m.TargetPath,
		CurrentVersion: m.CurrentVersion,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

func configVersionToDomain(m *configVersionModel) *config.ConfigVersion {
	id, _ := uuid.Parse(m.ID)
	configID, _ := uuid.Parse(m.ConfigID)
	createdBy, _ := uuid.Parse(m.CreatedBy)
	return &config.ConfigVersion{
		ID:            id,
		ConfigID:      configID,
		Version:       m.Version,
		Content:       m.Content,
		SwarmConfigID: m.SwarmConfigID,
		Comment:       m.Comment,
		CreatedBy:     createdBy,
		CreatedAt:     m.CreatedAt,
	}
}
