package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/orange/hivemind/internal/domain/network"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

type NetworkRepository struct{ db *gorm.DB }

func NewNetworkRepository(db *gorm.DB) *NetworkRepository { return &NetworkRepository{db: db} }

func (r *NetworkRepository) Save(ctx context.Context, n *network.Network) error {
	if err := r.db.WithContext(ctx).Create(networkToModel(n)).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: network name %q already exists", domainerrors.ErrConflict, n.Name)
		}
		return fmt.Errorf("save network: %w", err)
	}
	return nil
}

func (r *NetworkRepository) FindByID(ctx context.Context, id uuid.UUID) (*network.Network, error) {
	var m networkModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find network: %w", err)
	}
	return networkToDomain(&m), nil
}

func (r *NetworkRepository) List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*network.Network, int64, error) {
	var models []networkModel
	var count int64

	q := scopeCluster(r.db.WithContext(ctx).Model(&networkModel{}), clusterID)
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count networks: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list networks: %w", err)
	}

	out := make([]*network.Network, 0, len(models))
	for i := range models {
		out = append(out, networkToDomain(&models[i]))
	}
	return out, count, nil
}

func (r *NetworkRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&networkModel{})
	if res.Error != nil {
		return fmt.Errorf("delete network: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *NetworkRepository) IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&serviceNetworkModel{}).
		Where("network_id = ?", id.String()).Count(&count).Error; err != nil {
		return false, fmt.Errorf("check network attachment: %w", err)
	}
	return count > 0, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func networkToModel(n *network.Network) *networkModel {
	return &networkModel{
		ID:         n.ID.String(),
		ClusterID:  clusterIDColumn(n.ClusterID),
		Name:       n.Name,
		Driver:     n.Driver,
		Scope:      n.Scope,
		Subnet:     n.Subnet,
		Attachable: n.Attachable,
		External:   n.External,
		SwarmID:    n.SwarmID,
		CreatedAt:  n.CreatedAt,
	}
}

func networkToDomain(m *networkModel) *network.Network {
	id, _ := uuid.Parse(m.ID)
	return &network.Network{
		ID:         id,
		ClusterID:  parseClusterID(m.ClusterID),
		Name:       m.Name,
		Driver:     m.Driver,
		Scope:      m.Scope,
		Subnet:     m.Subnet,
		Attachable: m.Attachable,
		External:   m.External,
		SwarmID:    m.SwarmID,
		CreatedAt:  m.CreatedAt,
	}
}
