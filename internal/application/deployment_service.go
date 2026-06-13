package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/deployment"
	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

var (
	ErrOrchestratorUnavailable = errors.New("deployment engine is not configured")
	ErrServiceNotDeployed      = errors.New("service is not deployed")
)

const defaultConvergenceTimeout = 2 * time.Minute

// DeploymentService is the deployment engine (F-MVP-08). It assembles a service
// spec from the stored definition, provisions the required Swarm objects through
// the Orchestrator port, applies the deployment, waits for convergence and
// records the outcome. The Orchestrator and Notifier are kept behind ports so
// the engine itself is unit-testable without Docker.
type DeploymentService struct {
	services     ports.ServiceRepository
	deployments  ports.DeploymentRepository
	networks     ports.NetworkRepository
	secrets      ports.SecretRepository
	configs      ports.ConfigRepository
	orchestrator ports.Orchestrator
	notifier     ports.Notifier // optional
	timeout      time.Duration
	stateCache   *stateCache // short-lived cache of live orchestrator state (F-MVP-10)
}

func NewDeploymentService(
	services ports.ServiceRepository,
	deployments ports.DeploymentRepository,
	networks ports.NetworkRepository,
	secrets ports.SecretRepository,
	configs ports.ConfigRepository,
	orchestrator ports.Orchestrator,
	notifier ports.Notifier,
) *DeploymentService {
	return &DeploymentService{
		services:     services,
		deployments:  deployments,
		networks:     networks,
		secrets:      secrets,
		configs:      configs,
		orchestrator: orchestrator,
		notifier:     notifier,
		timeout:      defaultConvergenceTimeout,
		stateCache:   newStateCache(serviceStateTTL),
	}
}

type BeginDeploymentInput struct {
	ServiceID uuid.UUID
	UserID    *uuid.UUID // nil for webhook-triggered deployments
	Trigger   deployment.Trigger
}

// Begin validates and records a new pending deployment. It rejects a service
// that already has a deployment in flight. It does NOT run the engine — call
// Execute (or DeployAsync) to apply it.
func (s *DeploymentService) Begin(ctx context.Context, in BeginDeploymentInput) (*deployment.Deployment, error) {
	if s.orchestrator == nil {
		return nil, ErrOrchestratorUnavailable
	}

	svc, err := s.services.FindByID(ctx, in.ServiceID)
	if err != nil {
		return nil, err
	}

	if _, err := s.deployments.FindActiveByServiceID(ctx, in.ServiceID); err == nil {
		return nil, deployment.ErrAlreadyInProgress
	} else if !errors.Is(err, domainerrors.ErrNotFound) {
		return nil, err
	}

	trigger := in.Trigger
	if trigger == "" {
		trigger = deployment.TriggerManual
	}

	dep := deployment.New(in.ServiceID, in.UserID, svc.FullImage(), trigger, buildSnapshot(svc))
	if err := s.deployments.Save(ctx, dep); err != nil {
		return nil, err
	}
	return dep, nil
}

// DeployAsync records a pending deployment and runs the engine in the
// background, returning immediately so the caller can respond 202 and let the
// client poll the deployment status.
func (s *DeploymentService) DeployAsync(ctx context.Context, in BeginDeploymentInput) (*deployment.Deployment, error) {
	dep, err := s.Begin(ctx, in)
	if err != nil {
		return nil, err
	}
	go func() {
		if err := s.Execute(context.Background(), dep.ID); err != nil {
			slog.Error("deployment failed", "deployment_id", dep.ID, "service_id", dep.ServiceID, "err", err)
		}
	}()
	return dep, nil
}

// Execute applies a previously recorded pending deployment: it builds the spec,
// provisions Swarm objects, deploys or updates the service, waits for
// convergence and records success/failure. It is synchronous and returns the
// terminal error (if any) so it can be unit-tested directly.
func (s *DeploymentService) Execute(ctx context.Context, deploymentID uuid.UUID) error {
	dep, err := s.deployments.FindByID(ctx, deploymentID)
	if err != nil {
		return err
	}
	svc, err := s.services.FindByID(ctx, dep.ServiceID)
	if err != nil {
		return s.fail(ctx, dep, nil, err)
	}

	dep.Start()
	if err := s.deployments.Update(ctx, dep); err != nil {
		return err
	}

	if err := s.reconcile(ctx, svc); err != nil {
		return s.fail(ctx, dep, svc, err)
	}

	svc.Status = service.StatusDeployed
	if err := s.services.Update(ctx, svc); err != nil {
		return s.fail(ctx, dep, svc, err)
	}

	dep.Succeed()
	if err := s.deployments.Update(ctx, dep); err != nil {
		return err
	}
	s.notify(ctx, svc, dep)
	return nil
}

func (s *DeploymentService) Get(ctx context.Context, id uuid.UUID) (*deployment.Deployment, error) {
	return s.deployments.FindByID(ctx, id)
}

// List returns the global deployment history, filtered by service, status and
// time range (F-MVP-09).
func (s *DeploymentService) List(ctx context.Context, filter ports.DeploymentFilter, page pagination.Page) ([]*deployment.Deployment, int64, error) {
	return s.deployments.List(ctx, filter, page)
}

func (s *DeploymentService) ListForService(ctx context.Context, serviceID uuid.UUID, page pagination.Page) ([]*deployment.Deployment, int64, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, 0, err
	}
	return s.deployments.ListByServiceID(ctx, serviceID, page)
}

// ServiceState returns the live orchestrator state of a service (F-MVP-10):
// running/desired/failed task counts and the per-task (container) details used
// to surface launch failures. A service that was never deployed reports a zero
// state rather than an error. Results are cached for a short TTL (<= 5s) so
// that supervising many services — and hitting both /status and /tasks back to
// back — does not overload the Swarm API.
func (s *DeploymentService) ServiceState(ctx context.Context, serviceID uuid.UUID) (*ports.ServiceState, error) {
	if s.orchestrator == nil {
		return nil, ErrOrchestratorUnavailable
	}
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.SwarmServiceID == "" {
		return &ports.ServiceState{}, nil
	}
	if cached, ok := s.stateCache.get(svc.SwarmServiceID); ok {
		return cached, nil
	}
	state, err := s.orchestrator.GetServiceState(ctx, svc.SwarmServiceID)
	if err != nil {
		return nil, err
	}
	s.stateCache.put(svc.SwarmServiceID, state)
	return state, nil
}

// ServiceLogs returns a live stream of a service's aggregated container logs
// (F-V2-01). The caller owns the returned reader and must Close it. A service
// that was never deployed yields ErrServiceNotDeployed.
func (s *DeploymentService) ServiceLogs(ctx context.Context, serviceID uuid.UUID, opts ports.LogOptions) (io.ReadCloser, error) {
	if s.orchestrator == nil {
		return nil, ErrOrchestratorUnavailable
	}
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.SwarmServiceID == "" {
		return nil, ErrServiceNotDeployed
	}
	return s.orchestrator.ServiceLogs(ctx, svc.SwarmServiceID, opts)
}

// ─── Engine internals ─────────────────────────────────────────────────────────

func (s *DeploymentService) reconcile(ctx context.Context, svc *service.Service) error {
	spec, err := s.buildSpec(ctx, svc)
	if err != nil {
		return err
	}

	if svc.SwarmServiceID == "" {
		id, err := s.orchestrator.DeployService(ctx, spec)
		if err != nil {
			return fmt.Errorf("deploy service: %w", err)
		}
		svc.SwarmServiceID = id
		// Persist the Swarm id immediately so a later failure does not orphan
		// the created service: subsequent deployments update it in place.
		if err := s.services.Update(ctx, svc); err != nil {
			return err
		}
	} else if err := s.orchestrator.UpdateService(ctx, svc.SwarmServiceID, spec); err != nil {
		return fmt.Errorf("update service: %w", err)
	}

	if err := s.orchestrator.WaitConvergence(ctx, svc.SwarmServiceID, s.timeout); err != nil {
		return fmt.Errorf("await convergence: %w", err)
	}
	return nil
}

func (s *DeploymentService) buildSpec(ctx context.Context, svc *service.Service) (ports.ServiceSpec, error) {
	spec := ports.ServiceSpec{
		Name:       svc.Name,
		Image:      svc.FullImage(),
		Replicas:   svc.Replicas,
		Command:    svc.Command,
		Entrypoint: svc.Entrypoint,
		Resources: ports.ResourceSpec{
			CPUReservation: svc.Resources.CPUReservation,
			CPULimit:       svc.Resources.CPULimit,
			MemReservation: svc.Resources.MemReservation,
			MemLimit:       svc.Resources.MemLimit,
		},
		UpdateConfig: ports.UpdateConfigSpec{
			Parallelism:     svc.UpdateConfig.Parallelism,
			Delay:           svc.UpdateConfig.Delay,
			FailureAction:   svc.UpdateConfig.FailureAction,
			Monitor:         svc.UpdateConfig.Monitor,
			MaxFailureRatio: svc.UpdateConfig.MaxFailureRatio,
			Order:           svc.UpdateConfig.Order,
		},
		Labels: map[string]string{"hivemind.service.id": svc.ID.String()},
	}

	// Environment variables.
	envVars, err := s.services.GetEnvVars(ctx, svc.ID)
	if err != nil {
		return spec, err
	}
	spec.Env = make(map[string]string, len(envVars))
	for _, e := range envVars {
		spec.Env[e.Key] = e.Value
	}

	// Networks — ensured on Swarm (the adapter is idempotent on name).
	netIDs, err := s.services.GetNetworkIDs(ctx, svc.ID)
	if err != nil {
		return spec, err
	}
	for _, nid := range netIDs {
		n, err := s.networks.FindByID(ctx, nid)
		if err != nil {
			return spec, err
		}
		swarmID, err := s.orchestrator.CreateNetwork(ctx, n.Name, n.Attachable)
		if err != nil {
			return spec, fmt.Errorf("ensure network %q: %w", n.Name, err)
		}
		spec.Networks = append(spec.Networks, ports.NetworkAttachment{SwarmNetworkID: swarmID})
	}

	// Secrets — value read server-side and pushed to Swarm under a versioned name.
	secAtt, err := s.services.GetSecretAttachments(ctx, svc.ID)
	if err != nil {
		return spec, err
	}
	for _, a := range secAtt {
		sec, err := s.secrets.FindByID(ctx, a.SecretID)
		if err != nil {
			return spec, err
		}
		value, err := s.secrets.GetValue(ctx, a.SecretID)
		if err != nil {
			return spec, err
		}
		swarmName := sec.SwarmSecretName()
		swarmID, err := s.orchestrator.CreateSecret(ctx, swarmName, value)
		if err != nil {
			return spec, fmt.Errorf("ensure secret %q: %w", sec.Name, err)
		}
		spec.Secrets = append(spec.Secrets, ports.SecretAttachment{
			SwarmSecretID:   swarmID,
			SwarmSecretName: swarmName,
			TargetPath:      a.TargetPath,
		})
	}

	// Configs — current version content pushed under a versioned name.
	cfgAtt, err := s.services.GetConfigAttachments(ctx, svc.ID)
	if err != nil {
		return spec, err
	}
	for _, a := range cfgAtt {
		cfg, err := s.configs.FindByID(ctx, a.ConfigID)
		if err != nil {
			return spec, err
		}
		content, err := s.currentConfigContent(ctx, cfg.ID, cfg.CurrentVersion)
		if err != nil {
			return spec, err
		}
		swarmName := fmt.Sprintf("%s_v%d", cfg.Name, cfg.CurrentVersion)
		swarmID, err := s.orchestrator.CreateConfig(ctx, swarmName, content)
		if err != nil {
			return spec, fmt.Errorf("ensure config %q: %w", cfg.Name, err)
		}
		spec.Configs = append(spec.Configs, ports.ConfigAttachment{
			SwarmConfigID:   swarmID,
			SwarmConfigName: swarmName,
			TargetPath:      a.TargetPath,
		})
	}

	return spec, nil
}

func (s *DeploymentService) currentConfigContent(ctx context.Context, configID uuid.UUID, version int) ([]byte, error) {
	versions, err := s.configs.ListVersions(ctx, configID)
	if err != nil {
		return nil, err
	}
	for _, v := range versions {
		if v.Version == version {
			return v.Content, nil
		}
	}
	return nil, fmt.Errorf("config %s: version %d not found", configID, version)
}

func (s *DeploymentService) fail(ctx context.Context, dep *deployment.Deployment, svc *service.Service, cause error) error {
	dep.Fail(cause.Error())
	if err := s.deployments.Update(ctx, dep); err != nil {
		return err
	}
	s.notify(ctx, svc, dep)
	return cause
}

func (s *DeploymentService) notify(ctx context.Context, svc *service.Service, dep *deployment.Deployment) {
	if s.notifier == nil {
		return
	}
	evt := ports.NotificationEvent{
		ServiceID:    dep.ServiceID,
		DeploymentID: dep.ID,
		Status:       dep.Status,
		ImageTag:     dep.ImageTag,
		Trigger:      dep.Trigger,
		ErrorMessage: dep.ErrorMessage,
	}
	if svc != nil {
		evt.ServiceName = svc.Name
	}
	if err := s.notifier.Notify(ctx, evt); err != nil {
		slog.Warn("deployment notification failed", "deployment_id", dep.ID, "err", err)
	}
}

// deploymentSnapshot is the safe, persisted view of a service at deploy time.
// It deliberately omits environment values (which may carry secrets).
type deploymentSnapshot struct {
	Image        string               `json:"image"`
	Replicas     uint64               `json:"replicas"`
	Command      []string             `json:"command,omitempty"`
	Entrypoint   []string             `json:"entrypoint,omitempty"`
	Resources    service.Resources    `json:"resources"`
	UpdateConfig service.UpdateConfig `json:"update_config"`
}

func buildSnapshot(svc *service.Service) json.RawMessage {
	snap := deploymentSnapshot{
		Image:        svc.FullImage(),
		Replicas:     svc.Replicas,
		Command:      svc.Command,
		Entrypoint:   svc.Entrypoint,
		Resources:    svc.Resources,
		UpdateConfig: svc.UpdateConfig,
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return b
}
