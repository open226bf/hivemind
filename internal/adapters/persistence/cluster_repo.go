package persistence

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/cluster"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ClusterRepository persists orchestration targets. The TLS material is
// encrypted at rest with the injected Cipher.
type ClusterRepository struct {
	db     *gorm.DB
	cipher Cipher
}

func NewClusterRepository(db *gorm.DB, cipher Cipher) *ClusterRepository {
	return &ClusterRepository{db: db, cipher: cipher}
}

func (r *ClusterRepository) Save(ctx context.Context, c *cluster.Cluster) error {
	m, err := r.toModel(c)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: cluster name %q already exists", domainerrors.ErrConflict, c.Name)
		}
		return fmt.Errorf("save cluster: %w", err)
	}
	return nil
}

func (r *ClusterRepository) FindByID(ctx context.Context, id uuid.UUID) (*cluster.Cluster, error) {
	var m clusterModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find cluster: %w", err)
	}
	return r.toDomain(&m)
}

func (r *ClusterRepository) FindByName(ctx context.Context, name string) (*cluster.Cluster, error) {
	var m clusterModel
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find cluster by name: %w", err)
	}
	return r.toDomain(&m)
}

func (r *ClusterRepository) FindDefault(ctx context.Context) (*cluster.Cluster, error) {
	var m clusterModel
	err := r.db.WithContext(ctx).Where("is_default = ?", true).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find default cluster: %w", err)
	}
	return r.toDomain(&m)
}

func (r *ClusterRepository) List(ctx context.Context, p pagination.Page) ([]*cluster.Cluster, int64, error) {
	var models []clusterModel
	var count int64

	q := r.db.WithContext(ctx).Model(&clusterModel{})
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count clusters: %w", err)
	}
	// Default first, then alphabetical — the dashboard lists the primary target on top.
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("is_default DESC, name ASC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list clusters: %w", err)
	}

	out := make([]*cluster.Cluster, 0, len(models))
	for i := range models {
		c, err := r.toDomain(&models[i])
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, count, nil
}

func (r *ClusterRepository) Update(ctx context.Context, c *cluster.Cluster) error {
	m, err := r.toModel(c)
	if err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Save(m).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return fmt.Errorf("%w: cluster name %q already exists", domainerrors.ErrConflict, c.Name)
		}
		return fmt.Errorf("update cluster: %w", err)
	}
	return nil
}

func (r *ClusterRepository) Delete(ctx context.Context, id uuid.UUID) error {
	res := r.db.WithContext(ctx).Where("id = ?", id.String()).Delete(&clusterModel{})
	if res.Error != nil {
		return fmt.Errorf("delete cluster: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *ClusterRepository) ClearDefault(ctx context.Context) error {
	if err := r.db.WithContext(ctx).Model(&clusterModel{}).
		Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
		return fmt.Errorf("clear default cluster: %w", err)
	}
	return nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func (r *ClusterRepository) toModel(c *cluster.Cluster) (*clusterModel, error) {
	ca, err := r.cipher.Encrypt(c.TLS.CACert)
	if err != nil {
		return nil, fmt.Errorf("encrypt ca cert: %w", err)
	}
	crt, err := r.cipher.Encrypt(c.TLS.ClientCert)
	if err != nil {
		return nil, fmt.Errorf("encrypt client cert: %w", err)
	}
	key, err := r.cipher.Encrypt(c.TLS.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt client key: %w", err)
	}
	return &clusterModel{
		ID:                  c.ID.String(),
		Name:                c.Name,
		Type:                string(c.Type),
		Endpoint:            c.Endpoint,
		IsDefault:           c.IsDefault,
		Status:              string(c.Status),
		Labels:              labelsToSlice(c.Labels),
		EncryptedCACert:     ca,
		EncryptedClientCert: crt,
		EncryptedClientKey:  key,
		CreatedAt:           c.CreatedAt,
		UpdatedAt:           c.UpdatedAt,
	}, nil
}

func (r *ClusterRepository) toDomain(m *clusterModel) (*cluster.Cluster, error) {
	id, _ := uuid.Parse(m.ID)
	ca, err := r.cipher.Decrypt(m.EncryptedCACert)
	if err != nil {
		return nil, fmt.Errorf("decrypt ca cert: %w", err)
	}
	crt, err := r.cipher.Decrypt(m.EncryptedClientCert)
	if err != nil {
		return nil, fmt.Errorf("decrypt client cert: %w", err)
	}
	key, err := r.cipher.Decrypt(m.EncryptedClientKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt client key: %w", err)
	}
	return &cluster.Cluster{
		ID:        id,
		Name:      m.Name,
		Type:      cluster.Type(m.Type),
		Endpoint:  m.Endpoint,
		IsDefault: m.IsDefault,
		Status:    cluster.Status(m.Status),
		Labels:    labelsToMap(m.Labels),
		TLS:       cluster.TLS{CACert: ca, ClientCert: crt, ClientKey: key},
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

func labelsToSlice(labels map[string]string) stringSlice {
	if len(labels) == 0 {
		return stringSlice{}
	}
	out := make(stringSlice, 0, len(labels))
	for k, v := range labels {
		out = append(out, k+"="+v)
	}
	sort.Strings(out) // stable ordering for deterministic persistence
	return out
}

func labelsToMap(entries stringSlice) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		k, v, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		out[k] = v
	}
	return out
}
