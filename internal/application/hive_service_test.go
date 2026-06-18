package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/hive"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// ─── Fake hive repo ─────────────────────────────────────────────────────────────

type fakeHiveRepo struct {
	byID   map[uuid.UUID]*hive.Hive
	byName map[string]bool
}

func newFakeHiveRepo() *fakeHiveRepo {
	return &fakeHiveRepo{byID: map[uuid.UUID]*hive.Hive{}, byName: map[string]bool{}}
}

func (r *fakeHiveRepo) Save(_ context.Context, h *hive.Hive) error {
	if r.byName[h.Name] {
		return domainerrors.ErrConflict
	}
	r.byName[h.Name] = true
	r.byID[h.ID] = h
	return nil
}
func (r *fakeHiveRepo) FindByID(_ context.Context, id uuid.UUID) (*hive.Hive, error) {
	if h, ok := r.byID[id]; ok {
		return h, nil
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeHiveRepo) List(_ context.Context, _ uuid.UUID, _ pagination.Page) ([]*hive.Hive, int64, error) {
	out := make([]*hive.Hive, 0, len(r.byID))
	for _, h := range r.byID {
		out = append(out, h)
	}
	return out, int64(len(out)), nil
}
func (r *fakeHiveRepo) Update(_ context.Context, h *hive.Hive) error {
	r.byID[h.ID] = h
	return nil
}
func (r *fakeHiveRepo) Delete(_ context.Context, id uuid.UUID) error {
	h, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byName, h.Name)
	delete(r.byID, id)
	return nil
}

// ─── Tests ──────────────────────────────────────────────────────────────────────

func TestHiveCreate_Success(t *testing.T) {
	svc := application.NewHiveService(newFakeHiveRepo(), newFakeServiceRepo())
	h, err := svc.Create(context.Background(), uuid.Nil, application.SaveHiveInput{Name: "Paiement", Color: "#1e88e5"})
	require.NoError(t, err)
	assert.Equal(t, "Paiement", h.Name)
}

func TestHiveMoveService_AssignAndUnassign(t *testing.T) {
	hiveRepo := newFakeHiveRepo()
	svcRepo := newFakeServiceRepo()
	svc := application.NewHiveService(hiveRepo, svcRepo)

	h, _ := svc.Create(context.Background(), uuid.Nil, application.SaveHiveInput{Name: "Paiement"})
	s := mkService(t, "api")
	svcRepo.add(s)

	moved, err := svc.MoveService(context.Background(), s.ID, &h.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.HiveID)
	assert.Equal(t, h.ID, *moved.HiveID)

	// Unassign.
	moved, err = svc.MoveService(context.Background(), s.ID, nil)
	require.NoError(t, err)
	assert.Nil(t, moved.HiveID)
}

func TestHiveMoveService_UnknownHive(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	svc := application.NewHiveService(newFakeHiveRepo(), svcRepo)
	s := mkService(t, "api")
	svcRepo.add(s)

	ghost := uuid.New()
	_, err := svc.MoveService(context.Background(), s.ID, &ghost)
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestHiveDelete_NonEmpty(t *testing.T) {
	hiveRepo := newFakeHiveRepo()
	svcRepo := newFakeServiceRepo()
	svc := application.NewHiveService(hiveRepo, svcRepo)

	h, _ := svc.Create(context.Background(), uuid.Nil, application.SaveHiveInput{Name: "Paiement"})
	s := mkService(t, "api")
	svcRepo.add(s)
	_, err := svc.MoveService(context.Background(), s.ID, &h.ID)
	require.NoError(t, err)

	err = svc.Delete(context.Background(), h.ID)
	assert.ErrorIs(t, err, hive.ErrHiveNotEmpty)
}

func TestHiveList_WithCounts(t *testing.T) {
	hiveRepo := newFakeHiveRepo()
	svcRepo := newFakeServiceRepo()
	svc := application.NewHiveService(hiveRepo, svcRepo)

	h, _ := svc.Create(context.Background(), uuid.Nil, application.SaveHiveInput{Name: "Paiement"})
	for _, name := range []string{"api", "worker"} {
		s := mkService(t, name)
		svcRepo.add(s)
		_, err := svc.MoveService(context.Background(), s.ID, &h.ID)
		require.NoError(t, err)
	}

	summaries, total, err := svc.List(context.Background(), uuid.Nil, pagination.Page{Number: 1, Size: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, int64(2), summaries[0].ServiceCount)
}

func TestHiveListServices(t *testing.T) {
	hiveRepo := newFakeHiveRepo()
	svcRepo := newFakeServiceRepo()
	svc := application.NewHiveService(hiveRepo, svcRepo)

	h, _ := svc.Create(context.Background(), uuid.Nil, application.SaveHiveInput{Name: "Paiement"})
	in := mkService(t, "in-hive")
	out := mkService(t, "out-hive")
	svcRepo.add(in)
	svcRepo.add(out)
	_, err := svc.MoveService(context.Background(), in.ID, &h.ID)
	require.NoError(t, err)

	services, err := svc.ListServices(context.Background(), h.ID)
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, "in-hive", services[0].Name)
}
