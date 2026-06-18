package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/orange/hivemind/internal/domain/monitoring"
)

// TelemetryCollector is the driven port that produces a cluster's observability
// data. It mirrors Orchestrator: one interface, two adapters chosen by the
// cluster's connection mode and resolved through TelemetryCollectorRegistry —
//
//   - direct (agentless): reads the Docker API of the cluster endpoint;
//   - agent: consumes telemetry pushed over the agent's reverse tunnel.
//
// Everything downstream (alert engine, API, UI) consumes this port and never
// learns the connection mode. Capabilities() lets callers degrade gracefully
// where a mode cannot deliver a signal (see ADR 0002).
type TelemetryCollector interface {
	// CollectHealth returns the per-node health snapshot: every task/container
	// with a normalised verdict, grouped by node. Cluster-wide in both modes
	// (built from the Swarm task list) — the data behind "what is struggling and
	// where".
	CollectHealth(ctx context.Context) (*monitoring.ClusterHealth, error)

	// StreamMetrics streams per-container resource samples until ctx is done.
	// Agent mode covers the whole cluster (node-local stats pushed over the
	// tunnel); direct mode covers only the connected daemon's node — callers
	// should consult Capabilities first.
	StreamMetrics(ctx context.Context, opts MetricStreamOptions) (<-chan monitoring.MetricSample, error)

	// Capabilities reports what this collector can actually deliver for its
	// cluster, so callers and the UI adapt without knowing the connection mode.
	Capabilities() CollectorCapabilities
}

// MetricStreamOptions narrows a metrics stream.
type MetricStreamOptions struct {
	// Interval between samples for a given container.
	Interval time.Duration
	// ServiceID, when non-nil, limits the stream to one service's containers.
	ServiceID *uuid.UUID
}

// MetricsCoverage describes how much of a cluster a collector's metrics stream
// observes — the key asymmetry between the two connection modes (ADR 0002).
type MetricsCoverage string

const (
	MetricsClusterWide   MetricsCoverage = "cluster"        // agent mode: every node
	MetricsConnectedNode MetricsCoverage = "connected-node" // direct mode: only the daemon we talk to
)

// CollectorCapabilities advertises a collector's coverage so callers/UI can
// degrade gracefully (e.g. hide cluster-wide CPU on a direct cluster).
type CollectorCapabilities struct {
	MetricsCoverage MetricsCoverage
	// PerNodeTunnelHealth is true in agent mode (NodeHealth.TunnelUp is set).
	PerNodeTunnelHealth bool
}

// TelemetryCollectorRegistry resolves a cluster id to its TelemetryCollector,
// exactly like OrchestratorRegistry does for Orchestrator. The zero UUID is the
// default cluster.
type TelemetryCollectorRegistry interface {
	For(ctx context.Context, clusterID uuid.UUID) (TelemetryCollector, error)
	Default(ctx context.Context) (TelemetryCollector, error)
}

// TelemetryProvider is implemented by an Orchestrator that can also expose
// telemetry for its own cluster connection (e.g. the Swarm orchestrator, which
// already holds a Docker client). The collector registry derives a collector
// from the resolved orchestrator through this interface, reusing the live
// connection instead of opening a second one.
type TelemetryProvider interface {
	Collector(clusterID uuid.UUID) TelemetryCollector
}

// AlertRouter is the output port the alert engine calls when a rule fires or
// resolves. Implementations fan out to notification channels (Slack, email,
// webhooks) — the same plumbing as the existing Notifier — and may forward to an
// external Alertmanager (v2 API) when configured (ADR 0002, phase 3).
type AlertRouter interface {
	Route(ctx context.Context, alert monitoring.Alert) error
}
