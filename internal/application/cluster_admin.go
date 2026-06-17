package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/cluster"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// ClusterTLSInput carries optional mutual-TLS material (PEM) for reaching a
// remote Docker daemon. Empty fields leave the cluster's existing values
// untouched on update.
type ClusterTLSInput struct {
	CACert     string
	ClientCert string
	ClientKey  string
}

// CreateClusterInput registers a new orchestration target.
type CreateClusterInput struct {
	Name     string
	Type     string
	Endpoint string
	Labels   map[string]string
	TLS      ClusterTLSInput
}

// UpdateClusterInput patches an existing cluster. Nil pointers are left as-is.
type UpdateClusterInput struct {
	Name     *string
	Endpoint *string
	Labels   map[string]string
	TLS      *ClusterTLSInput
}

// CreateCluster registers a new cluster definition. The first cluster created
// when none is default is promoted automatically so the platform always has a
// resolvable default.
func (s *ClusterService) CreateCluster(ctx context.Context, in CreateClusterInput) (*cluster.Cluster, error) {
	typ := cluster.Type(in.Type)
	if in.Type == "" {
		typ = cluster.TypeSwarm
	}
	c, err := cluster.New(in.Name, typ, in.Endpoint)
	if err != nil {
		return nil, err
	}
	c.Labels = in.Labels
	c.TLS = cluster.TLS{CACert: in.TLS.CACert, ClientCert: in.TLS.ClientCert, ClientKey: in.TLS.ClientKey}

	if _, err := s.clusters.FindDefault(ctx); errors.Is(err, domainerrors.ErrNotFound) {
		c.IsDefault = true
	} else if err != nil {
		return nil, err
	}

	if err := s.clusters.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *ClusterService) ListClusters(ctx context.Context, page pagination.Page) ([]*cluster.Cluster, int64, error) {
	return s.clusters.List(ctx, page)
}

func (s *ClusterService) GetCluster(ctx context.Context, id uuid.UUID) (*cluster.Cluster, error) {
	return s.clusters.FindByID(ctx, id)
}

// UpdateCluster patches a cluster's editable fields. When the endpoint or TLS
// material changes, the cached connection is invalidated so the next lookup
// rebuilds it.
func (s *ClusterService) UpdateCluster(ctx context.Context, id uuid.UUID, in UpdateClusterInput) (*cluster.Cluster, error) {
	c, err := s.clusters.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	connChanged := false
	if in.Name != nil {
		if err := c.Rename(*in.Name); err != nil {
			return nil, err
		}
	}
	if in.Labels != nil {
		c.Labels = in.Labels
	}
	if in.Endpoint != nil || in.TLS != nil {
		endpoint := c.Endpoint
		if in.Endpoint != nil {
			endpoint = *in.Endpoint
		}
		tls := c.TLS
		if in.TLS != nil {
			tls = cluster.TLS{CACert: in.TLS.CACert, ClientCert: in.TLS.ClientCert, ClientKey: in.TLS.ClientKey}
		}
		c.SetEndpoint(endpoint, tls)
		connChanged = true
	}

	if err := s.clusters.Update(ctx, c); err != nil {
		return nil, err
	}
	if connChanged && s.registry != nil {
		s.registry.Invalidate(c.ID)
	}
	return c, nil
}

// SetDefaultCluster promotes a cluster to be the default, demoting the previous
// one. Resolving the zero ClusterID (and thus all legacy resources) will then
// target this cluster.
func (s *ClusterService) SetDefaultCluster(ctx context.Context, id uuid.UUID) (*cluster.Cluster, error) {
	c, err := s.clusters.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.IsDefault {
		return c, nil
	}
	if err := s.clusters.ClearDefault(ctx); err != nil {
		return nil, err
	}
	c.IsDefault = true
	if err := s.clusters.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// DeleteCluster removes a cluster. The default cluster cannot be removed, and a
// cluster that still has services targeting it is refused (its resources would
// be orphaned). The cached connection is invalidated on success.
func (s *ClusterService) DeleteCluster(ctx context.Context, id uuid.UUID) error {
	c, err := s.clusters.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if c.IsDefault {
		return cluster.ErrDefaultCluster
	}
	count, err := s.services.CountServicesByCluster(ctx, id)
	if err != nil {
		return err
	}
	if count > 0 {
		return cluster.ErrClusterNotEmpty
	}
	if err := s.clusters.Delete(ctx, id); err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Invalidate(id)
	}
	return nil
}

// TestCluster probes connectivity to a cluster and records the outcome. It
// invalidates any cached connection first so a fixed endpoint is re-dialled.
func (s *ClusterService) TestCluster(ctx context.Context, id uuid.UUID) (*cluster.Cluster, error) {
	c, err := s.clusters.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, ErrOrchestratorUnavailable
	}

	s.registry.Invalidate(id)
	status := cluster.StatusReachable
	if orch, err := s.registry.For(ctx, id); err != nil || orch == nil {
		status = cluster.StatusUnreachable
	} else if _, err := orch.ClusterInfo(ctx); err != nil {
		status = cluster.StatusUnreachable
	}

	c.MarkStatus(status)
	if err := s.clusters.Update(ctx, c); err != nil {
		return nil, err
	}
	if status == cluster.StatusUnreachable {
		return c, fmt.Errorf("%w: cluster %q is unreachable", ErrOrchestratorUnavailable, c.Name)
	}
	return c, nil
}
