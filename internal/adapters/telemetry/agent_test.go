package telemetry

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/swarm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/ports"
)

// nodeOrch is a ports.Orchestrator that also provides telemetry over a given
// swarmAPI — like *SwarmOrchestrator. The embedded nil interface satisfies the
// rest of ports.Orchestrator (unused here); only Collector is exercised.
type nodeOrch struct {
	ports.Orchestrator
	api swarmAPI
}

func (o nodeOrch) Collector(id uuid.UUID) ports.TelemetryCollector {
	return NewDirectCollector(o.api, id)
}

// fakeHub serves a manager orchestrator (cluster-wide health) plus a per-node
// orchestrator for each connected node (that node's local Docker).
type fakeHub struct {
	manager swarmAPI
	nodes   map[string]swarmAPI
}

func (h fakeHub) Orchestrator(context.Context, string) (ports.Orchestrator, error) {
	return nodeOrch{api: h.manager}, nil
}
func (h fakeHub) OrchestratorForNode(_ context.Context, _, nodeID string) (ports.Orchestrator, error) {
	api, ok := h.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not connected", nodeID)
	}
	return nodeOrch{api: api}, nil
}
func (h fakeHub) ConnectedNodeIDs(string) map[string]bool {
	m := make(map[string]bool, len(h.nodes))
	for id := range h.nodes {
		m[id] = true
	}
	return m
}

func TestAgentCollector(t *testing.T) {
	const frames = `{}` + `
{"cpu_stats":{"cpu_usage":{"total_usage":2000},"system_cpu_usage":20000,"online_cpus":2},
 "precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":10000},
 "memory_stats":{"usage":104857600,"limit":1073741824}}`

	// Manager view: the two nodes (cluster-wide health source).
	mgr := fakeSwarm{nodes: []swarm.Node{
		node("n1", "alpha", "manager", swarm.NodeStateReady),
		node("n2", "bravo", "worker", swarm.NodeStateReady),
	}}
	// Each node serves its own local container's stats.
	n1 := fakeSwarm{containers: []types.Container{{ID: "c-n1", Labels: map[string]string{"com.docker.swarm.node.id": "n1"}}}, statsJSON: frames}
	n2 := fakeSwarm{containers: []types.Container{{ID: "c-n2", Labels: map[string]string{"com.docker.swarm.node.id": "n2"}}}, statsJSON: frames}

	hub := fakeHub{manager: mgr, nodes: map[string]swarmAPI{"n1": n1, "n2": n2}}
	c := NewAgentCollector(hub, "agent-x", uuid.New())

	caps := c.Capabilities()
	assert.Equal(t, ports.MetricsClusterWide, caps.MetricsCoverage)
	assert.True(t, caps.PerNodeTunnelHealth)

	// Health: cluster-wide nodes (from the manager) with per-node tunnel set.
	h, err := c.CollectHealth(context.Background())
	require.NoError(t, err)
	require.Len(t, h.Nodes, 2)
	for _, n := range h.Nodes {
		require.NotNil(t, n.TunnelUp)
		assert.True(t, *n.TunnelUp)
	}

	// Metrics: aggregated across BOTH nodes — the cluster-wide coverage.
	samples, err := c.CollectMetrics(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 2)
	ids := map[string]bool{}
	for _, s := range samples {
		ids[s.ContainerID] = true
		assert.InDelta(t, 20.0, s.CPUPercent, 0.01)
	}
	assert.True(t, ids["c-n1"] && ids["c-n2"])
}
