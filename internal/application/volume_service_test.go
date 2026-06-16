package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/domain/volume"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// ─── Fake volume repo ───────────────────────────────────────────────────────────

type fakeVolumeRepo struct {
	byID   map[uuid.UUID]*volume.Volume
	byName map[string]*volume.Volume
}

func newFakeVolumeRepo() *fakeVolumeRepo {
	return &fakeVolumeRepo{byID: map[uuid.UUID]*volume.Volume{}, byName: map[string]*volume.Volume{}}
}

func (r *fakeVolumeRepo) Save(_ context.Context, v *volume.Volume) error {
	if _, exists := r.byName[v.Name]; exists {
		return domainerrors.ErrConflict
	}
	r.byID[v.ID] = v
	r.byName[v.Name] = v
	return nil
}
func (r *fakeVolumeRepo) FindByID(_ context.Context, id uuid.UUID) (*volume.Volume, error) {
	if v, ok := r.byID[id]; ok {
		return v, nil
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeVolumeRepo) FindByName(_ context.Context, name string) (*volume.Volume, error) {
	if v, ok := r.byName[name]; ok {
		return v, nil
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeVolumeRepo) List(_ context.Context, _ pagination.Page) ([]*volume.Volume, int64, error) {
	out := make([]*volume.Volume, 0, len(r.byID))
	for _, v := range r.byID {
		out = append(out, v)
	}
	return out, int64(len(out)), nil
}
func (r *fakeVolumeRepo) Delete(_ context.Context, id uuid.UUID) error {
	v, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byID, id)
	delete(r.byName, v.Name)
	return nil
}

// ─── Tests ──────────────────────────────────────────────────────────────────────

func TestVolumeCreate_Success(t *testing.T) {
	svc := application.NewVolumeService(newFakeVolumeRepo(), newFakeServiceRepo())
	v, err := svc.Create(context.Background(), application.CreateVolumeInput{Name: "app-data"})
	require.NoError(t, err)
	assert.Equal(t, "local", v.Driver)
}

func TestVolumeDelete_InUse(t *testing.T) {
	volRepo := newFakeVolumeRepo()
	svcRepo := newFakeServiceRepo()
	vsvc := application.NewVolumeService(volRepo, svcRepo)

	v, err := vsvc.Create(context.Background(), application.CreateVolumeInput{Name: "app-data"})
	require.NoError(t, err)

	s := mkService(t, "api")
	svcRepo.add(s)
	_, err = vsvc.SetServiceMounts(context.Background(), s.ID, []volume.Mount{
		{Type: volume.MountVolume, Source: "app-data", Target: "/data"},
	})
	require.NoError(t, err)

	err = vsvc.Delete(context.Background(), v.ID)
	assert.ErrorIs(t, err, volume.ErrVolumeInUse)
}

func TestSetServiceMounts_UnknownVolume(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	vsvc := application.NewVolumeService(newFakeVolumeRepo(), svcRepo)
	s := mkService(t, "api")
	svcRepo.add(s)

	_, err := vsvc.SetServiceMounts(context.Background(), s.ID, []volume.Mount{
		{Type: volume.MountVolume, Source: "ghost", Target: "/data"},
	})
	assert.ErrorIs(t, err, volume.ErrUnknownVolume)
}

func TestSetServiceMounts_MultiReplicaWarning(t *testing.T) {
	volRepo := newFakeVolumeRepo()
	svcRepo := newFakeServiceRepo()
	vsvc := application.NewVolumeService(volRepo, svcRepo)
	_, _ = vsvc.Create(context.Background(), application.CreateVolumeInput{Name: "app-data"})

	s, err := service.New("api", "nginx", "latest", 3) // 3 replicas
	require.NoError(t, err)
	svcRepo.add(s)

	res, err := vsvc.SetServiceMounts(context.Background(), s.ID, []volume.Mount{
		{Type: volume.MountVolume, Source: "app-data", Target: "/data"},
	})
	require.NoError(t, err)
	assert.Len(t, res.Warnings, 1)
}

func TestSetServiceMounts_DuplicateTarget(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	vsvc := application.NewVolumeService(newFakeVolumeRepo(), svcRepo)
	s := mkService(t, "api")
	svcRepo.add(s)

	_, err := vsvc.SetServiceMounts(context.Background(), s.ID, []volume.Mount{
		{Type: volume.MountTmpfs, Target: "/cache"},
		{Type: volume.MountTmpfs, Target: "/cache"},
	})
	assert.ErrorIs(t, err, volume.ErrDuplicateMountTarget)
}
