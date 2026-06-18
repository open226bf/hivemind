package orchestrator

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/ports"
)

// ErrTelemetryUnsupported is returned when a cluster's orchestrator cannot
// provide telemetry — the stub backend, or an agent-mode cluster until the agent
// collector lands.
var ErrTelemetryUnsupported = errors.New("telemetry not supported for this cluster")

// CollectorRegistry resolves a cluster id to a ports.TelemetryCollector by
// reusing the orchestrator already resolved (and cached) for that cluster: when
// the orchestrator implements ports.TelemetryProvider it derives a collector
// from the live connection. This keeps the orchestrator registry the single seam
// for multi-cluster resolution (one Docker connection per cluster, with its
// caching and invalidation), rather than duplicating the TLS/agent wiring.
type CollectorRegistry struct {
	orch ports.OrchestratorRegistry
}

// NewCollectorRegistry wraps an orchestrator registry.
func NewCollectorRegistry(orch ports.OrchestratorRegistry) *CollectorRegistry {
	return &CollectorRegistry{orch: orch}
}

// For resolves the collector for a cluster. The zero UUID is the default cluster.
func (r *CollectorRegistry) For(ctx context.Context, clusterID uuid.UUID) (ports.TelemetryCollector, error) {
	o, err := r.orch.For(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	return collectorFrom(o, clusterID)
}

// Default resolves the collector for the default cluster.
func (r *CollectorRegistry) Default(ctx context.Context) (ports.TelemetryCollector, error) {
	o, err := r.orch.Default(ctx)
	if err != nil {
		return nil, err
	}
	return collectorFrom(o, uuid.Nil)
}

func collectorFrom(o ports.Orchestrator, clusterID uuid.UUID) (ports.TelemetryCollector, error) {
	tp, ok := o.(ports.TelemetryProvider)
	if !ok {
		return nil, ErrTelemetryUnsupported
	}
	return tp.Collector(clusterID), nil
}

var _ ports.TelemetryCollectorRegistry = (*CollectorRegistry)(nil)
