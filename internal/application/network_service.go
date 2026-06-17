package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/network"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// NetworkService manages overlay networks and their attachment to services
// (F-MVP-05). Attachment lives on the service side of the relationship, so it
// coordinates both the network and service repositories.
type NetworkService struct {
	networks ports.NetworkRepository
	services ports.ServiceRepository
}

func NewNetworkService(networks ports.NetworkRepository, services ports.ServiceRepository) *NetworkService {
	return &NetworkService{networks: networks, services: services}
}

type CreateNetworkInput struct {
	Name       string
	Subnet     string
	Attachable bool
	External   bool
	// Cluster is the target cluster id. Empty selects the default cluster.
	Cluster uuid.UUID
}

// Create registers a new overlay network definition. It is not created on Swarm
// until a service using it is deployed (F-MVP-08).
func (s *NetworkService) Create(ctx context.Context, in CreateNetworkInput) (*network.Network, error) {
	n, err := network.New(in.Name)
	if err != nil {
		return nil, err
	}
	n.ClusterID = in.Cluster
	n.Subnet = in.Subnet
	n.Attachable = in.Attachable
	n.External = in.External

	if err := s.networks.Save(ctx, n); err != nil {
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

// Delete removes a network, refusing if it is still attached to any service.
func (s *NetworkService) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.networks.FindByID(ctx, id); err != nil {
		return err
	}
	attached, err := s.networks.IsAttachedToService(ctx, id)
	if err != nil {
		return err
	}
	if attached {
		return network.ErrNetworkInUse
	}
	return s.networks.Delete(ctx, id)
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
