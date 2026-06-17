package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fake network repo ────────────────────────────────────────────────────────

type fakeNetworkRepo struct {
	byID     map[uuid.UUID]*network.Network
	byName   map[string]*network.Network
	attached map[uuid.UUID]bool
}

func newFakeNetworkRepo() *fakeNetworkRepo {
	return &fakeNetworkRepo{
		byID:     map[uuid.UUID]*network.Network{},
		byName:   map[string]*network.Network{},
		attached: map[uuid.UUID]bool{},
	}
}

func (r *fakeNetworkRepo) Save(_ context.Context, n *network.Network) error {
	if _, ok := r.byName[n.Name]; ok {
		return domainerrors.ErrConflict
	}
	r.byID[n.ID] = n
	r.byName[n.Name] = n
	return nil
}

func (r *fakeNetworkRepo) FindByID(_ context.Context, id uuid.UUID) (*network.Network, error) {
	if n, ok := r.byID[id]; ok {
		return n, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeNetworkRepo) List(_ context.Context, _ uuid.UUID, _ pagination.Page) ([]*network.Network, int64, error) {
	out := make([]*network.Network, 0, len(r.byID))
	for _, n := range r.byID {
		out = append(out, n)
	}
	return out, int64(len(out)), nil
}

func (r *fakeNetworkRepo) Delete(_ context.Context, id uuid.UUID) error {
	n, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byID, id)
	delete(r.byName, n.Name)
	return nil
}

func (r *fakeNetworkRepo) IsAttachedToService(_ context.Context, id uuid.UUID) (bool, error) {
	return r.attached[id], nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func mkNetwork(t *testing.T, name string) *network.Network {
	t.Helper()
	n, err := network.New(name)
	require.NoError(t, err)
	return n
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestNetworkCreate_Success(t *testing.T) {
	svc := application.NewNetworkService(newFakeNetworkRepo(), newFakeServiceRepo())
	n, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "backend-net", Attachable: true})
	require.NoError(t, err)
	assert.Equal(t, "backend-net", n.Name)
	assert.Equal(t, "overlay", n.Driver)
}

func TestNetworkCreate_InvalidName(t *testing.T) {
	svc := application.NewNetworkService(newFakeNetworkRepo(), newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "-bad name"})
	assert.ErrorIs(t, err, network.ErrInvalidName)
}

func TestNetworkCreate_DuplicateName(t *testing.T) {
	repo := newFakeNetworkRepo()
	repo.byName["dup"] = mkNetwork(t, "dup")
	svc := application.NewNetworkService(repo, newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateNetworkInput{Name: "dup"})
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestNetworkDelete_InUse(t *testing.T) {
	repo := newFakeNetworkRepo()
	n := mkNetwork(t, "net")
	repo.byID[n.ID] = n
	repo.attached[n.ID] = true
	svc := application.NewNetworkService(repo, newFakeServiceRepo())

	err := svc.Delete(context.Background(), n.ID)
	assert.ErrorIs(t, err, network.ErrNetworkInUse)
}

func TestNetworkDelete_Success(t *testing.T) {
	repo := newFakeNetworkRepo()
	n := mkNetwork(t, "net")
	repo.byID[n.ID] = n
	svc := application.NewNetworkService(repo, newFakeServiceRepo())

	require.NoError(t, svc.Delete(context.Background(), n.ID))
	_, err := repo.FindByID(context.Background(), n.ID)
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

// ─── Attach / detach ──────────────────────────────────────────────────────────

func TestNetworkAttach_Success(t *testing.T) {
	netRepo := newFakeNetworkRepo()
	svcRepo := newFakeServiceRepo()
	n := mkNetwork(t, "net")
	netRepo.byID[n.ID] = n
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := application.NewNetworkService(netRepo, svcRepo)

	require.NoError(t, svc.AttachToService(context.Background(), s.ID, n.ID))

	nets, err := svc.ListServiceNetworks(context.Background(), s.ID)
	require.NoError(t, err)
	require.Len(t, nets, 1)
	assert.Equal(t, n.ID, nets[0].ID)
}

func TestNetworkAttach_AlreadyAttached(t *testing.T) {
	netRepo := newFakeNetworkRepo()
	svcRepo := newFakeServiceRepo()
	n := mkNetwork(t, "net")
	netRepo.byID[n.ID] = n
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := application.NewNetworkService(netRepo, svcRepo)

	require.NoError(t, svc.AttachToService(context.Background(), s.ID, n.ID))
	err := svc.AttachToService(context.Background(), s.ID, n.ID)
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

func TestNetworkAttach_UnknownNetwork(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := application.NewNetworkService(newFakeNetworkRepo(), svcRepo)

	err := svc.AttachToService(context.Background(), s.ID, uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestNetworkAttach_UnknownService(t *testing.T) {
	netRepo := newFakeNetworkRepo()
	n := mkNetwork(t, "net")
	netRepo.byID[n.ID] = n
	svc := application.NewNetworkService(netRepo, newFakeServiceRepo())

	err := svc.AttachToService(context.Background(), uuid.New(), n.ID)
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestNetworkDetach_NotAttached(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := application.NewNetworkService(newFakeNetworkRepo(), svcRepo)

	err := svc.DetachFromService(context.Background(), s.ID, uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}
