package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

type ServiceRepository struct {
	db     *gorm.DB
	cipher Cipher
}

func NewServiceRepository(db *gorm.DB, cipher Cipher) *ServiceRepository {
	return &ServiceRepository{db: db, cipher: cipher}
}

func (r *ServiceRepository) Save(ctx context.Context, s *service.Service) error {
	if err := r.db.WithContext(ctx).Create(serviceToModel(s)).Error; err != nil {
		return fmt.Errorf("save service: %w", err)
	}
	return nil
}

func (r *ServiceRepository) FindByID(ctx context.Context, id uuid.UUID) (*service.Service, error) {
	var m serviceModel
	err := r.db.WithContext(ctx).Where("id = ?", id.String()).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find service by id: %w", err)
	}
	return serviceToDomain(&m), nil
}

func (r *ServiceRepository) FindByName(ctx context.Context, name string) (*service.Service, error) {
	var m serviceModel
	err := r.db.WithContext(ctx).Where("name = ?", name).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domainerrors.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find service by name: %w", err)
	}
	return serviceToDomain(&m), nil
}

func (r *ServiceRepository) List(ctx context.Context, filter ports.ServiceFilter, p pagination.Page) ([]*service.Service, int64, error) {
	var models []serviceModel
	var count int64

	q := r.db.WithContext(ctx).Model(&serviceModel{})
	if filter.Name != "" {
		q = q.Where("name ILIKE ?", "%"+filter.Name+"%")
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if err := q.Count(&count).Error; err != nil {
		return nil, 0, fmt.Errorf("count services: %w", err)
	}
	if err := q.Offset(p.Offset()).Limit(p.Size).Order("created_at DESC").Find(&models).Error; err != nil {
		return nil, 0, fmt.Errorf("list services: %w", err)
	}

	services := make([]*service.Service, 0, len(models))
	for i := range models {
		services = append(services, serviceToDomain(&models[i]))
	}
	return services, count, nil
}

func (r *ServiceRepository) Update(ctx context.Context, s *service.Service) error {
	if err := r.db.WithContext(ctx).Save(serviceToModel(s)).Error; err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	return nil
}

func (r *ServiceRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sid := id.String()
		for _, table := range []string{"env_vars", "service_networks", "service_secrets", "service_configs"} {
			if err := tx.Exec("DELETE FROM "+table+" WHERE service_id = ?", sid).Error; err != nil {
				return fmt.Errorf("delete %s: %w", table, err)
			}
		}
		res := tx.Where("id = ?", sid).Delete(&serviceModel{})
		if res.Error != nil {
			return fmt.Errorf("delete service: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return domainerrors.ErrNotFound
		}
		return nil
	})
}

// ─── Env vars ─────────────────────────────────────────────────────────────────

func (r *ServiceRepository) SetEnvVars(ctx context.Context, serviceID uuid.UUID, vars []service.EnvVar) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("service_id = ?", serviceID.String()).Delete(&envVarModel{}).Error; err != nil {
			return fmt.Errorf("clear env vars: %w", err)
		}
		if len(vars) == 0 {
			return nil
		}
		models := make([]envVarModel, 0, len(vars))
		for _, v := range vars {
			val := v.Value
			if v.IsSecret {
				enc, err := r.cipher.Encrypt(v.Value)
				if err != nil {
					return fmt.Errorf("encrypt env var %s: %w", v.Key, err)
				}
				val = enc
			}
			models = append(models, envVarModel{
				ID:        v.ID.String(),
				ServiceID: serviceID.String(),
				Key:       v.Key,
				Value:     val,
				IsSecret:  v.IsSecret,
			})
		}
		return tx.Create(&models).Error
	})
}

func (r *ServiceRepository) GetEnvVars(ctx context.Context, serviceID uuid.UUID) ([]service.EnvVar, error) {
	var models []envVarModel
	if err := r.db.WithContext(ctx).
		Where("service_id = ?", serviceID.String()).
		Order("key ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("get env vars: %w", err)
	}

	vars := make([]service.EnvVar, 0, len(models))
	for _, m := range models {
		id, _ := uuid.Parse(m.ID)
		svcID, _ := uuid.Parse(m.ServiceID)
		val := m.Value
		if m.IsSecret {
			dec, err := r.cipher.Decrypt(m.Value)
			if err != nil {
				return nil, fmt.Errorf("decrypt env var %s: %w", m.Key, err)
			}
			val = dec
		}
		vars = append(vars, service.EnvVar{
			ID:        id,
			ServiceID: svcID,
			Key:       m.Key,
			Value:     val,
			IsSecret:  m.IsSecret,
		})
	}
	return vars, nil
}

// ─── Network attachments ──────────────────────────────────────────────────────

func (r *ServiceRepository) AttachNetwork(ctx context.Context, serviceID, networkID uuid.UUID) error {
	m := serviceNetworkModel{ServiceID: serviceID.String(), NetworkID: networkID.String()}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("attach network: %w", err)
	}
	return nil
}

func (r *ServiceRepository) DetachNetwork(ctx context.Context, serviceID, networkID uuid.UUID) error {
	res := r.db.WithContext(ctx).
		Where("service_id = ? AND network_id = ?", serviceID.String(), networkID.String()).
		Delete(&serviceNetworkModel{})
	if res.Error != nil {
		return fmt.Errorf("detach network: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *ServiceRepository) GetNetworkIDs(ctx context.Context, serviceID uuid.UUID) ([]uuid.UUID, error) {
	var rows []serviceNetworkModel
	if err := r.db.WithContext(ctx).
		Where("service_id = ?", serviceID.String()).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get networks: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		id, _ := uuid.Parse(row.NetworkID)
		ids = append(ids, id)
	}
	return ids, nil
}

// ─── Secret attachments ───────────────────────────────────────────────────────

func (r *ServiceRepository) AttachSecret(ctx context.Context, serviceID, secretID uuid.UUID, targetPath string) error {
	m := serviceSecretModel{
		ServiceID:  serviceID.String(),
		SecretID:   secretID.String(),
		TargetPath: targetPath,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("attach secret: %w", err)
	}
	return nil
}

func (r *ServiceRepository) DetachSecret(ctx context.Context, serviceID, secretID uuid.UUID) error {
	res := r.db.WithContext(ctx).
		Where("service_id = ? AND secret_id = ?", serviceID.String(), secretID.String()).
		Delete(&serviceSecretModel{})
	if res.Error != nil {
		return fmt.Errorf("detach secret: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *ServiceRepository) GetSecretAttachments(ctx context.Context, serviceID uuid.UUID) ([]ports.ServiceSecretAttachment, error) {
	var rows []serviceSecretModel
	if err := r.db.WithContext(ctx).
		Where("service_id = ?", serviceID.String()).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get secret attachments: %w", err)
	}
	out := make([]ports.ServiceSecretAttachment, 0, len(rows))
	for _, row := range rows {
		id, _ := uuid.Parse(row.SecretID)
		out = append(out, ports.ServiceSecretAttachment{SecretID: id, TargetPath: row.TargetPath})
	}
	return out, nil
}

// ─── Config attachments ───────────────────────────────────────────────────────

func (r *ServiceRepository) AttachConfig(ctx context.Context, serviceID, configID uuid.UUID, targetPath string) error {
	m := serviceConfigModel{
		ServiceID:  serviceID.String(),
		ConfigID:   configID.String(),
		TargetPath: targetPath,
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("attach config: %w", err)
	}
	return nil
}

func (r *ServiceRepository) DetachConfig(ctx context.Context, serviceID, configID uuid.UUID) error {
	res := r.db.WithContext(ctx).
		Where("service_id = ? AND config_id = ?", serviceID.String(), configID.String()).
		Delete(&serviceConfigModel{})
	if res.Error != nil {
		return fmt.Errorf("detach config: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domainerrors.ErrNotFound
	}
	return nil
}

func (r *ServiceRepository) GetConfigAttachments(ctx context.Context, serviceID uuid.UUID) ([]ports.ServiceConfigAttachment, error) {
	var rows []serviceConfigModel
	if err := r.db.WithContext(ctx).
		Where("service_id = ?", serviceID.String()).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("get config attachments: %w", err)
	}
	out := make([]ports.ServiceConfigAttachment, 0, len(rows))
	for _, row := range rows {
		id, _ := uuid.Parse(row.ConfigID)
		out = append(out, ports.ServiceConfigAttachment{ConfigID: id, TargetPath: row.TargetPath})
	}
	return out, nil
}

// ─── Mappers ──────────────────────────────────────────────────────────────────

func serviceToModel(s *service.Service) *serviceModel {
	return &serviceModel{
		ID:          s.ID.String(),
		Name:        s.Name,
		Description: s.Description,
		Image:       s.Image,
		Tag:         s.Tag,
		Replicas:    s.Replicas,
		Command:     stringSlice(s.Command),
		Entrypoint:  stringSlice(s.Entrypoint),

		CPUReservation: s.Resources.CPUReservation,
		CPULimit:       s.Resources.CPULimit,
		MemReservation: s.Resources.MemReservation,
		MemLimit:       s.Resources.MemLimit,

		UpdateParallelism:     s.UpdateConfig.Parallelism,
		UpdateDelay:           s.UpdateConfig.Delay.Nanoseconds(),
		UpdateFailureAction:   s.UpdateConfig.FailureAction,
		UpdateMonitor:         s.UpdateConfig.Monitor.Nanoseconds(),
		UpdateMaxFailureRatio: s.UpdateConfig.MaxFailureRatio,
		UpdateOrder:           s.UpdateConfig.Order,

		Status:         string(s.Status),
		SwarmServiceID: s.SwarmServiceID,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

func serviceToDomain(m *serviceModel) *service.Service {
	id, _ := uuid.Parse(m.ID)
	return &service.Service{
		ID:          id,
		Name:        m.Name,
		Description: m.Description,
		Image:       m.Image,
		Tag:         m.Tag,
		Replicas:    m.Replicas,
		Command:     []string(m.Command),
		Entrypoint:  []string(m.Entrypoint),
		Resources: service.Resources{
			CPUReservation: m.CPUReservation,
			CPULimit:       m.CPULimit,
			MemReservation: m.MemReservation,
			MemLimit:       m.MemLimit,
		},
		UpdateConfig: service.UpdateConfig{
			Parallelism:     m.UpdateParallelism,
			Delay:           time.Duration(m.UpdateDelay),
			FailureAction:   m.UpdateFailureAction,
			Monitor:         time.Duration(m.UpdateMonitor),
			MaxFailureRatio: m.UpdateMaxFailureRatio,
			Order:           m.UpdateOrder,
		},
		Status:         service.Status(m.Status),
		SwarmServiceID: m.SwarmServiceID,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}
