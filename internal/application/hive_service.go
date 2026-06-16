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

func (s *HiveService) Create(ctx context.Context, in SaveHiveInput) (*hive.Hive, error) {
	h, err := hive.New(in.Name, in.Description, in.Color)
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

// List returns every hive together with its service count.
func (s *HiveService) List(ctx context.Context, page pagination.Page) ([]HiveSummary, int64, error) {
	hives, total, err := s.hives.List(ctx, page)
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
