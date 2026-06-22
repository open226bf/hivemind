package telemetry

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/ports"
)

// agentHub is the subset of ports.AgentHub the AgentCollector needs (ports.AgentHub
// satisfies it).
type agentHub interface {
	Orchestrator(ctx context.Context, agentID string) (ports.Orchestrator, error)
	OrchestratorForNode(ctx context.Context, agentID, nodeID string) (ports.Orchestrator, error)
	ConnectedNodeIDs(agentID string) map[string]bool
	NodeMetricsByNode(agentID string) map[string]ports.NodeMetrics
}

// AgentCollector implements ports.TelemetryCollector for an agent-mode cluster.
// Health is cluster-wide over the manager tunnel (it reuses DirectCollector);
// metrics fan out per node over each node's own tunnel and aggregate — giving
// **cluster-wide** per-container CPU/mem, the coverage direct mode can't reach
// (see docs/adr/0002-monitoring-and-alerting.md). The agent already proxies each
// node's Docker socket, so no agent-side change is needed.
type AgentCollector struct {
	hub       agentHub
	agentID   string
	clusterID uuid.UUID
}

// NewAgentCollector builds a collector over an agent's tunnels.
func NewAgentCollector(hub agentHub, agentID string, clusterID uuid.UUID) *AgentCollector {
	return &AgentCollector{hub: hub, agentID: agentID, clusterID: clusterID}
}

// Capabilities: cluster-wide metrics and per-node tunnel health.
func (c *AgentCollector) Capabilities() ports.CollectorCapabilities {
	return ports.CollectorCapabilities{
		MetricsCoverage:     ports.MetricsClusterWide,
		PerNodeTunnelHealth: true,
	}
}

// CollectHealth returns cluster-wide health over the manager tunnel, then stamps
// each node's tunnel liveness from the hub.
func (c *AgentCollector) CollectHealth(ctx context.Context) (*monitoring.ClusterHealth, error) {
	col, err := c.managerCollector(ctx)
	if err != nil {
		return nil, err
	}
	h, err := col.CollectHealth(ctx)
	if err != nil {
		return nil, err
	}

	connected := c.hub.ConnectedNodeIDs(c.agentID)
	usage := c.hub.NodeMetricsByNode(c.agentID)
	for i := range h.Nodes {
		if h.Nodes[i].NodeID == "" {
			continue // unscheduled-tasks bucket — not a real node, so no tunnel status
		}
		up := connected[h.Nodes[i].NodeID]
		h.Nodes[i].TunnelUp = &up
		if m, ok := usage[h.Nodes[i].NodeID]; ok {
			h.Nodes[i].HostUsage = &monitoring.HostUsage{
				CPUPercent:    m.CPUPercent,
				MemUsedBytes:  m.MemUsedBytes,
				MemTotalBytes: m.MemTotalBytes,
			}
		}
	}
	return h, nil
}

// CollectMetrics fans out over each connected node's tunnel and aggregates the
// per-node container stats. Nodes without a live tunnel (or that drop mid-scan)
// are skipped rather than failing the whole snapshot.
func (c *AgentCollector) CollectMetrics(ctx context.Context) ([]monitoring.MetricSample, error) {
	var all []monitoring.MetricSample
	for nodeID, up := range c.hub.ConnectedNodeIDs(c.agentID) {
		if !up {
			continue
		}
		orch, err := c.hub.OrchestratorForNode(ctx, c.agentID, nodeID)
		if err != nil {
			continue
		}
		tp, ok := orch.(ports.TelemetryProvider)
		if !ok {
			continue
		}
		samples, err := tp.Collector(c.clusterID).CollectMetrics(ctx)
		if err != nil {
			continue
		}
		// This batch is the node's local containers; standalone (non-swarm)
		// containers carry no node label, so stamp the known node id where it's
		// missing — otherwise they collapse under an empty id into a phantom node.
		for i := range samples {
			if samples[i].NodeID == "" {
				samples[i].NodeID = nodeID
			}
		}
		all = append(all, samples...)
	}
	return all, nil
}

func (c *AgentCollector) managerCollector(ctx context.Context) (ports.TelemetryCollector, error) {
	orch, err := c.hub.Orchestrator(ctx, c.agentID)
	if err != nil {
		return nil, fmt.Errorf("agent manager orchestrator: %w", err)
	}
	tp, ok := orch.(ports.TelemetryProvider)
	if !ok {
		return nil, fmt.Errorf("agent orchestrator does not provide telemetry")
	}
	return tp.Collector(c.clusterID), nil
}

var _ ports.TelemetryCollector = (*AgentCollector)(nil)
