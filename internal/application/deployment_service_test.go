package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/deployment"
	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// ─── Fake deployment repo ─────────────────────────────────────────────────────

type fakeDeploymentRepo struct {
	byID   map[uuid.UUID]*deployment.Deployment
	active map[uuid.UUID]*deployment.Deployment // serviceID -> in-flight
}

func newFakeDeploymentRepo() *fakeDeploymentRepo {
	return &fakeDeploymentRepo{
		byID:   map[uuid.UUID]*deployment.Deployment{},
		active: map[uuid.UUID]*deployment.Deployment{},
	}
}

func (r *fakeDeploymentRepo) Save(_ context.Context, d *deployment.Deployment) error {
	r.byID[d.ID] = d
	if !d.IsTerminal() {
		r.active[d.ServiceID] = d
	}
	return nil
}

func (r *fakeDeploymentRepo) FindByID(_ context.Context, id uuid.UUID) (*deployment.Deployment, error) {
	if d, ok := r.byID[id]; ok {
		return d, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeDeploymentRepo) FindActiveByServiceID(_ context.Context, serviceID uuid.UUID) (*deployment.Deployment, error) {
	if d, ok := r.active[serviceID]; ok {
		return d, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeDeploymentRepo) ListByServiceID(_ context.Context, serviceID uuid.UUID, _ pagination.Page) ([]*deployment.Deployment, int64, error) {
	out := make([]*deployment.Deployment, 0)
	for _, d := range r.byID {
		if d.ServiceID == serviceID {
			out = append(out, d)
		}
	}
	return out, int64(len(out)), nil
}

func (r *fakeDeploymentRepo) List(_ context.Context, _ ports.DeploymentFilter, _ pagination.Page) ([]*deployment.Deployment, int64, error) {
	out := make([]*deployment.Deployment, 0, len(r.byID))
	for _, d := range r.byID {
		out = append(out, d)
	}
	return out, int64(len(out)), nil
}

func (r *fakeDeploymentRepo) Update(_ context.Context, d *deployment.Deployment) error {
	r.byID[d.ID] = d
	if d.IsTerminal() {
		delete(r.active, d.ServiceID)
	}
	return nil
}

// ─── Fake orchestrator ────────────────────────────────────────────────────────

type fakeOrchestrator struct {
	deployErr      error
	convergeErr    error
	deployCalls    int
	updateCalls    int
	lastSpec       ports.ServiceSpec
	createdSecrets []string
}

func (o *fakeOrchestrator) DeployService(_ context.Context, spec ports.ServiceSpec) (string, error) {
	o.deployCalls++
	o.lastSpec = spec
	if o.deployErr != nil {
		return "", o.deployErr
	}
	return "swarm-svc-1", nil
}
func (o *fakeOrchestrator) UpdateService(_ context.Context, _ string, spec ports.ServiceSpec) error {
	o.updateCalls++
	o.lastSpec = spec
	return nil
}
func (o *fakeOrchestrator) RemoveService(context.Context, string) error { return nil }
func (o *fakeOrchestrator) GetServiceState(context.Context, string) (*ports.ServiceState, error) {
	return &ports.ServiceState{Running: 1, Desired: 1}, nil
}
func (o *fakeOrchestrator) WaitConvergence(context.Context, string, time.Duration) error {
	return o.convergeErr
}
func (o *fakeOrchestrator) CreateSecret(_ context.Context, name string, _ []byte) (string, error) {
	o.createdSecrets = append(o.createdSecrets, name)
	return "swarm-secret-" + name, nil
}
func (o *fakeOrchestrator) RemoveSecret(context.Context, string) error { return nil }
func (o *fakeOrchestrator) CreateConfig(_ context.Context, name string, _ []byte) (string, error) {
	return "swarm-config-" + name, nil
}
func (o *fakeOrchestrator) RemoveConfig(context.Context, string) error { return nil }
func (o *fakeOrchestrator) CreateNetwork(_ context.Context, name string, _ bool) (string, error) {
	return "swarm-net-" + name, nil
}
func (o *fakeOrchestrator) RemoveNetwork(context.Context, string) error { return nil }

// ─── Fake notifier ────────────────────────────────────────────────────────────

type fakeNotifier struct{ events []ports.NotificationEvent }

func (n *fakeNotifier) Notify(_ context.Context, e ports.NotificationEvent) error {
	n.events = append(n.events, e)
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newDeploymentSvc(t *testing.T) (*application.DeploymentService, *fakeServiceRepo, *fakeDeploymentRepo, *fakeOrchestrator, *fakeNotifier) {
	t.Helper()
	svcRepo := newFakeServiceRepo()
	depRepo := newFakeDeploymentRepo()
	netRepo := newFakeNetworkRepo()
	secRepo := newFakeSecretRepo()
	cfgRepo := newFakeConfigRepo()
	orch := &fakeOrchestrator{}
	notif := &fakeNotifier{}
	svc := application.NewDeploymentService(svcRepo, depRepo, netRepo, secRepo, cfgRepo, orch, notif)
	return svc, svcRepo, depRepo, orch, notif
}

// ─── Begin ────────────────────────────────────────────────────────────────────

func TestDeploymentBegin_Success(t *testing.T) {
	svc, svcRepo, _, _, _ := newDeploymentSvc(t)
	s := mkService(t, "api")
	svcRepo.add(s)

	dep, err := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	require.NoError(t, err)
	assert.Equal(t, deployment.StatusPending, dep.Status)
	assert.Equal(t, deployment.TriggerManual, dep.Trigger)
	assert.NotEmpty(t, dep.ConfigSnapshot)
}

func TestDeploymentBegin_ServiceNotFound(t *testing.T) {
	svc, _, _, _, _ := newDeploymentSvc(t)
	_, err := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: uuid.New()})
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestDeploymentBegin_AlreadyInProgress(t *testing.T) {
	svc, svcRepo, _, _, _ := newDeploymentSvc(t)
	s := mkService(t, "api")
	svcRepo.add(s)

	_, err := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	require.NoError(t, err)
	_, err = svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	assert.ErrorIs(t, err, deployment.ErrAlreadyInProgress)
}

func TestDeploymentBegin_OrchestratorUnavailable(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "api")
	svcRepo.add(s)
	svc := application.NewDeploymentService(svcRepo, newFakeDeploymentRepo(), newFakeNetworkRepo(), newFakeSecretRepo(), newFakeConfigRepo(), nil, nil)

	_, err := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	assert.ErrorIs(t, err, application.ErrOrchestratorUnavailable)
}

// ─── Execute ──────────────────────────────────────────────────────────────────

func TestDeploymentExecute_Success(t *testing.T) {
	svc, svcRepo, depRepo, orch, notif := newDeploymentSvc(t)
	s := mkService(t, "api")
	s.Replicas = 3
	svcRepo.add(s)
	dep, err := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	require.NoError(t, err)

	require.NoError(t, svc.Execute(context.Background(), dep.ID))

	got, _ := depRepo.FindByID(context.Background(), dep.ID)
	assert.Equal(t, deployment.StatusSucceeded, got.Status)
	assert.NotNil(t, got.FinishedAt)

	// Service is marked deployed and gets its Swarm id.
	updated, _ := svcRepo.FindByID(context.Background(), s.ID)
	assert.Equal(t, service.StatusDeployed, updated.Status)
	assert.Equal(t, "swarm-svc-1", updated.SwarmServiceID)

	assert.Equal(t, 1, orch.deployCalls)
	assert.Equal(t, uint64(3), orch.lastSpec.Replicas)
	require.Len(t, notif.events, 1)
	assert.Equal(t, deployment.StatusSucceeded, notif.events[0].Status)
}

func TestDeploymentExecute_SecondDeployUpdatesInPlace(t *testing.T) {
	svc, svcRepo, _, orch, _ := newDeploymentSvc(t)
	s := mkService(t, "api")
	svcRepo.add(s)

	d1, _ := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	require.NoError(t, svc.Execute(context.Background(), d1.ID))

	d2, _ := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	require.NoError(t, svc.Execute(context.Background(), d2.ID))

	assert.Equal(t, 1, orch.deployCalls)
	assert.Equal(t, 1, orch.updateCalls)
}

func TestDeploymentExecute_BuildsEnvAndSecrets(t *testing.T) {
	svc, svcRepo, _, orch, _ := newDeploymentSvc(t)
	s := mkService(t, "api")
	svcRepo.add(s)
	// Attach an env var and a secret via the fake service repo's stores.
	_ = svcRepo.SetEnvVars(context.Background(), s.ID, mustEnvVars(t, s.ID))

	dep, _ := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})
	require.NoError(t, svc.Execute(context.Background(), dep.ID))

	assert.Equal(t, "info", orch.lastSpec.Env["LOG_LEVEL"])
}

func TestDeploymentExecute_ConvergenceFailure(t *testing.T) {
	svc, svcRepo, depRepo, orch, notif := newDeploymentSvc(t)
	orch.convergeErr = errors.New("timeout waiting for tasks")
	s := mkService(t, "api")
	svcRepo.add(s)
	dep, _ := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})

	err := svc.Execute(context.Background(), dep.ID)
	require.Error(t, err)

	got, _ := depRepo.FindByID(context.Background(), dep.ID)
	assert.Equal(t, deployment.StatusFailed, got.Status)
	assert.Contains(t, got.ErrorMessage, "timeout waiting for tasks")
	require.Len(t, notif.events, 1)
	assert.Equal(t, deployment.StatusFailed, notif.events[0].Status)
}

func TestDeploymentExecute_DeployErrorMarksFailed(t *testing.T) {
	svc, svcRepo, depRepo, orch, _ := newDeploymentSvc(t)
	orch.deployErr = errors.New("image pull failed")
	s := mkService(t, "api")
	svcRepo.add(s)
	dep, _ := svc.Begin(context.Background(), application.BeginDeploymentInput{ServiceID: s.ID})

	err := svc.Execute(context.Background(), dep.ID)
	require.Error(t, err)

	got, _ := depRepo.FindByID(context.Background(), dep.ID)
	assert.Equal(t, deployment.StatusFailed, got.Status)
}

func mustEnvVars(t *testing.T, _ uuid.UUID) []service.EnvVar {
	t.Helper()
	return []service.EnvVar{{Key: "LOG_LEVEL", Value: "info"}}
}
