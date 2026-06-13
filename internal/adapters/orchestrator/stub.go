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
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/ports"
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
	return &ports.ServiceState{Running: 1, Desired: 1}, nil
}

func (*StubOrchestrator) WaitConvergence(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (*StubOrchestrator) ServiceLogs(_ context.Context, swarmServiceID string, _ ports.LogOptions) (io.ReadCloser, error) {
	lines := "stub: log streaming is simulated; nothing is read from Docker\n" +
		"stub: wire the Swarm orchestrator for real service logs\n"
	return io.NopCloser(strings.NewReader(lines)), nil
}

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

func (*StubOrchestrator) CreateNetwork(_ context.Context, name string, _ bool) (string, error) {
	slog.Info("stub: create network", "name", name)
	return fakeID("net"), nil
}

func (*StubOrchestrator) RemoveNetwork(_ context.Context, swarmNetworkID string) error { return nil }
