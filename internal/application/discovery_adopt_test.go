package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/adapters/orchestrator"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/snapshot"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// adoptStub embeds the stub orchestrator and lets a test drive InspectService
// while recording the label seal/clear calls.
type adoptStub struct {
	*orchestrator.StubOrchestrator
	inspected  *ports.InspectedService
	inspectErr error
	sealErr    error

	sealed   map[string]string // swarmID -> hivemind service id
	released map[string]bool
}

func newAdoptStub(spec ports.ServiceSpec, warnings ...string) *adoptStub {
	return &adoptStub{
		StubOrchestrator: orchestrator.NewStubOrchestrator(),
		inspected:        &ports.InspectedService{Spec: spec, Warnings: warnings},
		sealed:           map[string]string{},
		released:         map[string]bool{},
	}
}

func (a *adoptStub) InspectService(context.Context, string) (*ports.InspectedService, error) {
	if a.inspectErr != nil {
		return nil, a.inspectErr
	}
	return a.inspected, nil
}

func (a *adoptStub) SetHivemindLabel(_ context.Context, swarmServiceID, hivemindServiceID string) error {
	if a.sealErr != nil {
		return a.sealErr
	}
	a.sealed[swarmServiceID] = hivemindServiceID
	return nil
}

func (a *adoptStub) ClearHivemindLabel(_ context.Context, swarmServiceID string) error {
	a.released[swarmServiceID] = true
	return nil
}

type fakeCapturer struct {
	called int
	err    error
}

func (f *fakeCapturer) Capture(context.Context, uuid.UUID, string, *uuid.UUID) (*snapshot.ServiceSnapshot, error) {
	f.called++
	return nil, f.err
}

func TestAdopt_CreatesRecordSealsAndSnapshots(t *testing.T) {
	repo := newFakeServiceRepo()
	cap := &fakeCapturer{}
	orch := newAdoptStub(ports.ServiceSpec{
		Name:     "legacy-api",
		Image:    "nginx:1.25",
		Replicas: 3,
		Env:      map[string]string{"FOO": "bar"},
		Ports:    []ports.PortSpec{{TargetPort: 80, PublishedPort: 8080, Protocol: "tcp", Mode: "ingress"}},
	}, "2 secret reference(s) not imported — re-attach after adoption")
	registry := orchestrator.NewStaticRegistry(orch)

	svc := application.NewDiscoveryService(registry, repo, cap)
	hiveID := uuid.New()
	res, err := svc.Adopt(context.Background(), application.AdoptInput{
		ClusterID:      uuid.Nil,
		SwarmServiceID: "swarm-xyz",
		HiveID:         &hiveID,
	})
	require.NoError(t, err)

	// Record created, deployed, attached to the hive, linked to the live service.
	rec, err := repo.FindByID(context.Background(), res.ServiceID)
	require.NoError(t, err)
	assert.Equal(t, "legacy-api", rec.Name)
	assert.Equal(t, "nginx", rec.Image)
	assert.Equal(t, "1.25", rec.Tag)
	assert.Equal(t, uint64(3), rec.Replicas)
	assert.Equal(t, service.StatusDeployed, rec.Status)
	assert.Equal(t, "swarm-xyz", rec.SwarmServiceID)
	require.NotNil(t, rec.HiveID)
	assert.Equal(t, hiveID, *rec.HiveID)

	// Env + ports imported.
	env, _ := repo.GetEnvVars(context.Background(), res.ServiceID)
	require.Len(t, env, 1)
	assert.Equal(t, "FOO", env[0].Key)
	ports_, _ := repo.GetPorts(context.Background(), res.ServiceID)
	require.Len(t, ports_, 1)
	assert.Equal(t, uint32(8080), ports_[0].PublishedPort)

	// Ownership sealed, snapshot taken, warnings surfaced.
	assert.Equal(t, res.ServiceID.String(), orch.sealed["swarm-xyz"])
	assert.Equal(t, 1, cap.called)
	assert.Contains(t, res.Warnings, "2 secret reference(s) not imported — re-attach after adoption")
}

func TestAdopt_AlreadyManaged(t *testing.T) {
	repo := newFakeServiceRepo()
	existing := &service.Service{ID: uuid.New(), Name: "api", SwarmServiceID: "swarm-xyz"}
	repo.add(existing)
	orch := newAdoptStub(ports.ServiceSpec{Name: "api", Image: "nginx"})
	svc := application.NewDiscoveryService(orchestrator.NewStaticRegistry(orch), repo, nil)

	_, err := svc.Adopt(context.Background(), application.AdoptInput{SwarmServiceID: "swarm-xyz"})
	assert.ErrorIs(t, err, application.ErrAlreadyManaged)
}

func TestAdopt_SealFailureRollsBackRecord(t *testing.T) {
	repo := newFakeServiceRepo()
	orch := newAdoptStub(ports.ServiceSpec{Name: "api", Image: "nginx"})
	orch.sealErr = errors.New("swarm update failed")
	svc := application.NewDiscoveryService(orchestrator.NewStaticRegistry(orch), repo, nil)

	_, err := svc.Adopt(context.Background(), application.AdoptInput{SwarmServiceID: "swarm-xyz"})
	require.Error(t, err)

	// No lingering half-adopted record.
	items, _, _ := repo.List(context.Background(), ports.ServiceFilter{}, pagination.New(1, 100))
	assert.Empty(t, items)
}

func TestRelease_ClearsLabelAndDeletesRecord(t *testing.T) {
	repo := newFakeServiceRepo()
	rec := &service.Service{ID: uuid.New(), Name: "api", SwarmServiceID: "swarm-xyz"}
	repo.add(rec)
	orch := newAdoptStub(ports.ServiceSpec{})
	svc := application.NewDiscoveryService(orchestrator.NewStaticRegistry(orch), repo, nil)

	err := svc.Release(context.Background(), uuid.Nil, "swarm-xyz")
	require.NoError(t, err)
	assert.True(t, orch.released["swarm-xyz"])
	_, err = repo.FindByID(context.Background(), rec.ID)
	assert.Error(t, err) // deleted
}

func TestRelease_UnknownSwarmService(t *testing.T) {
	repo := newFakeServiceRepo()
	orch := newAdoptStub(ports.ServiceSpec{})
	svc := application.NewDiscoveryService(orchestrator.NewStaticRegistry(orch), repo, nil)

	err := svc.Release(context.Background(), uuid.Nil, "missing")
	assert.ErrorIs(t, err, application.ErrServiceNotAdopted)
}
