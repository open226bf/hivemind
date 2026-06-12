package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/auditlog"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/pagination"
)

type AuditLogRepository struct{ db *gorm.DB }

func NewAuditLogRepository(db *gorm.DB) *AuditLogRepository { return &AuditLogRepository{db: db} }

func (r *AuditLogRepository) Save(ctx context.Context, log *auditlog.AuditLog) error {
	if err := r.db.WithContext(ctx).Create(auditLogToModel(log)).Error; err != nil {
		return fmt.Errorf("save audit log: %w", err)
	}
	return nil
}

func (r *AuditLogRepository) List(ctx context.Context, filter ports.AuditLogFilter, p pagination.Page) ([]*auditlog.AuditLog, int64, error) {
	var models []auditLogModel
	var count int64

	q := r.db.WithContext(ctx).Model(&auditLogModel{})
	if filter.UserID != nil {
		q = q.Where("user_id = ?", filter.UserID.String())
	}
	if filter.ResourceType != "" {
		q = q.Where("resource_type = ?", filter.ResourceType)
	}
	if filter.From != nil {
		q = q.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		q = q.Where("created_at <= ?", *filter.To)
	}

	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}

	out := make([]*auditlog.AuditLog, 0, len(models))
	for i := range models {
		out = append(out, auditLogToDomain(&models[i]))
	}
	return out, count, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func auditLogToModel(a *auditlog.AuditLog) *auditLogModel {
	var userID *string
	if a.UserID != nil {
		s := a.UserID.String()
		userID = &s
	}
	return &auditLogModel{
		ID:           a.ID.String(),
		UserID:       userID,
		Action:       a.Action,
		ResourceType: a.ResourceType,
		ResourceID:   a.ResourceID,
		Payload:      []byte(a.Payload),
		IP:           a.IP,
		CreatedAt:    a.CreatedAt,
	}
}

func auditLogToDomain(m *auditLogModel) *auditlog.AuditLog {
	id, _ := uuid.Parse(m.ID)
	var userID *uuid.UUID
	if m.UserID != nil {
		uid, _ := uuid.Parse(*m.UserID)
		userID = &uid
	}
	return &auditlog.AuditLog{
		ID:           id,
		UserID:       userID,
		Action:       m.Action,
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
		Payload:      m.Payload,
		IP:           m.IP,
		CreatedAt:    m.CreatedAt,
	}
}
