package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// NetworkService manages overlay networks and their attachment to services
// (F-MVP-05). Attachment lives on the service side of the relationship, so it
// coordinates both the network and service repositories.
type NetworkService struct {
	networks      ports.NetworkRepository
	services      ports.ServiceRepository
	orchestrators ports.OrchestratorRegistry
	tx            ports.TxManager
}

// NewNetworkService wires the network use case. orchestrators and tx may be nil
// (tests, or a deployment with no orchestrator): creation then only writes to
// the database, as it did before overlays were created up front.
func NewNetworkService(
	networks ports.NetworkRepository,
	services ports.ServiceRepository,
	orchestrators ports.OrchestratorRegistry,
	tx ports.TxManager,
) *NetworkService {
	return &NetworkService{networks: networks, services: services, orchestrators: orchestrators, tx: tx}
}

type CreateNetworkInput struct {
	Name       string
	Subnet     string
	Attachable bool
	External   bool
	// Cluster is the target cluster id. Empty selects the default cluster.
	Cluster uuid.UUID
}

// Create registers a new overlay network and materialises it on the target
// cluster in the same transaction: either the row and the overlay both exist,
// or neither does. An external network is only referenced, never created, so it
// is recorded without touching the cluster.
func (s *NetworkService) Create(ctx context.Context, in CreateNetworkInput) (*network.Network, error) {
	n, err := network.New(in.Name)
	if err != nil {
		return nil, err
	}
	n.ClusterID = in.Cluster
	n.Subnet = in.Subnet
	n.Attachable = in.Attachable
	n.External = in.External

	if n.External {
		if err := s.networks.Save(ctx, n); err != nil {
			return nil, err
		}
		return n, nil
	}

	res := clusterResource{
		Persist: func(ctx context.Context) error { return s.networks.Save(ctx, n) },
		Create: func(ctx context.Context, orch ports.Orchestrator) (string, error) {
			return orch.CreateNetwork(ctx, n.Name, ports.CreateNetworkOptions{
				Attachable: n.Attachable,
				Subnet:     n.Subnet,
			})
		},
		Record: func(ctx context.Context, swarmID string) error {
			n.SwarmID = swarmID
			return s.networks.SetSwarmID(ctx, n.ID, swarmID)
		},
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveNetwork(ctx, swarmID)
		},
	}
	if err := res.create(ctx, s.tx, s.orchestrators, n.ClusterID, "network "+n.Name); err != nil {
		return nil, err
	}
	return n, nil
}

func (s *NetworkService) Get(ctx context.Context, id uuid.UUID) (*network.Network, error) {
	return s.networks.FindByID(ctx, id)
}

func (s *NetworkService) List(ctx context.Context, clusterID uuid.UUID, page pagination.Page) ([]*network.Network, int64, error) {
	return s.networks.List(ctx, clusterID, page)
}

// Delete removes a network from the database and the overlay from the cluster in
// the same transaction, refusing if it is still attached to any service. An
// external network is only dropped from the catalog — Hivemind never created it.
func (s *NetworkService) Delete(ctx context.Context, id uuid.UUID) error {
	n, err := s.networks.FindByID(ctx, id)
	if err != nil {
		return err
	}
	attached, err := s.networks.IsAttachedToService(ctx, id)
	if err != nil {
		return err
	}
	if attached {
		return network.ErrNetworkInUse
	}
	if n.External || n.SwarmID == "" {
		return s.networks.Delete(ctx, id)
	}

	del := clusterDeletion{
		SwarmIDs: func(context.Context) ([]string, error) { return []string{n.SwarmID}, nil },
		Delete:   func(ctx context.Context) error { return s.networks.Delete(ctx, id) },
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveNetwork(ctx, swarmID)
		},
	}
	return del.run(ctx, s.tx, s.orchestrators, n.ClusterID, "network "+n.Name)
}

// AttachToService links a network to a service. Both must exist; attaching an
// already-attached network surfaces as a conflict.
func (s *NetworkService) AttachToService(ctx context.Context, serviceID, networkID uuid.UUID) error {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return err
	}
	n, err := s.networks.FindByID(ctx, networkID)
	if err != nil {
		return err
	}
	if n.ClusterID != svc.ClusterID {
		return ErrClusterMismatch
	}
	return s.services.AttachNetwork(ctx, serviceID, networkID)
}

// DetachFromService unlinks a network from a service. Returns ErrNotFound if the
// attachment does not exist.
func (s *NetworkService) DetachFromService(ctx context.Context, serviceID, networkID uuid.UUID) error {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return err
	}
	return s.services.DetachNetwork(ctx, serviceID, networkID)
}

// ListServiceNetworks returns the networks currently attached to a service.
func (s *NetworkService) ListServiceNetworks(ctx context.Context, serviceID uuid.UUID) ([]*network.Network, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, err
	}
	ids, err := s.services.GetNetworkIDs(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	out := make([]*network.Network, 0, len(ids))
	for _, id := range ids {
		n, err := s.networks.FindByID(ctx, id)
		if errors.Is(err, domainerrors.ErrNotFound) {
			continue // tolerate a dangling attachment row
		}
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}
