package orchestrator

import (
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/telemetry"
	"github.com/open226bf/hivemind/internal/ports"
)

// Collector returns a telemetry collector backed by this orchestrator's live
// Docker client, so monitoring reuses the cluster connection the registry
// already manages. Satisfies ports.TelemetryProvider.
func (o *SwarmOrchestrator) Collector(clusterID uuid.UUID) ports.TelemetryCollector {
	return telemetry.NewDirectCollector(o.cli, clusterID)
}

var _ ports.TelemetryProvider = (*SwarmOrchestrator)(nil)
