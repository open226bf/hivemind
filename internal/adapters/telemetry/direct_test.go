package telemetry

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/swarm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/domain/monitoring"
	"github.com/orange/hivemind/internal/ports"
)

// fakeSwarm is a canned swarmAPI for the collector tests.
type fakeSwarm struct {
	tasks      []swarm.Task
	nodes      []swarm.Node
	services   []swarm.Service
	containers []types.Container
	statsJSON  string // two concatenated stat frames, returned for any container
}

func (f fakeSwarm) TaskList(context.Context, types.TaskListOptions) ([]swarm.Task, error) {
	return f.tasks, nil
}
func (f fakeSwarm) NodeList(context.Context, types.NodeListOptions) ([]swarm.Node, error) {
	return f.nodes, nil
}
func (f fakeSwarm) ServiceList(context.Context, types.ServiceListOptions) ([]swarm.Service, error) {
	return f.services, nil
}
func (f fakeSwarm) ContainerList(context.Context, container.ListOptions) ([]types.Container, error) {
	return f.containers, nil
}
func (f fakeSwarm) ContainerStats(_ context.Context, _ string, _ bool) (container.StatsResponseReader, error) {
	return container.StatsResponseReader{Body: io.NopCloser(strings.NewReader(f.statsJSON))}, nil
}

func task(id, svc string, slot int, node string, state, desired swarm.TaskState, errMsg string, ageMin int) swarm.Task {
	return swarm.Task{
		ID:           id,
		ServiceID:    svc,
		Slot:         slot,
		NodeID:       node,
		DesiredState: desired,
		Status:       swarm.TaskStatus{State: state, Err: errMsg},
		Meta:         swarm.Meta{CreatedAt: time.Now().Add(-time.Duration(ageMin) * time.Minute)},
	}
}

func node(id, host, role string, state swarm.NodeState) swarm.Node {
	return swarm.Node{
		ID: id,
		Description: swarm.NodeDescription{
			Hostname:  host,
			Resources: swarm.Resources{NanoCPUs: 4e9, MemoryBytes: 8 << 30}, // 4 cores, 8 GiB
		},
		Spec:   swarm.NodeSpec{Annotations: swarm.Annotations{}, Role: swarm.NodeRole(role)},
		Status: swarm.NodeStatus{State: state},
	}
}

func TestDirectCollector_CollectHealth(t *testing.T) {
	webID := uuid.New()

	fake := fakeSwarm{
		nodes: []swarm.Node{
			node("node-a", "alpha", "manager", swarm.NodeStateReady),
			node("node-b", "bravo", "worker", swarm.NodeStateReady),
			node("node-c", "charlie", "worker", swarm.NodeStateDown), // empty + down
		},
		services: []swarm.Service{
			{ID: "svc-web", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{
				Name:   "web",
				Labels: map[string]string{hivemindServiceLabel: webID.String()},
			}}},
			{ID: "svc-db", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "db"}}}, // no label
		},
		tasks: []swarm.Task{
			// web slot 1 on node-a: rolling update — running task is newer than the shutdown one.
			task("w1-old", "svc-web", 1, "node-a", swarm.TaskStateShutdown, swarm.TaskStateShutdown, "", 10),
			task("w1-new", "svc-web", 1, "node-a", swarm.TaskStateRunning, swarm.TaskStateRunning, "", 2),
			// web slot 2 on node-b: crash-looping — current failed + 3 historical failures (4 total).
			task("w2-h1", "svc-web", 2, "node-b", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 9),
			task("w2-h2", "svc-web", 2, "node-b", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 7),
			task("w2-h3", "svc-web", 2, "node-b", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 5),
			task("w2-cur", "svc-web", 2, "node-b", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 1),
			// db is a global service (slot 0) on node-a, stuck unschedulable.
			task("d1", "svc-db", 0, "node-a", swarm.TaskStatePending, swarm.TaskStateRunning, "no suitable node", 3),
		},
	}

	cid := uuid.New()
	c := NewDirectCollector(fake, cid)
	c.now = func() time.Time { return time.Unix(0, 0) }

	got, err := c.CollectHealth(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, cid, got.ClusterID)
	require.Len(t, got.Nodes, 3)

	byNode := map[string]monitoring.NodeHealth{}
	for _, n := range got.Nodes {
		byNode[n.NodeID] = n
	}

	// node-a: web/slot1 running (ok) + db pending unschedulable (critical).
	a := byNode["node-a"]
	assert.Equal(t, "alpha", a.Hostname)
	assert.Equal(t, "manager", a.Role)
	assert.True(t, a.Reachable)
	assert.Equal(t, 4.0, a.CPUs)                  // capacity from NodeList
	assert.Equal(t, uint64(8)<<30, a.MemoryBytes) // 8 GiB
	assert.Equal(t, 1, a.OK)
	assert.Equal(t, 1, a.Critical)
	assert.Equal(t, monitoring.SeverityCritical, a.Worst())

	// node-b: web/slot2 crash-looping (critical).
	b := byNode["node-b"]
	require.Len(t, b.Containers, 1)
	assert.Equal(t, monitoring.SeverityCritical, b.Containers[0].Verdict)
	assert.Contains(t, b.Containers[0].Reason, "crash-looping")
	assert.GreaterOrEqual(t, b.Containers[0].Restarts, monitoring.CrashLoopThreshold)
	assert.Equal(t, webID, b.Containers[0].ServiceID)
	assert.Equal(t, "web", b.Containers[0].ServiceName)

	// node-c: down, no containers.
	cNode := byNode["node-c"]
	assert.False(t, cNode.Reachable)
	assert.Empty(t, cNode.Containers)

	// rolling update: the live task is the running one, not the shutdown.
	var webSlot1 *monitoring.ContainerHealth
	for i := range a.Containers {
		if a.Containers[i].Slot == 1 && a.Containers[i].ServiceName == "web" {
			webSlot1 = &a.Containers[i]
		}
	}
	require.NotNil(t, webSlot1)
	assert.Equal(t, "running", webSlot1.State)
	assert.Equal(t, monitoring.SeverityOK, webSlot1.Verdict)

	// db has no hivemind label → zero UUID, name still resolved.
	var db *monitoring.ContainerHealth
	for i := range a.Containers {
		if a.Containers[i].ServiceName == "db" {
			db = &a.Containers[i]
		}
	}
	require.NotNil(t, db)
	assert.Equal(t, uuid.Nil, db.ServiceID)
	assert.Equal(t, monitoring.SeverityCritical, db.Verdict)
	assert.Contains(t, db.Reason, "no suitable node")

	// Struggling = the two criticals (web/slot2 + db); the running web/slot1 excluded.
	assert.Len(t, got.Struggling(), 2)
}

func TestDirectCollector_OldFailuresAreNotCrashloop(t *testing.T) {
	// A slot whose failures are all older than the restart window, with a current
	// running task, must read healthy — not crash-looping. (Swarm retains old
	// terminal tasks; lifetime failures must not flag a long-stable service.)
	fake := fakeSwarm{
		nodes:    []swarm.Node{node("node-a", "alpha", "manager", swarm.NodeStateReady)},
		services: []swarm.Service{{ID: "svc-web", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}}}},
		tasks: []swarm.Task{
			task("old1", "svc-web", 1, "node-a", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 90),
			task("old2", "svc-web", 1, "node-a", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 70),
			task("old3", "svc-web", 1, "node-a", swarm.TaskStateFailed, swarm.TaskStateRunning, "boom", 60),
			task("cur", "svc-web", 1, "node-a", swarm.TaskStateRunning, swarm.TaskStateRunning, "", 1),
		},
	}
	c := NewDirectCollector(fake, uuid.New()) // default now → real 15-minute window

	got, err := c.CollectHealth(context.Background())
	require.NoError(t, err)
	require.Len(t, got.Nodes, 1)
	require.Len(t, got.Nodes[0].Containers, 1)
	ch := got.Nodes[0].Containers[0]
	assert.Equal(t, monitoring.SeverityOK, ch.Verdict)
	assert.Equal(t, 0, ch.Restarts)
}

func TestDirectCollector_Capabilities(t *testing.T) {
	caps := NewDirectCollector(fakeSwarm{}, uuid.New()).Capabilities()
	assert.Equal(t, ports.MetricsConnectedNode, caps.MetricsCoverage)
	assert.False(t, caps.PerNodeTunnelHealth)
}

func TestDirectCollector_CollectMetrics(t *testing.T) {
	// Two stat frames: cpuDelta=1000, sysDelta=10000, 2 online CPUs → 20% CPU;
	// mem usage 200 MiB − 50 MiB inactive = 150 MiB of a 1 GiB limit ≈ 14.6%.
	const frames = `{}` + `
{"cpu_stats":{"cpu_usage":{"total_usage":2000},"system_cpu_usage":20000,"online_cpus":2},
 "precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":10000},
 "memory_stats":{"usage":209715200,"limit":1073741824,"stats":{"inactive_file":52428800}}}`

	fake := fakeSwarm{
		containers: []types.Container{
			{ID: "c1", Labels: map[string]string{"com.docker.swarm.node.id": "node-a"}},
		},
		statsJSON: frames,
	}
	c := NewDirectCollector(fake, uuid.New())

	samples, err := c.CollectMetrics(context.Background())
	require.NoError(t, err)
	require.Len(t, samples, 1)

	s := samples[0]
	assert.Equal(t, "c1", s.ContainerID)
	assert.Equal(t, "node-a", s.NodeID)
	assert.InDelta(t, 20.0, s.CPUPercent, 0.01)
	assert.Equal(t, uint64(157286400), s.MemUsedBytes) // 150 MiB
	assert.Equal(t, uint64(1073741824), s.MemLimitBytes)
	assert.InDelta(t, 14.65, s.MemPercent, 0.1)
}
