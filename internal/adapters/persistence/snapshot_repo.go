package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/snapshot"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// SnapshotRepository persists service snapshots. The JSON payload — which embeds
// secret values and config contents — is encrypted as a single blob before it
// touches the database, so no sensitive material is ever stored in plaintext.
type SnapshotRepository struct {
	db     *gorm.DB
	cipher Cipher
}

func NewSnapshotRepository(db *gorm.DB, cipher Cipher) *SnapshotRepository {
	return &SnapshotRepository{db: db, cipher: cipher}
}

func (r *SnapshotRepository) Save(ctx context.Context, s *snapshot.ServiceSnapshot) error {
	raw, err := json.Marshal(s.Payload)
	if err != nil {
		return fmt.Errorf("marshal snapshot payload: %w", err)
	}
	enc, err := r.cipher.Encrypt(string(raw))
	if err != nil {
		return fmt.Errorf("encrypt snapshot payload: %w", err)
	}

	var createdBy *string
	if s.CreatedBy != nil {
		cb := s.CreatedBy.String()
		createdBy = &cb
	}

	m := &serviceSnapshotModel{
		ID:               s.ID.String(),
		ServiceID:        s.ServiceID.String(),
		Label:            s.Label,
		CreatedBy:        createdBy,
		SchemaVersion:    s.SchemaVersion,
		EncryptedPayload: enc,
		CreatedAt:        s.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

func (r *SnapshotRepository) FindByID(ctx context.Context, id uuid.UUID) (*snapshot.ServiceSnapshot, error) {
	var m serviceSnapshotModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find snapshot: %w", err)
	}
	return r.toDomain(&m)
}

func (r *SnapshotRepository) ListByServiceID(ctx context.Context, serviceID uuid.UUID, p pagination.Page) ([]*snapshot.ServiceSnapshot, int64, error) {
	var models []serviceSnapshotModel
	var count int64

	q := r.db.WithContext(ctx).Model(&serviceSnapshotModel{}).Where("service_id = ?", serviceID.String())
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count snapshots: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list snapshots: %w", err)
	}

	out := make([]*snapshot.ServiceSnapshot, 0, len(models))
	for i := range models {
		s, err := r.toDomain(&models[i])
		if err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, count, nil
}

func (r *SnapshotRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&serviceSnapshotModel{})
	if res.Error != nil {
		return fmt.Errorf("delete snapshot: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *SnapshotRepository) toDomain(m *serviceSnapshotModel) (*snapshot.ServiceSnapshot, error) {
	plain, err := r.cipher.Decrypt(m.EncryptedPayload)
	if err != nil {
		return nil, fmt.Errorf("decrypt snapshot payload: %w", err)
	}
	var payload snapshot.Payload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot payload: %w", err)
	}

	id, _ := uuid.Parse(m.ID)
	svcID, _ := uuid.Parse(m.ServiceID)
	var createdBy *uuid.UUID
	if m.CreatedBy != nil {
		cb, _ := uuid.Parse(*m.CreatedBy)
		createdBy = &cb
	}

	return &snapshot.ServiceSnapshot{
		ID:            id,
		ServiceID:     svcID,
		Label:         m.Label,
		CreatedBy:     createdBy,
		SchemaVersion: m.SchemaVersion,
		Payload:       payload,
		CreatedAt:     m.CreatedAt,
	}, nil
}

var _ ports.SnapshotRepository = (*SnapshotRepository)(nil)
