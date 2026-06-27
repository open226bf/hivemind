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

	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

var (
	ErrOrchestratorUnavailable = errors.New("cluster orchestrator unavailable")
	ErrServiceNotDeployed      = errors.New("service is not deployed")
	ErrContainerNotInService   = errors.New("container does not belong to this service")
)

// ErrDeploymentInProgress is returned when an undeploy is requested while a
// deployment is still pending. Undeploying mid-deploy would race the engine.
var ErrDeploymentInProgress = errors.New("a deployment is still in progress — wait for it to finish before undeploying")

const defaultConvergenceTimeout = 2 * time.Minute

// DeploymentService is the deployment engine (F-MVP-08). It assembles a service
// spec from the stored definition, provisions the required Swarm objects through
// the Orchestrator port, applies the deployment, waits for convergence and
// records the outcome. The Orchestrator and Notifier are kept behind ports so
// the engine itself is unit-testable without Docker.
type DeploymentService struct {
	services    ports.ServiceRepository
	deployments ports.DeploymentRepository
	networks    ports.NetworkRepository
	secrets     ports.SecretRepository
	configs     ports.ConfigRepository
	registry    ports.OrchestratorRegistry
	notifier    ports.Notifier // optional
	timeout     time.Duration
	stateCache  *stateCache
}

func NewDeploymentService(
	services ports.ServiceRepository,
	deployments ports.DeploymentRepository,
	networks ports.NetworkRepository,
	secrets ports.SecretRepository,
	configs ports.ConfigRepository,
	registry ports.OrchestratorRegistry,
	notifier ports.Notifier,
) *DeploymentService {
	return &DeploymentService{
		services:    services,
		deployments: deployments,
		networks:    networks,
		secrets:     secrets,
		configs:     configs,
		registry:    registry,
		notifier:    notifier,
		timeout:     defaultConvergenceTimeout,
		stateCache:  newStateCache(serviceStateTTL),
	}
}

// orchFor resolves the orchestrator for a service's cluster, mapping any
// resolution failure (no registry, unknown/unreachable cluster) to
// ErrOrchestratorUnavailable so callers keep their existing error handling.
func (s *DeploymentService) orchFor(ctx context.Context, svc *service.Service) (ports.Orchestrator, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("%w: deployment engine is not configured", ErrOrchestratorUnavailable)
	}
	orch, err := s.registry.For(ctx, svc.ClusterID)
	if err != nil {
		// Surface the real cause (agent offline, daemon down, unknown cluster…)
		// instead of a generic "not configured" message.
		return nil, fmt.Errorf("%w: %v", ErrOrchestratorUnavailable, err)
	}
	if orch == nil {
		return nil, ErrOrchestratorUnavailable
	}
	return orch, nil
}

type BeginDeploymentInput struct {
	ServiceID uuid.UUID
	UserID    *uuid.UUID // nil for webhook-triggered deployments
	Trigger   deployment.Trigger
	Options   DeployOptions
}

// DeployOptions controls how an in-place redeploy is applied. On the very first
// deploy (no SwarmServiceID yet) these are ignored — the service is created
// fresh. On subsequent deploys, Force recreates all tasks even when the spec
// is unchanged, and Repull asks Swarm to re-resolve the image from the
// registry so a moved tag (e.g. mariadb:latest) is picked up.
type DeployOptions struct {
	Force  bool
	Repull bool
}

// Begin validates and records a new pending deployment. It rejects a service
// that already has a deployment in flight. It does NOT run the engine — call
// Execute (or DeployAsync) to apply it.
func (s *DeploymentService) Begin(ctx context.Context, in BeginDeploymentInput) (*deployment.Deployment, error) {
	if s.registry == nil {
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
	opts := in.Options
	go func() {
		if err := s.Execute(context.Background(), dep.ID, opts); err != nil {
			slog.Error("deployment failed", "deployment_id", dep.ID, "service_id", dep.ServiceID, "err", err)
		}
	}()
	return dep, nil
}

// Execute applies a previously recorded pending deployment: it builds the spec,
// provisions Swarm objects, deploys or updates the service, waits for
// convergence and records success/failure. It is synchronous and returns the
// terminal error (if any) so it can be unit-tested directly.
func (s *DeploymentService) Execute(ctx context.Context, deploymentID uuid.UUID, opts DeployOptions) error {
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

	if err := s.reconcile(ctx, svc, opts); err != nil {
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
//
// External drift: if the swarm service has been removed out-of-band (someone
// ran `docker service rm`), the persisted status is reconciled to "removed"
// and the response is flagged ExternallyRemoved so the UI can surface it.
func (s *DeploymentService) ServiceState(ctx context.Context, serviceID uuid.UUID) (*ports.ServiceState, error) {
	if s.registry == nil {
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

	orch, err := s.orchFor(ctx, svc)
	if err != nil {
		return nil, err
	}
	state, err := orch.GetServiceState(ctx, svc.SwarmServiceID)
	if err != nil {
		if errors.Is(err, ports.ErrSwarmServiceNotFound) {
			drift := s.reconcileExternalRemoval(ctx, svc)
			return drift, nil
		}
		return nil, err
	}
	s.stateCache.put(svc.SwarmServiceID, state)
	return state, nil
}

// reconcileExternalRemoval handles the case where the swarm service was deleted
// outside of Hivemind. It transitions the persisted status to "removed", clears
// the dangling SwarmServiceID, and returns a synthetic state flagged so callers
// can surface the drift. Persistence errors are logged but do not prevent
// returning the drift state — the next call will simply retry.
func (s *DeploymentService) reconcileExternalRemoval(ctx context.Context, svc *service.Service) *ports.ServiceState {
	if svc.Status == service.StatusDeployed {
		svc.Status = service.StatusRemoved
		svc.SwarmServiceID = ""
		svc.UpdatedAt = time.Now().UTC()
		if err := s.services.Update(ctx, svc); err != nil {
			slog.Warn("reconcile external removal: persist failed", "service_id", svc.ID, "err", err)
		}
	}
	return &ports.ServiceState{ExternallyRemoved: true}
}

// Undeploy removes the swarm service for a deployed service and transitions
// its status to "removed". The service definition stays in Hivemind (history,
// env vars, attachments preserved) and can be redeployed later. Idempotent:
// a service that is already removed/draft is a no-op success. A service with
// a deployment in progress is rejected so we do not race the engine.
func (s *DeploymentService) Undeploy(ctx context.Context, serviceID uuid.UUID) (*service.Service, error) {
	if s.registry == nil {
		return nil, ErrOrchestratorUnavailable
	}
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.Status != service.StatusDeployed {
		return svc, nil
	}
	if _, err := s.deployments.FindActiveByServiceID(ctx, serviceID); err == nil {
		return nil, ErrDeploymentInProgress
	} else if !errors.Is(err, domainerrors.ErrNotFound) {
		return nil, err
	}

	if svc.SwarmServiceID != "" {
		orch, err := s.orchFor(ctx, svc)
		if err != nil {
			return nil, err
		}
		if err := orch.RemoveService(ctx, svc.SwarmServiceID); err != nil {
			return nil, fmt.Errorf("remove swarm service: %w", err)
		}
		s.stateCache.invalidate(svc.SwarmServiceID)
	}

	svc.Status = service.StatusRemoved
	svc.SwarmServiceID = ""
	svc.UpdatedAt = time.Now().UTC()
	if err := s.services.Update(ctx, svc); err != nil {
		return nil, err
	}
	return svc, nil
}

// ServiceLogs returns a live stream of a service's aggregated container logs
// (F-V2-01). The caller owns the returned reader and must Close it. A service
// that was never deployed yields ErrServiceNotDeployed.
func (s *DeploymentService) ServiceLogs(ctx context.Context, serviceID uuid.UUID, opts ports.LogOptions) (io.ReadCloser, error) {
	if s.registry == nil {
		return nil, ErrOrchestratorUnavailable
	}
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.SwarmServiceID == "" {
		return nil, ErrServiceNotDeployed
	}
	orch, err := s.orchFor(ctx, svc)
	if err != nil {
		return nil, err
	}
	return orch.ServiceLogs(ctx, svc.SwarmServiceID, opts)
}

// ExecContainer opens an interactive exec session in one of the service's
// containers (web terminal). The containerID is validated against the service's
// live tasks so a caller cannot exec into an unrelated container.
func (s *DeploymentService) ExecContainer(ctx context.Context, serviceID uuid.UUID, containerID string, opts ports.ExecOptions) (ports.ExecStream, error) {
	if s.registry == nil {
		return nil, ErrOrchestratorUnavailable
	}
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.SwarmServiceID == "" {
		return nil, ErrServiceNotDeployed
	}

	orch, err := s.orchFor(ctx, svc)
	if err != nil {
		return nil, err
	}
	state, err := orch.GetServiceState(ctx, svc.SwarmServiceID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, t := range state.Tasks {
		if t.ContainerID != "" && t.ContainerID == containerID {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrContainerNotInService
	}

	return orch.ExecContainer(ctx, containerID, opts)
}

// ─── Engine internals ─────────────────────────────────────────────────────────

func (s *DeploymentService) reconcile(ctx context.Context, svc *service.Service, opts DeployOptions) error {
	orch, err := s.orchFor(ctx, svc)
	if err != nil {
		return err
	}

	spec, err := s.buildSpec(ctx, orch, svc)
	if err != nil {
		return err
	}

	if svc.SwarmServiceID == "" {
		id, err := orch.DeployService(ctx, spec)
		if err != nil {
			return fmt.Errorf("deploy service: %w", err)
		}
		svc.SwarmServiceID = id
		// Persist the Swarm id immediately so a later failure does not orphan
		// the created service: subsequent deployments update it in place.
		if err := s.services.Update(ctx, svc); err != nil {
			return err
		}
	} else {
		updateOpts := ports.UpdateServiceOptions{
			Force:         opts.Force,
			QueryRegistry: opts.Repull,
		}
		if err := orch.UpdateService(ctx, svc.SwarmServiceID, spec, updateOpts); err != nil {
			return fmt.Errorf("update service: %w", err)
		}
	}

	if err := orch.WaitConvergence(ctx, svc.SwarmServiceID, s.timeout); err != nil {
		return fmt.Errorf("await convergence: %w", err)
	}
	return nil
}

func (s *DeploymentService) buildSpec(ctx context.Context, orch ports.Orchestrator, svc *service.Service) (ports.ServiceSpec, error) {
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
		Placement: ports.PlacementSpec{
			Constraints: svc.Placement.Constraints,
			Preferences: svc.Placement.Preferences,
			MaxReplicas: svc.Placement.MaxReplicas,
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
		swarmID, err := orch.CreateNetwork(ctx, n.Name, ports.CreateNetworkOptions{
			Attachable: n.Attachable,
			Subnet:     n.Subnet,
		})
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
		swarmID, err := orch.CreateSecret(ctx, swarmName, value)
		if err != nil {
			return spec, fmt.Errorf("ensure secret %q: %w", sec.Name, err)
		}
		spec.Secrets = append(spec.Secrets, ports.SecretAttachment{
			SwarmSecretID:   swarmID,
			SwarmSecretName: swarmName,
			Name:            sec.Name,
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
		swarmID, err := orch.CreateConfig(ctx, swarmName, content)
		if err != nil {
			return spec, fmt.Errorf("ensure config %q: %w", cfg.Name, err)
		}
		spec.Configs = append(spec.Configs, ports.ConfigAttachment{
			SwarmConfigID:   swarmID,
			SwarmConfigName: swarmName,
			Name:            cfg.Name,
			TargetPath:      a.TargetPath,
		})
	}

	// Mounts (F-V2-06) — passed straight through. Local volumes are created
	// per-node by Docker when a task first mounts them, so no pre-provisioning.
	mounts, err := s.services.GetMounts(ctx, svc.ID)
	if err != nil {
		return spec, err
	}
	for _, m := range mounts {
		spec.Mounts = append(spec.Mounts, ports.MountSpec{
			Type:     string(m.Type),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	// Published ports — passed straight through to the endpoint spec.
	pubPorts, err := s.services.GetPorts(ctx, svc.ID)
	if err != nil {
		return spec, err
	}
	for _, p := range pubPorts {
		spec.Ports = append(spec.Ports, ports.PortSpec{
			TargetPort:    p.TargetPort,
			PublishedPort: p.PublishedPort,
			Protocol:      p.Protocol,
			Mode:          p.Mode,
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
	Placement    service.Placement    `json:"placement"`
	UpdateConfig service.UpdateConfig `json:"update_config"`
}

func buildSnapshot(svc *service.Service) json.RawMessage {
	snap := deploymentSnapshot{
		Image:        svc.FullImage(),
		Replicas:     svc.Replicas,
		Command:      svc.Command,
		Entrypoint:   svc.Entrypoint,
		Resources:    svc.Resources,
		Placement:    svc.Placement,
		UpdateConfig: svc.UpdateConfig,
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return b
}
