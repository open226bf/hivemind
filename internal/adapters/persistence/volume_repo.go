package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/volume"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

type VolumeRepository struct{ db *gorm.DB }

func NewVolumeRepository(db *gorm.DB) *VolumeRepository { return &VolumeRepository{db: db} }

func (r *VolumeRepository) Save(ctx context.Context, v *volume.Volume) error {
	if err := r.db.WithContext(ctx).Create(volumeToModel(v)).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: volume name %q already exists", domainerrors.ErrConflict, v.Name)
		}
		return fmt.Errorf("save volume: %w", err)
	}
	return nil
}

func (r *VolumeRepository) FindByID(ctx context.Context, id uuid.UUID) (*volume.Volume, error) {
	var m volumeModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find volume: %w", err)
	}
	return volumeToDomain(&m), nil
}

func (r *VolumeRepository) FindByName(ctx context.Context, name string) (*volume.Volume, error) {
	var m volumeModel
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find volume by name: %w", err)
	}
	return volumeToDomain(&m), nil
}

func (r *VolumeRepository) List(ctx context.Context, p pagination.Page) ([]*volume.Volume, int64, error) {
	var models []volumeModel
	var count int64

	q := r.db.WithContext(ctx).Model(&volumeModel{})
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count volumes: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list volumes: %w", err)
	}

	out := make([]*volume.Volume, 0, len(models))
	for i := range models {
		out = append(out, volumeToDomain(&models[i]))
	}
	return out, count, nil
}

func (r *VolumeRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&volumeModel{})
	if res.Error != nil {
		return fmt.Errorf("delete volume: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func volumeToModel(v *volume.Volume) *volumeModel {
	return &volumeModel{
		ID:        v.ID.String(),
		ClusterID: clusterIDColumn(v.ClusterID),
		Name:      v.Name,
		Driver:    v.Driver,
		CreatedAt: v.CreatedAt,
	}
}

func volumeToDomain(m *volumeModel) *volume.Volume {
	id, _ := uuid.Parse(m.ID)
	return &volume.Volume{
		ID:        id,
		ClusterID: parseClusterID(m.ClusterID),
		Name:      m.Name,
		Driver:    m.Driver,
		CreatedAt: m.CreatedAt,
	}
}
