package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/adapters/orchestrator"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
)

// discoStub embeds the stub orchestrator and overrides ListServices so the test
// controls the live inventory; every other Orchestrator method comes for free.
type discoStub struct {
	*orchestrator.StubOrchestrator
	live []ports.SwarmServiceInfo
}

func (d *discoStub) ListServices(context.Context) ([]ports.SwarmServiceInfo, error) {
	return d.live, nil
}

func TestDiscover_ClassifiesManagedForeignOrphan(t *testing.T) {
	repo := newFakeServiceRepo()

	// A persisted, managed service. Its ID is the value carried in the
	// hivemind.service.id label of the corresponding live service.
	hiveID := uuid.New()
	managed := &service.Service{ID: uuid.New(), Name: "api", HiveID: &hiveID, SwarmServiceID: "swarm-managed"}
	repo.add(managed)

	live := []ports.SwarmServiceInfo{
		{SwarmServiceID: "swarm-managed", Name: "api", Image: "api:v1", Replicas: 2, HivemindLabel: managed.ID.String()},
		{SwarmServiceID: "swarm-foreign", Name: "legacy-nginx", Image: "nginx:1.25", Replicas: 1, HivemindLabel: ""},
		{SwarmServiceID: "swarm-orphan", Name: "ghost", Image: "redis:7", Replicas: 1, HivemindLabel: uuid.New().String()},
	}
	registry := orchestrator.NewStaticRegistry(&discoStub{
		StubOrchestrator: orchestrator.NewStubOrchestrator(),
		live:             live,
	})

	svc := application.NewDiscoveryService(registry, repo, nil)
	out, err := svc.Discover(context.Background(), uuid.Nil)
	require.NoError(t, err)
	require.Len(t, out, 3)

	assert.Equal(t, application.ClassManaged, out[0].Class)
	require.NotNil(t, out[0].ServiceID)
	assert.Equal(t, managed.ID, *out[0].ServiceID)
	require.NotNil(t, out[0].HiveID)
	assert.Equal(t, hiveID, *out[0].HiveID)

	assert.Equal(t, application.ClassForeign, out[1].Class)
	assert.Nil(t, out[1].ServiceID)

	assert.Equal(t, application.ClassOrphan, out[2].Class)
	assert.Nil(t, out[2].ServiceID)
}

func TestDiscover_NilRegistryUnavailable(t *testing.T) {
	svc := application.NewDiscoveryService(nil, newFakeServiceRepo(), nil)
	_, err := svc.Discover(context.Background(), uuid.Nil)
	assert.ErrorIs(t, err, application.ErrOrchestratorUnavailable)
}
