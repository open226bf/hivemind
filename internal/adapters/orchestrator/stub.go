// Package orchestrator contains adapters implementing ports.Orchestrator.
//
// StubOrchestrator is a non-functional placeholder used until the real Docker
// Swarm adapter (F-MVP-08 follow-up) is wired. It records nothing on a real
// cluster: it returns deterministic fake object ids and reports immediate
// convergence, so the full deployment flow (spec assembly, persistence, status
// transitions, notifications) can be exercised end-to-end without Docker.
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/ports"
)

// StubOrchestrator implements ports.Orchestrator without touching Docker.
type StubOrchestrator struct{}

func NewStubOrchestrator() *StubOrchestrator {
	slog.Warn("using STUB orchestrator — deployments are simulated and nothing is applied to Docker Swarm; wire the real adapter for production")
	return &StubOrchestrator{}
}

func fakeID(prefix string) string { return prefix + "-" + uuid.NewString() }

func (*StubOrchestrator) DeployService(_ context.Context, spec ports.ServiceSpec) (string, error) {
	slog.Info("stub: deploy service", "name", spec.Name, "image", spec.Image, "replicas", spec.Replicas)
	return fakeID("svc"), nil
}

func (*StubOrchestrator) UpdateService(_ context.Context, swarmServiceID string, spec ports.ServiceSpec) error {
	slog.Info("stub: update service", "swarm_id", swarmServiceID, "image", spec.Image, "replicas", spec.Replicas)
	return nil
}

func (*StubOrchestrator) RemoveService(_ context.Context, swarmServiceID string) error {
	slog.Info("stub: remove service", "swarm_id", swarmServiceID)
	return nil
}

func (*StubOrchestrator) GetServiceState(_ context.Context, swarmServiceID string) (*ports.ServiceState, error) {
	// Simulate a single running task so the supervision view (containers, node,
	// per-task IP) is exercisable without a real Swarm. The container ID and IP
	// are derived deterministically from the service ID so they stay stable
	// across the frontend's polling refreshes.
	now := time.Now()
	octet := stubOctet(swarmServiceID)
	clean := strings.ReplaceAll(swarmServiceID, "-", "")
	if len(clean) > 12 {
		clean = clean[:12]
	}
	task := ports.TaskState{
		ID:           fakeID("task"),
		ContainerID:  "stub" + clean,
		Node:         "stub-node-1",
		Image:        "stub/image:latest",
		Slot:         1,
		CurrentState: "running",
		DesiredState: "running",
		PID:          1000 + octet,
		Networks: []ports.TaskNetwork{
			{Name: "hivemind-net", Address: fmt.Sprintf("10.0.1.%d/24", octet)},
		},
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now,
	}
	return &ports.ServiceState{Running: 1, Desired: 1, Tasks: []ports.TaskState{task}}, nil
}

// stubOctet maps a swarm service ID to a stable host octet in the range 2..254.
func stubOctet(id string) int {
	var sum int
	for _, b := range []byte(id) {
		sum += int(b)
	}
	return sum%253 + 2
}

func (*StubOrchestrator) WaitConvergence(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (*StubOrchestrator) ServiceLogs(_ context.Context, swarmServiceID string, _ ports.LogOptions) (io.ReadCloser, error) {
	lines := "stub: log streaming is simulated; nothing is read from Docker\n" +
		"stub: wire the Swarm orchestrator for real service logs\n"
	return io.NopCloser(strings.NewReader(lines)), nil
}

func (*StubOrchestrator) ExecContainer(_ context.Context, containerID string, _ ports.ExecOptions) (ports.ExecStream, error) {
	return &stubExecStream{r: strings.NewReader(
		"stub: interactive exec is not available without a real Swarm orchestrator\r\n")}, nil
}

// stubExecStream emits a notice then EOF; writes are discarded.
type stubExecStream struct{ r *strings.Reader }

func (s *stubExecStream) Read(p []byte) (int, error)               { return s.r.Read(p) }
func (s *stubExecStream) Write(p []byte) (int, error)              { return len(p), nil }
func (s *stubExecStream) Close() error                             { return nil }
func (s *stubExecStream) Resize(context.Context, uint, uint) error { return nil }

func (*StubOrchestrator) CreateSecret(_ context.Context, name string, _ []byte) (string, error) {
	slog.Info("stub: create secret", "name", name)
	return fakeID("secret"), nil
}

func (*StubOrchestrator) RemoveSecret(_ context.Context, swarmSecretID string) error { return nil }

func (*StubOrchestrator) CreateConfig(_ context.Context, name string, _ []byte) (string, error) {
	slog.Info("stub: create config", "name", name)
	return fakeID("config"), nil
}

func (*StubOrchestrator) RemoveConfig(_ context.Context, swarmConfigID string) error { return nil }

func (*StubOrchestrator) CreateNetwork(_ context.Context, name string, _ ports.CreateNetworkOptions) (string, error) {
	slog.Info("stub: create network", "name", name)
	return fakeID("net"), nil
}

func (*StubOrchestrator) RemoveNetwork(_ context.Context, swarmNetworkID string) error { return nil }

func (*StubOrchestrator) ListNetworks(_ context.Context) ([]ports.SwarmNetworkInfo, error) {
	return []ports.SwarmNetworkInfo{
		{ID: "stub-ingress", Name: "ingress", Scope: "swarm", Driver: "overlay", Subnet: "10.0.0.0/24"},
	}, nil
}

func (*StubOrchestrator) CreateVolume(_ context.Context, name, driver string) error {
	slog.Info("stub: create volume", "name", name, "driver", driver)
	return nil
}

func (*StubOrchestrator) RemoveVolume(_ context.Context, name string) error { return nil }

func (*StubOrchestrator) ListVolumes(_ context.Context) ([]ports.SwarmVolumeInfo, error) {
	return []ports.SwarmVolumeInfo{
		{Name: "stub-data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/stub-data/_data", Scope: "local"},
	}, nil
}

// ClusterInfo returns a deterministic 3-node topology (one manager-leader plus
// two workers) so the cluster dashboard is fully exercisable without a real
// Swarm. Capacities are fixed sample values.
func (*StubOrchestrator) ClusterInfo(_ context.Context) (*ports.ClusterInfo, error) {
	const gib = 1 << 30
	return &ports.ClusterInfo{Nodes: []ports.NodeInfo{
		{
			ID: "stub-node-1", Hostname: "stub-manager-1", Role: "manager", Leader: true,
			Availability: "active", State: "ready", Addr: "10.0.0.1", EngineVersion: "27.5.1",
			CPUs: 4, MemoryBytes: 8 * gib, Platform: "linux/x86_64",
		},
		{
			ID: "stub-node-2", Hostname: "stub-worker-1", Role: "worker",
			Availability: "active", State: "ready", Addr: "10.0.0.2", EngineVersion: "27.5.1",
			CPUs: 8, MemoryBytes: 16 * gib, Platform: "linux/x86_64",
		},
		{
			ID: "stub-node-3", Hostname: "stub-worker-2", Role: "worker",
			Availability: "drain", State: "down", Addr: "10.0.0.3", EngineVersion: "27.5.1",
			CPUs: 8, MemoryBytes: 16 * gib, Platform: "linux/x86_64",
		},
	}}, nil
}
