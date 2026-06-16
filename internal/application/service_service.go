package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

var ErrServiceDeployed = errors.New("service is currently deployed — undeploy it before deleting")

// ErrResourceExceedsCluster is returned when a CPU/memory reservation or limit
// is larger than the biggest single node in the cluster. A task that no node
// can satisfy would stay pending forever, so we reject it up front rather than
// letting it silently never schedule.
var ErrResourceExceedsCluster = errors.New("resource request exceeds the largest node's capacity")

type ServiceService struct {
	services     ports.ServiceRepository
	orchestrator ports.Orchestrator // nil until F-MVP-08 is wired
}

func NewServiceService(services ports.ServiceRepository, orchestrator ports.Orchestrator) *ServiceService {
	return &ServiceService{services: services, orchestrator: orchestrator}
}

// ─── Input types ─────────────────────────────────────────────────────────────

type CreateServiceInput struct {
	Name         string
	Description  string
	Image        string
	Tag          string
	Replicas     uint64
	Command      []string
	Entrypoint   []string
	Resources    service.Resources
	Placement    service.Placement
	UpdateConfig *service.UpdateConfig
}

// UpdateServiceInput uses pointer fields so callers can omit unchanged fields.
// A nil field means "leave as-is"; a non-nil field (even zero-value) replaces the current value.
type UpdateServiceInput struct {
	Description  *string
	Image        *string
	Tag          *string
	Replicas     *uint64
	Command      *[]string
	Entrypoint   *[]string
	Resources    *service.Resources
	Placement    *service.Placement
	UpdateConfig *service.UpdateConfig
}

// EnvVarInput is a single environment variable submitted by the API (F-MVP-04).
type EnvVarInput struct {
	Key      string
	Value    string
	IsSecret bool
}

// ─── Use cases ────────────────────────────────────────────────────────────────

// Create saves a new service in draft status. It does NOT deploy the service.
func (s *ServiceService) Create(ctx context.Context, in CreateServiceInput) (*service.Service, error) {
	_, err := s.services.FindByName(ctx, in.Name)
	if err == nil {
		return nil, fmt.Errorf("%w: a service named %q already exists", domainerrors.ErrConflict, in.Name)
	}
	if !errors.Is(err, domainerrors.ErrNotFound) {
		return nil, err
	}

	svc, err := service.New(in.Name, in.Image, in.Tag, in.Replicas)
	if err != nil {
		return nil, err
	}

	svc.Description = in.Description
	svc.Command = in.Command
	svc.Entrypoint = in.Entrypoint

	if err := svc.SetResources(in.Resources); err != nil {
		return nil, err
	}
	if err := s.validateResourceCapacity(ctx, in.Resources); err != nil {
		return nil, err
	}

	if err := svc.SetPlacement(in.Placement); err != nil {
		return nil, err
	}

	if in.UpdateConfig != nil {
		// Overlay onto the defaults so a partial payload keeps sensible values.
		svc.UpdateConfig = svc.UpdateConfig.Overlay(*in.UpdateConfig)
	}
	if err := svc.UpdateConfig.Validate(); err != nil {
		return nil, err
	}

	if err := s.services.Save(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// validateResourceCapacity rejects reservations/limits that exceed the largest
// single node in the cluster — such a task could never be scheduled. It is
// best-effort: when the orchestrator is absent, unreachable, or reports no
// capacity, the check is skipped (we cannot enforce a bound we cannot measure;
// the domain still guarantees non-negative values and limit ≥ reservation).
func (s *ServiceService) validateResourceCapacity(ctx context.Context, r service.Resources) error {
	if s.orchestrator == nil {
		return nil
	}
	info, err := s.orchestrator.ClusterInfo(ctx)
	if err != nil || info == nil || len(info.Nodes) == 0 {
		return nil
	}

	var maxCPU float64
	var maxMem int64
	for _, n := range info.Nodes {
		if n.CPUs > maxCPU {
			maxCPU = n.CPUs
		}
		if n.MemoryBytes > maxMem {
			maxMem = n.MemoryBytes
		}
	}

	if maxCPU > 0 && (r.CPUReservation > maxCPU || r.CPULimit > maxCPU) {
		return fmt.Errorf("%w: CPU must be ≤ %.2f cores (largest node)", ErrResourceExceedsCluster, maxCPU)
	}
	if maxMem > 0 && (r.MemReservation > maxMem || r.MemLimit > maxMem) {
		return fmt.Errorf("%w: memory must be ≤ %d bytes (largest node)", ErrResourceExceedsCluster, maxMem)
	}
	return nil
}

// Get returns a service by its ID.
func (s *ServiceService) Get(ctx context.Context, id uuid.UUID) (*service.Service, error) {
	return s.services.FindByID(ctx, id)
}

// List returns a paginated list of services, optionally filtered by name/status.
func (s *ServiceService) List(
	ctx context.Context,
	filter ports.ServiceFilter,
	page pagination.Page,
) ([]*service.Service, int64, error) {
	return s.services.List(ctx, filter, page)
}

// Update applies the non-nil fields from UpdateServiceInput to the service.
// The service name is immutable.
func (s *ServiceService) Update(ctx context.Context, id uuid.UUID, in UpdateServiceInput) (*service.Service, error) {
	svc, err := s.services.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	touched := false

	if in.Description != nil {
		svc.Description = *in.Description
		touched = true
	}
	if in.Image != nil {
		if strings.TrimSpace(*in.Image) == "" {
			return nil, service.ErrInvalidImage
		}
		svc.Image = *in.Image
		touched = true
	}
	if in.Tag != nil {
		svc.UpdateTag(*in.Tag)
		touched = true
	}
	if in.Replicas != nil {
		svc.Replicas = *in.Replicas
		touched = true
	}
	if in.Command != nil {
		svc.Command = *in.Command
		touched = true
	}
	if in.Entrypoint != nil {
		svc.Entrypoint = *in.Entrypoint
		touched = true
	}
	if in.Resources != nil {
		if err := svc.SetResources(*in.Resources); err != nil {
			return nil, err
		}
		if err := s.validateResourceCapacity(ctx, *in.Resources); err != nil {
			return nil, err
		}
		touched = true
	}
	if in.Placement != nil {
		if err := svc.SetPlacement(*in.Placement); err != nil {
			return nil, err
		}
		touched = true
	}
	if in.UpdateConfig != nil {
		// Overlay onto the current config so omitted fields are preserved.
		svc.UpdateConfig = svc.UpdateConfig.Overlay(*in.UpdateConfig)
		if err := svc.UpdateConfig.Validate(); err != nil {
			return nil, err
		}
		touched = true
	}

	if !touched {
		return svc, nil
	}

	svc.UpdatedAt = now

	if err := s.services.Update(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// SetResources updates only the CPU/memory constraints of a service (F-MVP-03),
// leaving every other field untouched. Validation (limit >= reservation, no
// negatives) lives in the domain via Service.SetResources.
func (s *ServiceService) SetResources(ctx context.Context, id uuid.UUID, r service.Resources) (*service.Service, error) {
	svc, err := s.services.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := svc.SetResources(r); err != nil {
		return nil, err
	}
	if err := s.validateResourceCapacity(ctx, r); err != nil {
		return nil, err
	}
	if err := s.services.Update(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// SetPlacement updates only the scheduling placement of a service (constraints,
// spread preferences and max replicas per node), leaving every other field
// untouched. Validation (well-formed constraints, non-empty preferences) lives
// in the domain via Service.SetPlacement.
func (s *ServiceService) SetPlacement(ctx context.Context, id uuid.UUID, p service.Placement) (*service.Service, error) {
	svc, err := s.services.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := svc.SetPlacement(p); err != nil {
		return nil, err
	}
	if err := s.services.Update(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// SetEnvVars atomically replaces the full set of environment variables for a
// service (F-MVP-04). Keys are validated and must be unique; secret values are
// encrypted at rest by the repository. Returns the stored variables.
func (s *ServiceService) SetEnvVars(ctx context.Context, serviceID uuid.UUID, inputs []EnvVarInput) ([]service.EnvVar, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, err
	}

	vars := make([]service.EnvVar, 0, len(inputs))
	for _, in := range inputs {
		ev, err := service.NewEnvVar(serviceID, in.Key, in.Value, in.IsSecret)
		if err != nil {
			return nil, err
		}
		vars = append(vars, *ev)
	}
	if err := service.ValidateEnvVars(vars); err != nil {
		return nil, err
	}

	if err := s.services.SetEnvVars(ctx, serviceID, vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// GetEnvVars returns the environment variables of a service. Secret values are
// returned decrypted by the repository; masking for API responses is the
// handler's responsibility.
func (s *ServiceService) GetEnvVars(ctx context.Context, serviceID uuid.UUID) ([]service.EnvVar, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, err
	}
	return s.services.GetEnvVars(ctx, serviceID)
}

// Delete removes a service. If it is currently deployed and an Orchestrator
// is wired, the Swarm service is removed first. Without an Orchestrator,
// deleting a deployed service returns ErrServiceDeployed.
func (s *ServiceService) Delete(ctx context.Context, id uuid.UUID) error {
	svc, err := s.services.FindByID(ctx, id)
	if err != nil {
		return err
	}

	if svc.Status == service.StatusDeployed {
		if s.orchestrator == nil {
			return ErrServiceDeployed
		}
		if err := s.orchestrator.RemoveService(ctx, svc.SwarmServiceID); err != nil {
			return fmt.Errorf("remove swarm service: %w", err)
		}
	}

	return s.services.Delete(ctx, id)
}
