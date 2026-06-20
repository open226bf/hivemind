package orchestrator

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/telemetry"
	"github.com/open226bf/hivemind/internal/domain/cluster"
	"github.com/open226bf/hivemind/internal/ports"
)

// ErrTelemetryUnsupported is returned when a cluster's orchestrator cannot
// provide telemetry — the stub backend, for example.
var ErrTelemetryUnsupported = errors.New("telemetry not supported for this cluster")

// CollectorRegistry resolves a cluster id to a ports.TelemetryCollector,
// dispatching on the cluster's connection mode:
//
//   - direct: derive a collector from the orchestrator already resolved (and
//     cached) for that cluster, reusing the live Docker connection;
//   - agent: an AgentCollector that fans metrics out per node over the agent
//     tunnels (cluster-wide), and reuses the manager tunnel for health.
//
// It keeps the orchestrator registry as the single seam for the direct path
// rather than duplicating the TLS wiring.
type CollectorRegistry struct {
	orch     ports.OrchestratorRegistry
	clusters ports.ClusterRepository
	hub      ports.AgentHub
}

// NewCollectorRegistry wraps the orchestrator registry. clusters/hub may be nil
// (e.g. stub mode), in which case every cluster falls through to the direct path.
func NewCollectorRegistry(orch ports.OrchestratorRegistry, clusters ports.ClusterRepository, hub ports.AgentHub) *CollectorRegistry {
	return &CollectorRegistry{orch: orch, clusters: clusters, hub: hub}
}

// For resolves the collector for a cluster. The zero UUID is the default cluster.
func (r *CollectorRegistry) For(ctx context.Context, clusterID uuid.UUID) (ports.TelemetryCollector, error) {
	if c, err := r.resolve(ctx, clusterID); err == nil && c.ConnectionMode == cluster.ModeAgent && r.hub != nil {
		return telemetry.NewAgentCollector(r.hub, c.AgentID, c.ID), nil
	}
	o, err := r.orch.For(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	return collectorFrom(o, clusterID)
}

// Default resolves the collector for the default cluster.
func (r *CollectorRegistry) Default(ctx context.Context) (ports.TelemetryCollector, error) {
	return r.For(ctx, uuid.Nil)
}

// resolve looks up the cluster definition (the zero UUID = the default cluster).
func (r *CollectorRegistry) resolve(ctx context.Context, clusterID uuid.UUID) (*cluster.Cluster, error) {
	if r.clusters == nil {
		return nil, ErrTelemetryUnsupported
	}
	if clusterID == uuid.Nil {
		return r.clusters.FindDefault(ctx)
	}
	return r.clusters.FindByID(ctx, clusterID)
}

func collectorFrom(o ports.Orchestrator, clusterID uuid.UUID) (ports.TelemetryCollector, error) {
	tp, ok := o.(ports.TelemetryProvider)
	if !ok {
		return nil, ErrTelemetryUnsupported
	}
	return tp.Collector(clusterID), nil
}

var _ ports.TelemetryCollectorRegistry = (*CollectorRegistry)(nil)
