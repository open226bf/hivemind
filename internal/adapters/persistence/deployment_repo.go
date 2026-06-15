package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/deployment"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

type DeploymentRepository struct{ db *gorm.DB }

func NewDeploymentRepository(db *gorm.DB) *DeploymentRepository {
	return &DeploymentRepository{db: db}
}

func (r *DeploymentRepository) Save(ctx context.Context, d *deployment.Deployment) error {
	if err := r.db.WithContext(ctx).Create(deploymentToModel(d)).Error; err != nil {
		return fmt.Errorf("save deployment: %w", err)
	}
	return nil
}

func (r *DeploymentRepository) FindByID(ctx context.Context, id uuid.UUID) (*deployment.Deployment, error) {
	var m deploymentModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find deployment: %w", err)
	}
	return deploymentToDomain(&m), nil
}

// FindActiveByServiceID returns the in-progress deployment for a service, if any.
func (r *DeploymentRepository) FindActiveByServiceID(ctx context.Context, serviceID uuid.UUID) (*deployment.Deployment, error) {
	var m deploymentModel
	err := r.db.WithContext(ctx).
		Where("service_id = ? AND status IN ?", serviceID.String(),
			[]string{string(deployment.StatusPending), string(deployment.StatusInProgress)}).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find active deployment: %w", err)
	}
	return deploymentToDomain(&m), nil
}

func (r *DeploymentRepository) ListByServiceID(ctx context.Context, serviceID uuid.UUID, p pagination.Page) ([]*deployment.Deployment, int64, error) {
	return r.list(ctx, ports.DeploymentFilter{ServiceID: &serviceID}, p)
}

func (r *DeploymentRepository) List(ctx context.Context, filter ports.DeploymentFilter, p pagination.Page) ([]*deployment.Deployment, int64, error) {
	return r.list(ctx, filter, p)
}

func (r *DeploymentRepository) list(ctx context.Context, filter ports.DeploymentFilter, p pagination.Page) ([]*deployment.Deployment, int64, error) {
	var models []deploymentModel
	var count int64

	q := r.db.WithContext(ctx).Model(&deploymentModel{})
	if filter.ServiceID != nil {
		q = q.Where("service_id = ?", filter.ServiceID.String())
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.From != nil {
		q = q.Where("started_at >= ?", *filter.From)
	}
	if filter.To != nil {
		q = q.Where("started_at <= ?", *filter.To)
	}

	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count deployments: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("started_at DESC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list deployments: %w", err)
	}

	out := make([]*deployment.Deployment, 0, len(models))
	for i := range models {
		out = append(out, deploymentToDomain(&models[i]))
	}
	return out, count, nil
}

func (r *DeploymentRepository) Update(ctx context.Context, d *deployment.Deployment) error {
	if err := r.db.WithContext(ctx).Save(deploymentToModel(d)).Error; err != nil {
		return fmt.Errorf("update deployment: %w", err)
	}
	return nil
}

// FailOrphaned marks all pending/in_progress deployments as failed. This is
// called at server startup to clean up deployments whose convergence goroutine
// was killed by a previous shutdown.
func (r *DeploymentRepository) FailOrphaned(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&deploymentModel{}).
		Where("status IN ?", []string{string(deployment.StatusPending), string(deployment.StatusInProgress)}).
		Updates(map[string]any{
			"status":        string(deployment.StatusFailed),
			"error_message": "server restarted while deployment was in progress",
			"finished_at":   time.Now().UTC(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("fail orphaned deployments: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func deploymentToModel(d *deployment.Deployment) *deploymentModel {
	var userID *string
	if d.UserID != nil {
		s := d.UserID.String()
		userID = &s
	}
	return &deploymentModel{
		ID:             d.ID.String(),
		ServiceID:      d.ServiceID.String(),
		UserID:         userID,
		ImageTag:       d.ImageTag,
		Trigger:        string(d.Trigger),
		Status:         string(d.Status),
		ErrorMessage:   d.ErrorMessage,
		ConfigSnapshot: []byte(d.ConfigSnapshot),
		StartedAt:      d.StartedAt,
		FinishedAt:     d.FinishedAt,
	}
}

func deploymentToDomain(m *deploymentModel) *deployment.Deployment {
	id, _ := uuid.Parse(m.ID)
	svcID, _ := uuid.Parse(m.ServiceID)

	var userID *uuid.UUID
	if m.UserID != nil {
		uid, _ := uuid.Parse(*m.UserID)
		userID = &uid
	}

	return &deployment.Deployment{
		ID:             id,
		ServiceID:      svcID,
		UserID:         userID,
		ImageTag:       m.ImageTag,
		Trigger:        deployment.Trigger(m.Trigger),
		Status:         deployment.Status(m.Status),
		ErrorMessage:   m.ErrorMessage,
		ConfigSnapshot: m.ConfigSnapshot,
		StartedAt:      m.StartedAt,
		FinishedAt:     m.FinishedAt,
	}
}
