package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/volume"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// VolumeService manages the named-volume catalog (F-V2-06) and the per-service
// mount declarations. Mounts live on the service side of the relationship, so
// it coordinates both the volume and service repositories.
type VolumeService struct {
	volumes  ports.VolumeRepository
	services ports.ServiceRepository
}

func NewVolumeService(volumes ports.VolumeRepository, services ports.ServiceRepository) *VolumeService {
	return &VolumeService{volumes: volumes, services: services}
}

type CreateVolumeInput struct {
	Name   string
	Driver string
	// Cluster is the target cluster id. Empty selects the default cluster.
	Cluster uuid.UUID
}

// Create registers a named volume. Like networks, it is materialised on Swarm
// only when a service mounting it is deployed.
func (s *VolumeService) Create(ctx context.Context, in CreateVolumeInput) (*volume.Volume, error) {
	v, err := volume.New(in.Name, in.Driver)
	if err != nil {
		return nil, err
	}
	v.ClusterID = in.Cluster
	if err := s.volumes.Save(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *VolumeService) Get(ctx context.Context, id uuid.UUID) (*volume.Volume, error) {
	return s.volumes.FindByID(ctx, id)
}

func (s *VolumeService) List(ctx context.Context, clusterID uuid.UUID, page pagination.Page) ([]*volume.Volume, int64, error) {
	return s.volumes.List(ctx, clusterID, page)
}

// Delete removes a named volume, refusing if any service still mounts it.
func (s *VolumeService) Delete(ctx context.Context, id uuid.UUID) error {
	v, err := s.volumes.FindByID(ctx, id)
	if err != nil {
		return err
	}
	count, err := s.services.CountMountsByVolumeName(ctx, v.Name)
	if err != nil {
		return err
	}
	if count > 0 {
		return volume.ErrVolumeInUse
	}
	return s.volumes.Delete(ctx, id)
}

// MountsResult carries a service's mounts together with non-blocking warnings
// (e.g. a local volume on a multi-replica service is not shared across nodes).
type MountsResult struct {
	Mounts   []volume.Mount
	Warnings []string
}

// SetServiceMounts validates and atomically replaces the mount set of a service.
// Volume-type mounts must reference an existing catalog volume. Enforcing the
// Admin-only rule for bind mounts is the handler's responsibility (it owns the
// caller's role); the domain only guarantees structural validity.
func (s *VolumeService) SetServiceMounts(ctx context.Context, serviceID uuid.UUID, mounts []volume.Mount) (*MountsResult, error) {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if err := volume.ValidateMounts(mounts); err != nil {
		return nil, err
	}
	for _, m := range mounts {
		if m.Type != volume.MountVolume {
			continue
		}
		if _, err := s.volumes.FindByName(ctx, m.Source); err != nil {
			if errors.Is(err, domainerrors.ErrNotFound) {
				return nil, fmt.Errorf("%w: %q", volume.ErrUnknownVolume, m.Source)
			}
			return nil, err
		}
	}
	if err := s.services.SetMounts(ctx, serviceID, mounts); err != nil {
		return nil, err
	}
	return &MountsResult{Mounts: mounts, Warnings: mountWarnings(svc, mounts)}, nil
}

// GetServiceMounts returns a service's mounts and the same warnings.
func (s *VolumeService) GetServiceMounts(ctx context.Context, serviceID uuid.UUID) (*MountsResult, error) {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	mounts, err := s.services.GetMounts(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	return &MountsResult{Mounts: mounts, Warnings: mountWarnings(svc, mounts)}, nil
}

// mountWarnings flags node-local mounts on multi-replica services: a local
// volume or bind path is not shared across nodes, so replicas would each see
// their own copy.
func mountWarnings(svc *service.Service, mounts []volume.Mount) []string {
	if svc.Replicas <= 1 {
		return nil
	}
	var out []string
	for _, m := range mounts {
		switch m.Type {
		case volume.MountVolume:
			out = append(out, fmt.Sprintf(
				"Le volume local %q n'est pas partagé entre nœuds : chacune des %d réplicas aura sa propre copie.",
				m.Source, svc.Replicas))
		case volume.MountBind:
			out = append(out, fmt.Sprintf(
				"Le bind mount %q dépend du système de fichiers de chaque nœud (non partagé entre les %d réplicas).",
				m.Source, svc.Replicas))
		}
	}
	return out
}
