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
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fake transaction manager ─────────────────────────────────────────────────

// fakeTx runs the unit of work directly, then reports whether it committed.
// commitErr simulates a commit that fails after the work went through — the one
// case where the cluster object outlives the transaction meant to own it.
type fakeTx struct {
	commitErr error
	calls     int
	rolled    bool
}

func (t *fakeTx) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	t.calls++
	if err := fn(ctx); err != nil {
		t.rolled = true
		return err
	}
	if t.commitErr != nil {
		t.rolled = true
		return t.commitErr
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func clusterWiring(orch *fakeOrchestrator) (ports.OrchestratorRegistry, *fakeTx) {
	return orchestrator.NewStaticRegistry(orch), &fakeTx{}
}

// ─── Networks ─────────────────────────────────────────────────────────────────

func TestNetworkCreate_MaterialisesOnCluster(t *testing.T) {
	repo, orch := newFakeNetworkRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), reg, tx)

	n, err := svc.Create(context.Background(), application.CreateNetworkInput{
		Name: "backend", Subnet: "10.0.9.0/24", Attachable: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"backend"}, orch.createdNetworks)
	assert.Equal(t, "swarm-net-backend", n.SwarmID)
	stored, err := repo.FindByID(context.Background(), n.ID)
	require.NoError(t, err)
	assert.Equal(t, "swarm-net-backend", stored.SwarmID, "swarm id must be persisted, not just returned")
	assert.Equal(t, 1, tx.calls)
	assert.False(t, tx.rolled)
}

func TestNetworkCreate_ClusterFailureRollsBackTheRow(t *testing.T) {
	repo := newFakeNetworkRepo()
	orch := &fakeOrchestrator{createErr: errors.New("swarm down")}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), reg, tx)

	_, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})

	require.Error(t, err)
	assert.ErrorIs(t, err, application.ErrClusterRejected)
	assert.True(t, tx.rolled, "the transaction must roll back so no orphan row survives")
	assert.Empty(t, orch.removed, "nothing was created on the cluster, nothing to clean up")
}

func TestNetworkCreate_CommitFailureRemovesTheOverlay(t *testing.T) {
	orch := &fakeOrchestrator{}
	reg := orchestrator.NewStaticRegistry(orch)
	tx := &fakeTx{commitErr: errors.New("commit failed")}
	svc := application.NewNetworkService(newFakeNetworkRepo(), newFakeServiceRepo(), reg, tx)

	_, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})

	require.Error(t, err)
	// The database forgot the network, so the overlay must go too.
	assert.Equal(t, []string{"swarm-net-backend"}, orch.removed)
}

func TestNetworkCreate_DuplicateNameNeverReachesTheCluster(t *testing.T) {
	repo, orch := newFakeNetworkRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), reg, tx)
	require.NoError(t, repo.Save(context.Background(), mkNetwork(t, "backend")))

	_, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})

	assert.ErrorIs(t, err, domainerrors.ErrConflict)
	assert.Empty(t, orch.createdNetworks, "the row is rejected before the cluster is touched")
}

func TestNetworkCreate_ExternalIsRecordedOnly(t *testing.T) {
	orch := &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(newFakeNetworkRepo(), newFakeServiceRepo(), reg, tx)

	n, err := svc.Create(context.Background(), application.CreateNetworkInput{
		Name: "ingress", External: true,
	})

	require.NoError(t, err)
	assert.Empty(t, orch.createdNetworks, "an external network already exists; Hivemind only references it")
	assert.Empty(t, n.SwarmID)
}

func TestNetworkCreate_WithoutOrchestratorStaysDatabaseOnly(t *testing.T) {
	repo := newFakeNetworkRepo()
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), nil, nil)

	n, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})

	require.NoError(t, err)
	_, err = repo.FindByID(context.Background(), n.ID)
	assert.NoError(t, err)
}

func TestNetworkCreate_UnreachableClusterFails(t *testing.T) {
	repo := newFakeNetworkRepo()
	svc := application.NewNetworkService(
		repo, newFakeServiceRepo(), orchestrator.NewStaticRegistry(nil), &fakeTx{})

	_, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})

	assert.ErrorIs(t, err, application.ErrOrchestratorUnavailable)
	items, _, _ := repo.List(context.Background(), uuid.Nil, pagination.New(1, 10))
	assert.Empty(t, items, "the network must not be recorded when its cluster is out of reach")
}

// ─── Secrets ──────────────────────────────────────────────────────────────────

func TestSecretCreate_MaterialisesVersionOnCluster(t *testing.T) {
	repo, orch := newFakeSecretRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewSecretService(repo, newFakeServiceRepo(), reg, tx)

	_, err := svc.Create(context.Background(), application.CreateSecretInput{
		Name: "db_password", Value: []byte("s3cr3t"),
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"db_password_v1"}, orch.createdSecrets)
	assert.False(t, tx.rolled)
}

func TestSecretCreate_ClusterFailureRollsBack(t *testing.T) {
	orch := &fakeOrchestrator{createErr: errors.New("swarm down")}
	reg, tx := clusterWiring(orch)
	svc := application.NewSecretService(newFakeSecretRepo(), newFakeServiceRepo(), reg, tx)

	_, err := svc.Create(context.Background(), application.CreateSecretInput{
		Name: "db_password", Value: []byte("s3cr3t"),
	})

	assert.ErrorIs(t, err, application.ErrClusterRejected)
	assert.True(t, tx.rolled)
}

func TestSecretRotate_PushesTheNewVersion(t *testing.T) {
	repo, orch := newFakeSecretRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewSecretService(repo, newFakeServiceRepo(), reg, tx)
	sec, err := svc.Create(context.Background(), application.CreateSecretInput{
		Name: "db_password", Value: []byte("old"),
	})
	require.NoError(t, err)

	rotated, err := svc.Rotate(context.Background(), sec.ID, []byte("new"))

	require.NoError(t, err)
	assert.Equal(t, 2, rotated.CurrentVersion)
	assert.Equal(t, []string{"db_password_v1", "db_password_v2"}, orch.createdSecrets,
		"each version is its own immutable Swarm object")
}

func TestSecretCreate_CommitFailureRemovesTheSwarmSecret(t *testing.T) {
	orch := &fakeOrchestrator{}
	tx := &fakeTx{commitErr: errors.New("commit failed")}
	svc := application.NewSecretService(
		newFakeSecretRepo(), newFakeServiceRepo(), orchestrator.NewStaticRegistry(orch), tx)

	_, err := svc.Create(context.Background(), application.CreateSecretInput{
		Name: "db_password", Value: []byte("s3cr3t"),
	})

	require.Error(t, err)
	assert.Equal(t, []string{"swarm-secret-db_password_v1"}, orch.removed)
}

// ─── Configs ──────────────────────────────────────────────────────────────────

func TestConfigCreate_MaterialisesVersionOnCluster(t *testing.T) {
	repo, orch := newFakeConfigRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewConfigService(repo, newFakeServiceRepo(), reg, tx)

	cfg, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("server {}"), Comment: "initial",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"nginx_v1"}, orch.createdConfigs)
	versions, err := repo.ListVersions(context.Background(), cfg.ID)
	require.NoError(t, err)
	assert.Equal(t, "swarm-config-nginx_v1", versions[0].SwarmConfigID)
}

func TestConfigCreate_ClusterFailureRollsBack(t *testing.T) {
	orch := &fakeOrchestrator{createErr: errors.New("swarm down")}
	reg, tx := clusterWiring(orch)
	svc := application.NewConfigService(newFakeConfigRepo(), newFakeServiceRepo(), reg, tx)

	_, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("server {}"), Comment: "initial",
	})

	assert.ErrorIs(t, err, application.ErrClusterRejected)
	assert.True(t, tx.rolled)
}

func TestConfigAddVersion_PushesTheNewVersion(t *testing.T) {
	repo, orch := newFakeConfigRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewConfigService(repo, newFakeServiceRepo(), reg, tx)
	cfg, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("v1"), Comment: "initial",
	})
	require.NoError(t, err)

	updated, err := svc.AddVersion(context.Background(), cfg.ID, []byte("v2"), "tuning", uuid.New())

	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)
	assert.Equal(t, []string{"nginx_v1", "nginx_v2"}, orch.createdConfigs)
}

func TestConfigRestoreVersion_PushesTheRestoredContent(t *testing.T) {
	repo, orch := newFakeConfigRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewConfigService(repo, newFakeServiceRepo(), reg, tx)
	cfg, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("v1"), Comment: "initial",
	})
	require.NoError(t, err)
	_, err = svc.AddVersion(context.Background(), cfg.ID, []byte("v2"), "tuning", uuid.New())
	require.NoError(t, err)

	restored, err := svc.RestoreVersion(context.Background(), cfg.ID, 1, "", uuid.New())

	require.NoError(t, err)
	assert.Equal(t, 3, restored.CurrentVersion)
	assert.Equal(t, []string{"nginx_v1", "nginx_v2", "nginx_v3"}, orch.createdConfigs)
}

func TestConfigCreate_RemovalFailureStillReportsTheCommitError(t *testing.T) {
	orch := &fakeOrchestrator{removeErr: errors.New("still in use")}
	commitErr := errors.New("commit failed")
	svc := application.NewConfigService(
		newFakeConfigRepo(), newFakeServiceRepo(),
		orchestrator.NewStaticRegistry(orch), &fakeTx{commitErr: commitErr})

	_, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("server {}"), Comment: "initial",
	})

	// A failed clean-up is logged, never swallowed over the real error.
	assert.ErrorIs(t, err, commitErr)
}

// ─── Deletion ─────────────────────────────────────────────────────────────────

func TestNetworkDelete_RemovesTheOverlay(t *testing.T) {
	repo, orch := newFakeNetworkRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), reg, tx)
	n, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), n.ID))

	assert.Equal(t, []string{"swarm-net-backend"}, orch.removed)
}

func TestNetworkDelete_ExternalLeavesTheClusterAlone(t *testing.T) {
	repo, orch := newFakeNetworkRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), reg, tx)
	n, err := svc.Create(context.Background(), application.CreateNetworkInput{
		Name: "ingress", External: true,
	})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), n.ID))

	assert.Empty(t, orch.removed, "Hivemind never created it, so it must not remove it")
}

func TestNetworkDelete_ClusterRefusalRollsBack(t *testing.T) {
	repo := newFakeNetworkRepo()
	orch := &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewNetworkService(repo, newFakeServiceRepo(), reg, tx)
	n, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend"})
	require.NoError(t, err)
	orch.removeErr = errors.New("network in use")

	err = svc.Delete(context.Background(), n.ID)

	assert.ErrorIs(t, err, application.ErrClusterRejected)
	assert.True(t, tx.rolled, "the row must come back when the cluster refuses")
}

func TestSecretDelete_RemovesEveryVersion(t *testing.T) {
	repo, orch := newFakeSecretRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewSecretService(repo, newFakeServiceRepo(), reg, tx)
	sec, err := svc.Create(context.Background(), application.CreateSecretInput{
		Name: "db_password", Value: []byte("old"),
	})
	require.NoError(t, err)
	_, err = svc.Rotate(context.Background(), sec.ID, []byte("new"))
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), sec.ID))

	assert.Equal(t,
		[]string{"swarm-secret-db_password_v1", "swarm-secret-db_password_v2"},
		orch.removed,
		"every version is a Swarm object of its own and must be cleaned up")
}

func TestConfigDelete_RemovesEveryVersion(t *testing.T) {
	repo, orch := newFakeConfigRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewConfigService(repo, newFakeServiceRepo(), reg, tx)
	cfg, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("v1"), Comment: "initial",
	})
	require.NoError(t, err)
	_, err = svc.AddVersion(context.Background(), cfg.ID, []byte("v2"), "tuning", uuid.New())
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), cfg.ID))

	assert.ElementsMatch(t,
		[]string{"swarm-config-nginx_v1", "swarm-config-nginx_v2"},
		orch.removed)
}

func TestConfigDelete_AttachedConfigNeverReachesTheCluster(t *testing.T) {
	repo, orch := newFakeConfigRepo(), &fakeOrchestrator{}
	reg, tx := clusterWiring(orch)
	svc := application.NewConfigService(repo, newFakeServiceRepo(), reg, tx)
	cfg, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx", Content: []byte("v1"), Comment: "initial",
	})
	require.NoError(t, err)
	repo.attached[cfg.ID] = true

	err = svc.Delete(context.Background(), cfg.ID)

	require.Error(t, err)
	assert.Empty(t, orch.removed)
}
