package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/adapters/orchestrator"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/cluster"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fake cluster repo ────────────────────────────────────────────────────────

type fakeClusterRepo struct {
	byID map[uuid.UUID]*cluster.Cluster
}

func newFakeClusterRepo() *fakeClusterRepo {
	return &fakeClusterRepo{byID: map[uuid.UUID]*cluster.Cluster{}}
}

func (r *fakeClusterRepo) Save(_ context.Context, c *cluster.Cluster) error {
	r.byID[c.ID] = c
	return nil
}
func (r *fakeClusterRepo) FindByID(_ context.Context, id uuid.UUID) (*cluster.Cluster, error) {
	if c, ok := r.byID[id]; ok {
		return c, nil
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeClusterRepo) FindByName(_ context.Context, name string) (*cluster.Cluster, error) {
	for _, c := range r.byID {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeClusterRepo) FindByAgentID(_ context.Context, agentID string) (*cluster.Cluster, error) {
	for _, c := range r.byID {
		if agentID != "" && c.AgentID == agentID {
			return c, nil
		}
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeClusterRepo) FindByEnrollmentTokenHash(_ context.Context, h string) (*cluster.Cluster, error) {
	for _, c := range r.byID {
		if h != "" && c.EnrollmentTokenHash == h {
			return c, nil
		}
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeClusterRepo) FindDefault(_ context.Context) (*cluster.Cluster, error) {
	for _, c := range r.byID {
		if c.IsDefault {
			return c, nil
		}
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeClusterRepo) List(_ context.Context, _ pagination.Page) ([]*cluster.Cluster, int64, error) {
	out := make([]*cluster.Cluster, 0, len(r.byID))
	for _, c := range r.byID {
		out = append(out, c)
	}
	return out, int64(len(out)), nil
}
func (r *fakeClusterRepo) Update(_ context.Context, c *cluster.Cluster) error {
	r.byID[c.ID] = c
	return nil
}
func (r *fakeClusterRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.byID[id]; !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}
func (r *fakeClusterRepo) ClearDefault(_ context.Context) error {
	for _, c := range r.byID {
		c.IsDefault = false
	}
	return nil
}

func newClusterSvc(clusters *fakeClusterRepo, services *fakeServiceRepo) *application.ClusterService {
	reg := orchestrator.NewStaticRegistry(orchestrator.NewStubOrchestrator())
	return application.NewClusterService(reg, clusters, services,
		newFakeDeploymentRepo(), newFakeNetworkRepo(), newFakeSecretRepo(), newFakeConfigRepo())
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestCreateCluster_FirstBecomesDefault(t *testing.T) {
	clusters := newFakeClusterRepo()
	svc := newClusterSvc(clusters, newFakeServiceRepo())

	first, err := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "a"})
	require.NoError(t, err)
	assert.True(t, first.IsDefault, "first cluster must be promoted to default")

	second, err := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "b"})
	require.NoError(t, err)
	assert.False(t, second.IsDefault)
}

func TestSetDefaultCluster_DemotesPrevious(t *testing.T) {
	clusters := newFakeClusterRepo()
	svc := newClusterSvc(clusters, newFakeServiceRepo())

	a, _ := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "a"})
	b, _ := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "b"})

	_, err := svc.SetDefaultCluster(context.Background(), b.ID)
	require.NoError(t, err)

	reloadedA, _ := svc.GetCluster(context.Background(), a.ID)
	reloadedB, _ := svc.GetCluster(context.Background(), b.ID)
	assert.False(t, reloadedA.IsDefault)
	assert.True(t, reloadedB.IsDefault)
}

func TestDeleteCluster_RefusesDefault(t *testing.T) {
	clusters := newFakeClusterRepo()
	svc := newClusterSvc(clusters, newFakeServiceRepo())

	a, _ := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "a"})
	err := svc.DeleteCluster(context.Background(), a.ID)
	assert.ErrorIs(t, err, cluster.ErrDefaultCluster)
}

func TestDeleteCluster_RefusesNonEmpty(t *testing.T) {
	clusters := newFakeClusterRepo()
	svcRepo := newFakeServiceRepo()
	svc := newClusterSvc(clusters, svcRepo)

	_, _ = svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "a"}) // default
	b, _ := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "b"})

	s, _ := service.New("api", "nginx", "latest", 1, nil)
	s.ClusterID = b.ID
	svcRepo.add(s)

	err := svc.DeleteCluster(context.Background(), b.ID)
	assert.ErrorIs(t, err, cluster.ErrClusterNotEmpty)
}

func TestDeleteCluster_Success(t *testing.T) {
	clusters := newFakeClusterRepo()
	svc := newClusterSvc(clusters, newFakeServiceRepo())

	_, _ = svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "a"}) // default
	b, _ := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "b"})

	require.NoError(t, svc.DeleteCluster(context.Background(), b.ID))
	_, err := svc.GetCluster(context.Background(), b.ID)
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestTestCluster_Reachable(t *testing.T) {
	clusters := newFakeClusterRepo()
	svc := newClusterSvc(clusters, newFakeServiceRepo())

	a, _ := svc.CreateCluster(context.Background(), application.CreateClusterInput{Name: "a"})
	got, err := svc.TestCluster(context.Background(), a.ID)
	require.NoError(t, err)
	assert.Equal(t, cluster.StatusReachable, got.Status)
}
