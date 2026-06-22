// Package telemetry implements the ports.TelemetryCollector port. DirectCollector
// reads the Docker API of an agentless cluster; the agent-tunnel collector lands
// in a later slice. See docs/adr/0002-monitoring-and-alerting.md.
package telemetry

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/swarm"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/ports"
)

// hivemindServiceLabel is the swarm-service label carrying the Hivemind service
// UUID (set by the orchestrator when it creates the service).
const hivemindServiceLabel = "hivemind.service.id"

// restartWindow bounds how far back a task failure counts toward the crash-loop
// signal. Swarm keeps terminal tasks around, so without this a service's lifetime
// failures would falsely read as an ongoing crash loop.
const restartWindow = 15 * time.Minute

// swarmAPI is the subset of the Docker client DirectCollector needs. The real
// *client.Client satisfies it; tests inject a fake. ContainerList/ContainerStats
// are node-local (the daemon we talk to), which is exactly the direct-mode
// coverage.
type swarmAPI interface {
	TaskList(ctx context.Context, options types.TaskListOptions) ([]swarm.Task, error)
	NodeList(ctx context.Context, options types.NodeListOptions) ([]swarm.Node, error)
	ServiceList(ctx context.Context, options types.ServiceListOptions) ([]swarm.Service, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]types.Container, error)
	ContainerStats(ctx context.Context, containerID string, stream bool) (container.StatsResponseReader, error)
}

// Compile-time guarantee that the collector satisfies the port.
var _ ports.TelemetryCollector = (*DirectCollector)(nil)

// DirectCollector implements ports.TelemetryCollector for an agentless cluster by
// reading the Docker API of the cluster's manager directly. Health is cluster-wide
// (built from the Swarm task list); metrics cover only the connected daemon's
// node (node-local container stats — see ADR 0002).
type DirectCollector struct {
	api       swarmAPI
	clusterID uuid.UUID
	now       func() time.Time
}

// NewDirectCollector builds a collector over a Docker client (*client.Client
// satisfies swarmAPI). clusterID stamps the resulting snapshots.
func NewDirectCollector(api swarmAPI, clusterID uuid.UUID) *DirectCollector {
	return &DirectCollector{api: api, clusterID: clusterID, now: time.Now}
}

// Capabilities reports the direct-mode coverage: metrics cover only the connected
// node, and there is no per-node tunnel health.
func (c *DirectCollector) Capabilities() ports.CollectorCapabilities {
	return ports.CollectorCapabilities{
		MetricsCoverage:     ports.MetricsConnectedNode,
		PerNodeTunnelHealth: false,
	}
}

// instanceKey identifies a logical service instance so historical (failed)
// tasks fold into one current container. Replicated services key by slot; global
// services (slot 0) key by node.
type instanceKey struct {
	service string
	slot    int
	node    string
}

func keyOf(t swarm.Task) instanceKey {
	if t.Slot > 0 {
		return instanceKey{service: t.ServiceID, slot: t.Slot}
	}
	return instanceKey{service: t.ServiceID, node: t.NodeID}
}

// CollectHealth returns the per-node health snapshot for the cluster: the current
// task of every service instance, classified and grouped by node — including
// nodes that currently run nothing.
// unscheduledNodeLabel is the display name given to the synthetic bucket that
// holds tasks not yet placed on any node (empty Swarm NodeID), so the UI shows a
// clear "unscheduled" group instead of a phantom unreachable node.
const unscheduledNodeLabel = "Tâches non planifiées"

func (c *DirectCollector) CollectHealth(ctx context.Context) (*monitoring.ClusterHealth, error) {
	tasks, err := c.api.TaskList(ctx, types.TaskListOptions{})
	if err != nil {
		return nil, fmt.Errorf("task list: %w", err)
	}
	nodes, err := c.api.NodeList(ctx, types.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("node list: %w", err)
	}
	services, err := c.api.ServiceList(ctx, types.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("service list: %w", err)
	}

	svcByID := indexServices(services)

	// Keep the most recent task per instance, and count *recent* failures per
	// instance as the crash-loop signal. The window matters: Swarm retains
	// terminal tasks, so a long-lived service accumulates old failures over its
	// lifetime — counting those would flag a perfectly healthy service as
	// crash-looping. A crash loop is repeated failures *now*, not ever.
	cutoff := c.now().Add(-restartWindow)
	current := make(map[instanceKey]swarm.Task)
	fails := make(map[instanceKey]int)
	for _, t := range tasks {
		k := keyOf(t)
		switch t.Status.State {
		case swarm.TaskStateFailed, swarm.TaskStateRejected:
			if t.CreatedAt.After(cutoff) {
				fails[k]++
			}
		}
		if prev, ok := current[k]; !ok || t.CreatedAt.After(prev.CreatedAt) {
			current[k] = t
		}
	}

	byNode := make(map[string][]monitoring.ContainerHealth)
	for k, t := range current {
		restarts := fails[k]
		// Don't count the current instance itself as a restart.
		if t.Status.State == swarm.TaskStateFailed || t.Status.State == swarm.TaskStateRejected {
			if restarts > 0 {
				restarts--
			}
		}

		verdict, reason := monitoring.Classify(string(t.Status.State), string(t.DesiredState), t.Status.Err, restarts)
		ident := svcByID[t.ServiceID]
		ch := monitoring.ContainerHealth{
			TaskID:      t.ID,
			ServiceID:   ident.id,
			ServiceName: ident.name,
			NodeID:      t.NodeID,
			Slot:        t.Slot,
			State:       string(t.Status.State),
			Verdict:     verdict,
			Reason:      reason,
			Restarts:    restarts,
			Since:       t.CreatedAt,
		}
		if cs := t.Status.ContainerStatus; cs != nil {
			ch.ContainerID = cs.ContainerID
			ec := cs.ExitCode
			ch.ExitCode = &ec
		}
		byNode[t.NodeID] = append(byNode[t.NodeID], ch)
	}

	out := &monitoring.ClusterHealth{ClusterID: c.clusterID, ObservedAt: c.now()}
	for _, n := range nodes {
		nh := monitoring.NodeHealth{
			NodeID:      n.ID,
			Hostname:    n.Description.Hostname,
			Role:        string(n.Spec.Role),
			Reachable:   n.Status.State == swarm.NodeStateReady,
			CPUs:        float64(n.Description.Resources.NanoCPUs) / 1e9,
			MemoryBytes: uint64(n.Description.Resources.MemoryBytes),
			Containers:  byNode[n.ID],
		}
		nh.Recount()
		out.Nodes = append(out.Nodes, nh)
		delete(byNode, n.ID)
	}
	// Tasks whose node isn't in NodeList fall into two cases. A non-empty node id
	// is a node that left the cluster with tasks still pinned — surface it as
	// unreachable so it stays visible. An empty node id means the tasks aren't
	// scheduled on any node yet (pending, or a crash-looping task between
	// placements): that is NOT a node-down condition, so bucket it under a clear
	// label and keep it reachable. The per-task critical verdicts (crash-loop /
	// unschedulable) still alert on their own — without a phantom "unreachable
	// node" and its false node-down alert.
	for nodeID, chs := range byNode {
		nh := monitoring.NodeHealth{NodeID: nodeID, Reachable: false, Containers: chs}
		if nodeID == "" {
			nh.Hostname = unscheduledNodeLabel
			nh.Reachable = true
		}
		nh.Recount()
		out.Nodes = append(out.Nodes, nh)
	}

	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].NodeID < out.Nodes[j].NodeID })
	return out, nil
}

type svcIdentity struct {
	id   uuid.UUID
	name string
}

func indexServices(services []swarm.Service) map[string]svcIdentity {
	m := make(map[string]svcIdentity, len(services))
	for _, s := range services {
		ident := svcIdentity{name: s.Spec.Name}
		if raw := s.Spec.Labels[hivemindServiceLabel]; raw != "" {
			if u, err := uuid.Parse(raw); err == nil {
				ident.id = u
			}
		}
		m[s.ID] = ident
	}
	return m
}
