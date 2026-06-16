package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/template"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

type TemplateRepository struct{ db *gorm.DB }

func NewTemplateRepository(db *gorm.DB) *TemplateRepository { return &TemplateRepository{db: db} }

func (r *TemplateRepository) Save(ctx context.Context, t *template.Template) error {
	m, err := templateToModel(t)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: template name %q already exists", domainerrors.ErrConflict, t.Name)
		}
		return fmt.Errorf("save template: %w", err)
	}
	return nil
}

func (r *TemplateRepository) FindByID(ctx context.Context, id uuid.UUID) (*template.Template, error) {
	var m templateModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find template: %w", err)
	}
	return templateToDomain(&m)
}

func (r *TemplateRepository) List(ctx context.Context, p pagination.Page) ([]*template.Template, int64, error) {
	var models []templateModel
	var count int64

	q := r.db.WithContext(ctx).Model(&templateModel{})
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count templates: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list templates: %w", err)
	}

	out := make([]*template.Template, 0, len(models))
	for i := range models {
		t, err := templateToDomain(&models[i])
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, count, nil
}

func (r *TemplateRepository) Update(ctx context.Context, t *template.Template) error {
	m, err := templateToModel(t)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Save(m).Error; err != nil {
		return fmt.Errorf("update template: %w", err)
	}
	return nil
}

func (r *TemplateRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&templateModel{})
	if res.Error != nil {
		return fmt.Errorf("delete template: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func templateToModel(t *template.Template) (*templateModel, error) {
	specJSON, err := json.Marshal(t.Spec)
	if err != nil {
		return nil, fmt.Errorf("marshal template spec: %w", err)
	}
	return &templateModel{
		ID:           t.ID.String(),
		Name:         t.Name,
		Description:  t.Description,
		Version:      t.Version,
		SpecJSON:     specJSON,
		LockedFields: stringSlice(t.LockedFields),
		CreatedBy:    t.CreatedBy.String(),
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}, nil
}

func templateToDomain(m *templateModel) (*template.Template, error) {
	id, _ := uuid.Parse(m.ID)
	createdBy, _ := uuid.Parse(m.CreatedBy)
	var spec template.Spec
	if len(m.SpecJSON) > 0 {
		if err := json.Unmarshal(m.SpecJSON, &spec); err != nil {
			return nil, fmt.Errorf("unmarshal template spec: %w", err)
		}
	}
	return &template.Template{
		ID:           id,
		Name:         m.Name,
		Description:  m.Description,
		Version:      m.Version,
		Spec:         spec,
		LockedFields: []string(m.LockedFields),
		CreatedBy:    createdBy,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}, nil
}
