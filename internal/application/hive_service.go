package application

import (
	"context"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/hive"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// HiveService manages hives (projects grouping services) and the assignment of
// services to a hive. A service belongs to at most one hive.
type HiveService struct {
	hives    ports.HiveRepository
	services ports.ServiceRepository
}

func NewHiveService(hives ports.HiveRepository, services ports.ServiceRepository) *HiveService {
	return &HiveService{hives: hives, services: services}
}

type SaveHiveInput struct {
	Name        string
	Description string
	Color       string
}

// HiveSummary pairs a hive with the number of services it contains.
type HiveSummary struct {
	Hive         *hive.Hive
	ServiceCount int64
}

func (s *HiveService) Create(ctx context.Context, clusterID uuid.UUID, in SaveHiveInput) (*hive.Hive, error) {
	h, err := hive.New(clusterID, in.Name, in.Description, in.Color)
	if err != nil {
		return nil, err
	}
	if err := s.hives.Save(ctx, h); err != nil {
		return nil, err
	}
	return h, nil
}

func (s *HiveService) Get(ctx context.Context, id uuid.UUID) (*hive.Hive, error) {
	return s.hives.FindByID(ctx, id)
}

// List returns every hive in the cluster together with its service count.
func (s *HiveService) List(ctx context.Context, clusterID uuid.UUID, page pagination.Page) ([]HiveSummary, int64, error) {
	hives, total, err := s.hives.List(ctx, clusterID, page)
	if err != nil {
		return nil, 0, err
	}
	out := make([]HiveSummary, 0, len(hives))
	for _, h := range hives {
		count, err := s.services.CountServicesByHive(ctx, h.ID)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, HiveSummary{Hive: h, ServiceCount: count})
	}
	return out, total, nil
}

func (s *HiveService) Update(ctx context.Context, id uuid.UUID, in SaveHiveInput) (*hive.Hive, error) {
	h, err := s.hives.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := h.Update(in.Name, in.Description, in.Color); err != nil {
		return nil, err
	}
	if err := s.hives.Update(ctx, h); err != nil {
		return nil, err
	}
	return h, nil
}

// Delete removes a hive, refusing if it still contains services.
func (s *HiveService) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.hives.FindByID(ctx, id); err != nil {
		return err
	}
	count, err := s.services.CountServicesByHive(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return hive.ErrHiveNotEmpty
	}
	return s.hives.Delete(ctx, id)
}

// ListServices returns the services assigned to a hive.
func (s *HiveService) ListServices(ctx context.Context, hiveID uuid.UUID) ([]*service.Service, error) {
	if _, err := s.hives.FindByID(ctx, hiveID); err != nil {
		return nil, err
	}
	id := hiveID
	items, _, err := s.services.List(ctx, ports.ServiceFilter{HiveID: &id}, pagination.Page{Number: 1, Size: 1000})
	return items, err
}

// SetEnvVars replaces the hive's global environment variables, applied to every
// service in the hive at deploy time (a service-level var with the same key
// overrides it). Keys are validated and unique; secret values are encrypted at
// rest. Like service env vars, a secret submitted blank keeps its stored value
// (secrets are masked on read, so blank means "unchanged", not "clear").
// Changes take effect on the next deploy of each affected service.
func (s *HiveService) SetEnvVars(ctx context.Context, hiveID uuid.UUID, inputs []EnvVarInput) ([]hive.EnvVar, error) {
	if _, err := s.hives.FindByID(ctx, hiveID); err != nil {
		return nil, err
	}

	current, err := s.hives.GetEnvVars(ctx, hiveID)
	if err != nil {
		return nil, err
	}
	existingSecret := make(map[string]string, len(current))
	for _, ev := range current {
		if ev.IsSecret {
			existingSecret[ev.Key] = ev.Value
		}
	}

	vars := make([]hive.EnvVar, 0, len(inputs))
	for _, in := range inputs {
		if in.IsSecret && in.Value == "" {
			if prev, ok := existingSecret[in.Key]; ok {
				in.Value = prev
			}
		}
		ev, err := hive.NewEnvVar(hiveID, in.Key, in.Value, in.IsSecret)
		if err != nil {
			return nil, err
		}
		vars = append(vars, *ev)
	}
	if err := hive.ValidateEnvVars(vars); err != nil {
		return nil, err
	}

	if err := s.hives.SetEnvVars(ctx, hiveID, vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// GetEnvVars returns the hive's global env vars (secret values decrypted;
// masking for API responses is the handler's responsibility).
func (s *HiveService) GetEnvVars(ctx context.Context, hiveID uuid.UUID) ([]hive.EnvVar, error) {
	if _, err := s.hives.FindByID(ctx, hiveID); err != nil {
		return nil, err
	}
	return s.hives.GetEnvVars(ctx, hiveID)
}

// MoveService assigns a service to a hive, or clears its hive when hiveID is nil
// (unassign). The target hive, when provided, must exist.
func (s *HiveService) MoveService(ctx context.Context, serviceID uuid.UUID, hiveID *uuid.UUID) (*service.Service, error) {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if hiveID != nil {
		if _, err := s.hives.FindByID(ctx, *hiveID); err != nil {
			return nil, err
		}
	}
	svc.HiveID = hiveID
	if err := s.services.Update(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}
