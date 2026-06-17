package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/adapters/orchestrator"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/domain/volume"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// ─── Fake repo ────────────────────────────────────────────────────────────────

type fakeServiceRepo struct {
	byID     map[uuid.UUID]*service.Service
	byName   map[string]*service.Service
	envVars  map[uuid.UUID][]service.EnvVar
	networks map[uuid.UUID][]uuid.UUID // serviceID -> networkIDs
	secrets  map[uuid.UUID][]ports.ServiceSecretAttachment
	configs  map[uuid.UUID][]ports.ServiceConfigAttachment
	mounts   map[uuid.UUID][]volume.Mount
}

func newFakeServiceRepo() *fakeServiceRepo {
	return &fakeServiceRepo{
		byID:     map[uuid.UUID]*service.Service{},
		byName:   map[string]*service.Service{},
		envVars:  map[uuid.UUID][]service.EnvVar{},
		networks: map[uuid.UUID][]uuid.UUID{},
		secrets:  map[uuid.UUID][]ports.ServiceSecretAttachment{},
		configs:  map[uuid.UUID][]ports.ServiceConfigAttachment{},
		mounts:   map[uuid.UUID][]volume.Mount{},
	}
}

func (r *fakeServiceRepo) add(s *service.Service) {
	r.byID[s.ID] = s
	r.byName[s.Name] = s
}

func (r *fakeServiceRepo) Save(_ context.Context, s *service.Service) error {
	r.add(s)
	return nil
}

func (r *fakeServiceRepo) FindByID(_ context.Context, id uuid.UUID) (*service.Service, error) {
	if s, ok := r.byID[id]; ok {
		return s, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeServiceRepo) FindByName(_ context.Context, name string) (*service.Service, error) {
	if s, ok := r.byName[name]; ok {
		return s, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeServiceRepo) List(_ context.Context, f ports.ServiceFilter, _ pagination.Page) ([]*service.Service, int64, error) {
	items := make([]*service.Service, 0, len(r.byID))
	for _, s := range r.byID {
		if f.Unassigned && s.HiveID != nil {
			continue
		}
		if f.HiveID != nil && (s.HiveID == nil || *s.HiveID != *f.HiveID) {
			continue
		}
		items = append(items, s)
	}
	return items, int64(len(items)), nil
}

func (r *fakeServiceRepo) Update(_ context.Context, s *service.Service) error {
	if _, ok := r.byID[s.ID]; !ok {
		return domainerrors.ErrNotFound
	}
	r.add(s)
	return nil
}

func (r *fakeServiceRepo) Delete(_ context.Context, id uuid.UUID) error {
	s, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byID, id)
	delete(r.byName, s.Name)
	return nil
}

func (r *fakeServiceRepo) SetEnvVars(_ context.Context, id uuid.UUID, vars []service.EnvVar) error {
	r.envVars[id] = vars
	return nil
}
func (r *fakeServiceRepo) GetEnvVars(_ context.Context, id uuid.UUID) ([]service.EnvVar, error) {
	return r.envVars[id], nil
}

// Attachment stubs — not exercised by service CRUD tests.
func (r *fakeServiceRepo) AttachNetwork(_ context.Context, serviceID, networkID uuid.UUID) error {
	for _, id := range r.networks[serviceID] {
		if id == networkID {
			return domainerrors.ErrConflict
		}
	}
	r.networks[serviceID] = append(r.networks[serviceID], networkID)
	return nil
}
func (r *fakeServiceRepo) DetachNetwork(_ context.Context, serviceID, networkID uuid.UUID) error {
	ids := r.networks[serviceID]
	for i, id := range ids {
		if id == networkID {
			r.networks[serviceID] = append(ids[:i], ids[i+1:]...)
			return nil
		}
	}
	return domainerrors.ErrNotFound
}
func (r *fakeServiceRepo) GetNetworkIDs(_ context.Context, serviceID uuid.UUID) ([]uuid.UUID, error) {
	return r.networks[serviceID], nil
}
func (r *fakeServiceRepo) AttachSecret(_ context.Context, serviceID, secretID uuid.UUID, targetPath string) error {
	for _, a := range r.secrets[serviceID] {
		if a.SecretID == secretID {
			return domainerrors.ErrConflict
		}
	}
	r.secrets[serviceID] = append(r.secrets[serviceID], ports.ServiceSecretAttachment{SecretID: secretID, TargetPath: targetPath})
	return nil
}
func (r *fakeServiceRepo) DetachSecret(_ context.Context, serviceID, secretID uuid.UUID) error {
	as := r.secrets[serviceID]
	for i, a := range as {
		if a.SecretID == secretID {
			r.secrets[serviceID] = append(as[:i], as[i+1:]...)
			return nil
		}
	}
	return domainerrors.ErrNotFound
}
func (r *fakeServiceRepo) GetSecretAttachments(_ context.Context, serviceID uuid.UUID) ([]ports.ServiceSecretAttachment, error) {
	return r.secrets[serviceID], nil
}
func (r *fakeServiceRepo) AttachConfig(_ context.Context, serviceID, configID uuid.UUID, targetPath string) error {
	for _, a := range r.configs[serviceID] {
		if a.ConfigID == configID {
			return domainerrors.ErrConflict
		}
	}
	r.configs[serviceID] = append(r.configs[serviceID], ports.ServiceConfigAttachment{ConfigID: configID, TargetPath: targetPath})
	return nil
}
func (r *fakeServiceRepo) DetachConfig(_ context.Context, serviceID, configID uuid.UUID) error {
	as := r.configs[serviceID]
	for i, a := range as {
		if a.ConfigID == configID {
			r.configs[serviceID] = append(as[:i], as[i+1:]...)
			return nil
		}
	}
	return domainerrors.ErrNotFound
}
func (r *fakeServiceRepo) GetConfigAttachments(_ context.Context, serviceID uuid.UUID) ([]ports.ServiceConfigAttachment, error) {
	return r.configs[serviceID], nil
}
func (r *fakeServiceRepo) ServiceIDsByConfigID(_ context.Context, configID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for sid, atts := range r.configs {
		for _, a := range atts {
			if a.ConfigID == configID {
				ids = append(ids, sid)
			}
		}
	}
	return ids, nil
}
func (r *fakeServiceRepo) SetMounts(_ context.Context, serviceID uuid.UUID, mounts []volume.Mount) error {
	r.mounts[serviceID] = mounts
	return nil
}
func (r *fakeServiceRepo) GetMounts(_ context.Context, serviceID uuid.UUID) ([]volume.Mount, error) {
	return r.mounts[serviceID], nil
}
func (r *fakeServiceRepo) CountMountsByVolumeName(_ context.Context, name string) (int64, error) {
	var n int64
	for _, ms := range r.mounts {
		for _, m := range ms {
			if m.Type == volume.MountVolume && m.Source == name {
				n++
			}
		}
	}
	return n, nil
}
func (r *fakeServiceRepo) CountServicesByHive(_ context.Context, hiveID uuid.UUID) (int64, error) {
	var n int64
	for _, s := range r.byID {
		if s.HiveID != nil && *s.HiveID == hiveID {
			n++
		}
	}
	return n, nil
}
func (r *fakeServiceRepo) CountServicesByCluster(_ context.Context, clusterID uuid.UUID) (int64, error) {
	var n int64
	for _, s := range r.byID {
		if s.ClusterID == clusterID {
			n++
		}
	}
	return n, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func mkService(t *testing.T, name string) *service.Service {
	t.Helper()
	s, err := service.New(name, "nginx", "latest", 1, nil)
	require.NoError(t, err)
	return s
}

func newServiceSvc(repo *fakeServiceRepo) *application.ServiceService {
	return application.NewServiceService(repo, nil)
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestServiceCreate_Success(t *testing.T) {
	repo := newFakeServiceRepo()
	svc := newServiceSvc(repo)

	s, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name:  "my-service",
		Image: "nginx",
		Tag:   "1.25",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-service", s.Name)
	assert.Equal(t, service.StatusDraft, s.Status)
	assert.NotEqual(t, uuid.Nil, s.ID)
}

func TestServiceCreate_InvalidName(t *testing.T) {
	repo := newFakeServiceRepo()
	svc := newServiceSvc(repo)

	_, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name:  "UPPER_CASE",
		Image: "nginx",
	})
	assert.ErrorIs(t, err, service.ErrInvalidName)
}

func TestServiceCreate_DuplicateName(t *testing.T) {
	repo := newFakeServiceRepo()
	repo.add(mkService(t, "existing"))
	svc := newServiceSvc(repo)

	_, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name:  "existing",
		Image: "nginx",
	})
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

func TestServiceCreate_PartialUpdateConfigKeepsDefaults(t *testing.T) {
	repo := newFakeServiceRepo()
	svc := newServiceSvc(repo)

	partial := service.UpdateConfig{Parallelism: 3}
	s, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name:         "my-service",
		Image:        "nginx",
		UpdateConfig: &partial,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(3), s.UpdateConfig.Parallelism)    // overridden
	assert.Equal(t, "rollback", s.UpdateConfig.FailureAction) // default preserved
	assert.Equal(t, "start-first", s.UpdateConfig.Order)      // default preserved
}

func TestServiceCreate_EmptyImage(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateServiceInput{Name: "my-service", Image: "  "})
	assert.ErrorIs(t, err, service.ErrInvalidImage)
}

func TestServiceCreate_InvalidFailureAction(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	bad := service.UpdateConfig{FailureAction: "explode"}
	_, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name: "my-service", Image: "nginx", UpdateConfig: &bad,
	})
	assert.ErrorIs(t, err, service.ErrInvalidFailureAction)
}

func TestServiceCreate_NegativeResource(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name: "my-service", Image: "nginx",
		Resources: service.Resources{CPUReservation: -1},
	})
	assert.ErrorIs(t, err, service.ErrNegativeResource)
}

func TestServiceUpdate_EmptyImageRejected(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)
	empty := ""
	_, err := svc.Update(context.Background(), s.ID, application.UpdateServiceInput{Image: &empty})
	assert.ErrorIs(t, err, service.ErrInvalidImage)
}

func TestServiceCreate_ResourceConflict(t *testing.T) {
	repo := newFakeServiceRepo()
	svc := newServiceSvc(repo)

	_, err := svc.Create(context.Background(), application.CreateServiceInput{
		Name:  "my-service",
		Image: "nginx",
		Resources: service.Resources{
			CPUReservation: 1.0,
			CPULimit:       0.5, // limit < reservation
		},
	})
	assert.ErrorIs(t, err, service.ErrResourceConflict)
}

// ─── Get ──────────────────────────────────────────────────────────────────────

func TestServiceGet_NotFound(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())

	_, err := svc.Get(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestServiceGet_Found(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	got, err := svc.Get(context.Background(), s.ID)
	require.NoError(t, err)
	assert.Equal(t, s.ID, got.ID)
}

// ─── List ─────────────────────────────────────────────────────────────────────

func TestServiceList_ReturnsAll(t *testing.T) {
	repo := newFakeServiceRepo()
	repo.add(mkService(t, "svc-a"))
	repo.add(mkService(t, "svc-b"))
	svc := newServiceSvc(repo)

	items, total, err := svc.List(context.Background(), ports.ServiceFilter{}, pagination.New(1, 20))
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, items, 2)
}

// ─── Update ───────────────────────────────────────────────────────────────────

func TestServiceUpdate_PartialUpdate(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	newTag := "2.0"
	newReplicas := uint64(3)
	updated, err := svc.Update(context.Background(), s.ID, application.UpdateServiceInput{
		Tag:      &newTag,
		Replicas: &newReplicas,
	})
	require.NoError(t, err)
	assert.Equal(t, "2.0", updated.Tag)
	assert.Equal(t, uint64(3), updated.Replicas)
	assert.Equal(t, "nginx", updated.Image) // unchanged
}

func TestServiceUpdate_NotFound(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	tag := "v2"
	_, err := svc.Update(context.Background(), uuid.New(), application.UpdateServiceInput{Tag: &tag})
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestServiceUpdate_NoOpWhenEmpty(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	updated, err := svc.Update(context.Background(), s.ID, application.UpdateServiceInput{})
	require.NoError(t, err)
	assert.Equal(t, s.Tag, updated.Tag) // unchanged
}

// ─── SetResources (F-MVP-03) ────────────────────────────────────────────────────

func TestServiceSetResources_Success(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	updated, err := svc.SetResources(context.Background(), s.ID, service.Resources{
		CPUReservation: 0.25,
		CPULimit:       0.5,
		MemReservation: 64 << 20,
		MemLimit:       128 << 20,
	})
	require.NoError(t, err)
	assert.Equal(t, 0.5, updated.Resources.CPULimit)
	assert.EqualValues(t, 128<<20, updated.Resources.MemLimit)
}

func TestServiceSetResources_LimitBelowReservation(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	_, err := svc.SetResources(context.Background(), s.ID, service.Resources{
		CPUReservation: 1.0,
		CPULimit:       0.5,
	})
	assert.ErrorIs(t, err, service.ErrResourceConflict)
}

func TestServiceSetResources_ExceedsClusterCapacity(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	// Stub cluster's largest node has 8 cores / 16 GiB.
	svc := application.NewServiceService(repo, orchestrator.NewStaticRegistry(orchestrator.NewStubOrchestrator()))

	_, err := svc.SetResources(context.Background(), s.ID, service.Resources{
		CPUReservation: 10000, // no node can satisfy this
	})
	assert.ErrorIs(t, err, application.ErrResourceExceedsCluster)

	_, err = svc.SetResources(context.Background(), s.ID, service.Resources{
		MemReservation: 300000 << 20, // ~293 GiB — exceeds every node
	})
	assert.ErrorIs(t, err, application.ErrResourceExceedsCluster)
}

func TestServiceSetResources_WithinClusterCapacity(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := application.NewServiceService(repo, orchestrator.NewStaticRegistry(orchestrator.NewStubOrchestrator()))

	_, err := svc.SetResources(context.Background(), s.ID, service.Resources{
		CPUReservation: 4,
		CPULimit:       8, // fits the largest node exactly
		MemReservation: 8 << 30,
		MemLimit:       16 << 30,
	})
	require.NoError(t, err)
}

func TestServiceSetResources_NoOrchestratorSkipsCapacityCheck(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo) // orchestrator = nil → capacity unknown, not enforced

	_, err := svc.SetResources(context.Background(), s.ID, service.Resources{CPUReservation: 10000})
	require.NoError(t, err)
}

func TestServiceSetResources_NotFound(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	_, err := svc.SetResources(context.Background(), uuid.New(), service.Resources{})
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

// ─── Env vars (F-MVP-04) ─────────────────────────────────────────────────────────

func TestServiceSetEnvVars_Success(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	vars, err := svc.SetEnvVars(context.Background(), s.ID, []application.EnvVarInput{
		{Key: "DATABASE_URL", Value: "postgres://...", IsSecret: true},
		{Key: "LOG_LEVEL", Value: "info"},
	})
	require.NoError(t, err)
	assert.Len(t, vars, 2)

	stored, err := svc.GetEnvVars(context.Background(), s.ID)
	require.NoError(t, err)
	assert.Len(t, stored, 2)
}

func TestServiceSetEnvVars_InvalidKey(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	_, err := svc.SetEnvVars(context.Background(), s.ID, []application.EnvVarInput{
		{Key: "lower-case", Value: "x"},
	})
	assert.ErrorIs(t, err, service.ErrInvalidEnvKey)
}

func TestServiceSetEnvVars_DuplicateKey(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	repo.add(s)
	svc := newServiceSvc(repo)

	_, err := svc.SetEnvVars(context.Background(), s.ID, []application.EnvVarInput{
		{Key: "FOO", Value: "1"},
		{Key: "FOO", Value: "2"},
	})
	assert.ErrorIs(t, err, service.ErrDuplicateKey)
}

func TestServiceSetEnvVars_ServiceNotFound(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	_, err := svc.SetEnvVars(context.Background(), uuid.New(), []application.EnvVarInput{{Key: "FOO", Value: "1"}})
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestServiceGetEnvVars_ServiceNotFound(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	_, err := svc.GetEnvVars(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestServiceDelete_DraftOK(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service") // status = draft
	repo.add(s)
	svc := newServiceSvc(repo)

	err := svc.Delete(context.Background(), s.ID)
	require.NoError(t, err)
	_, err = repo.FindByID(context.Background(), s.ID)
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestServiceDelete_DeployedWithoutOrchestrator(t *testing.T) {
	repo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	s.Status = service.StatusDeployed
	s.SwarmServiceID = "swarm-abc"
	repo.add(s)
	svc := newServiceSvc(repo) // orchestrator = nil

	err := svc.Delete(context.Background(), s.ID)
	assert.ErrorIs(t, err, application.ErrServiceDeployed)
}

func TestServiceDelete_NotFound(t *testing.T) {
	svc := newServiceSvc(newFakeServiceRepo())
	err := svc.Delete(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}
