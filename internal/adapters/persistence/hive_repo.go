package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/hive"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

type HiveRepository struct{ db *gorm.DB }

func NewHiveRepository(db *gorm.DB) *HiveRepository { return &HiveRepository{db: db} }

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

func (r *HiveRepository) List(ctx context.Context, p pagination.Page) ([]*hive.Hive, int64, error) {
	var models []hiveModel
	var count int64

	q := r.db.WithContext(ctx).Model(&hiveModel{})
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

// ─── Mappers ──────────────────────────────────────────────────────────────────

func hiveToModel(h *hive.Hive) *hiveModel {
	return &hiveModel{
		ID:          h.ID.String(),
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
		Name:        m.Name,
		Description: m.Description,
		Color:       m.Color,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}
