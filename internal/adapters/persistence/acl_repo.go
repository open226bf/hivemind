package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/orange/hivemind/internal/domain/acl"
	"github.com/orange/hivemind/pkg/domainerrors"
)

type AclRepository struct{ db *gorm.DB }

func NewAclRepository(db *gorm.DB) *AclRepository { return &AclRepository{db: db} }

// Save upserts on the (subject_id, resource_type, resource_id) unique key so a
// repeated grant updates the verb/expiry instead of failing on a duplicate.
func (r *AclRepository) Save(ctx context.Context, g *acl.Grant) error {
	m := aclToModel(g)
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "subject_id"}, {Name: "resource_type"}, {Name: "resource_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"verb", "created_by", "created_at", "expires_at"}),
	}).Create(m).Error
	if err != nil {
		return fmt.Errorf("save acl grant: %w", err)
	}
	return nil
}

func (r *AclRepository) FindByID(ctx context.Context, id uuid.UUID) (*acl.Grant, error) {
	var m aclGrantModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find acl grant: %w", err)
	}
	return aclToDomain(&m)
}

func (r *AclRepository) DeleteByID(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&aclGrantModel{})
	if res.Error != nil {
		return fmt.Errorf("delete acl grant: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *AclRepository) ListBySubject(ctx context.Context, subjectID uuid.UUID) ([]*acl.Grant, error) {
	var models []aclGrantModel
	if err := r.db.WithContext(ctx).
		Where("subject_id = ?", subjectID.String()).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list acl grants by subject: %w", err)
	}
	return aclToDomainSlice(models)
}

func (r *AclRepository) ListByResource(ctx context.Context, rt acl.ResourceType, resourceID uuid.UUID) ([]*acl.Grant, error) {
	var models []aclGrantModel
	if err := r.db.WithContext(ctx).
		Where("resource_type = ? AND resource_id = ?", string(rt), resourceID.String()).
		Order("created_at ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list acl grants by resource: %w", err)
	}
	return aclToDomainSlice(models)
}

func (r *AclRepository) DeleteByResource(ctx context.Context, rt acl.ResourceType, resourceID uuid.UUID) error {
	err := r.db.WithContext(ctx).
		Where("resource_type = ? AND resource_id = ?", string(rt), resourceID.String()).
		Delete(&aclGrantModel{}).Error
	if err != nil {
		return fmt.Errorf("delete acl grants by resource: %w", err)
	}
	return nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func aclToModel(g *acl.Grant) *aclGrantModel {
	m := &aclGrantModel{
		ID:           g.ID.String(),
		SubjectID:    g.SubjectID.String(),
		ResourceType: string(g.ResourceType),
		ResourceID:   g.ResourceID.String(),
		Verb:         string(g.Verb),
		CreatedAt:    g.CreatedAt,
		ExpiresAt:    g.ExpiresAt,
	}
	if g.CreatedBy != uuid.Nil {
		m.CreatedBy = g.CreatedBy.String()
	}
	return m
}

func aclToDomain(m *aclGrantModel) (*acl.Grant, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return nil, fmt.Errorf("parse acl grant id: %w", err)
	}
	subject, err := uuid.Parse(m.SubjectID)
	if err != nil {
		return nil, fmt.Errorf("parse acl subject id: %w", err)
	}
	resource, err := uuid.Parse(m.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("parse acl resource id: %w", err)
	}
	var createdBy uuid.UUID
	if m.CreatedBy != "" {
		createdBy, _ = uuid.Parse(m.CreatedBy)
	}
	return &acl.Grant{
		ID:           id,
		SubjectID:    subject,
		ResourceType: acl.ResourceType(m.ResourceType),
		ResourceID:   resource,
		Verb:         acl.Verb(m.Verb),
		CreatedBy:    createdBy,
		CreatedAt:    m.CreatedAt,
		ExpiresAt:    m.ExpiresAt,
	}, nil
}

func aclToDomainSlice(models []aclGrantModel) ([]*acl.Grant, error) {
	out := make([]*acl.Grant, 0, len(models))
	for i := range models {
		g, err := aclToDomain(&models[i])
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}
