package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
)

// ErrAlreadyManaged is returned when adoption targets a Swarm service that a
// Hivemind record already owns (its swarm_service_id is already persisted).
var ErrAlreadyManaged = errors.New("service is already managed by Hivemind")

// AdoptInput parameterises the adoption of a foreign Swarm service (ADR 0004).
type AdoptInput struct {
	ClusterID      uuid.UUID
	SwarmServiceID string
	HiveID         *uuid.UUID // project to attach the adopted service to (nil = unassigned)
	UserID         *uuid.UUID // for snapshot authorship / audit
}

// AdoptResult reports the created Hivemind service and any fidelity warnings
// raised while reconstructing its spec.
type AdoptResult struct {
	ServiceID uuid.UUID
	Warnings  []string
}

// Adopt takes over a foreign Swarm service: it reconstructs the service spec,
// creates a deployed Hivemind record attached to the requested hive, seals the
// hivemind.service.id label on the live service, and records an initial
// snapshot. The live service keeps running; only its labels change.
//
// On a seal failure the freshly-created record is rolled back, so a half-adopted
// state (DB record without the ownership label) cannot linger.
func (s *DiscoveryService) Adopt(ctx context.Context, in AdoptInput) (*AdoptResult, error) {
	orch, err := s.orchFor(ctx, in.ClusterID)
	if err != nil {
		return nil, err
	}

	// Refuse double-adoption: a record already owning this Swarm service.
	owned, err := s.findBySwarmID(ctx, in.ClusterID, in.SwarmServiceID)
	if err != nil {
		return nil, err
	}
	if owned != nil {
		return nil, ErrAlreadyManaged
	}

	inspected, err := orch.InspectService(ctx, in.SwarmServiceID)
	if err != nil {
		return nil, fmt.Errorf("inspect service: %w", err)
	}

	svc, err := buildAdoptedService(in, inspected.Spec)
	if err != nil {
		return nil, err
	}
	if err := s.services.Save(ctx, svc); err != nil {
		return nil, fmt.Errorf("persist adopted service: %w", err)
	}

	warnings := append([]string(nil), inspected.Warnings...)

	// Carry the cleanly-mappable attachments. These persist as-is (the repo does
	// not re-validate), so adoption never fails on a foreign service's data.
	if env := envVarsFromSpec(svc.ID, inspected.Spec.Env); len(env) > 0 {
		if err := s.services.SetEnvVars(ctx, svc.ID, env); err != nil {
			warnings = append(warnings, "environment variables could not be imported")
		}
	}
	if len(inspected.Spec.Ports) > 0 {
		if err := s.services.SetPorts(ctx, svc.ID, portsFromSpec(inspected.Spec.Ports)); err != nil {
			warnings = append(warnings, "published ports could not be imported")
		}
	}

	// Seal ownership last: if it fails, undo the record so we never leave an
	// unlabelled service masquerading as managed.
	if err := orch.SetHivemindLabel(ctx, in.SwarmServiceID, svc.ID.String()); err != nil {
		_ = s.services.Delete(ctx, svc.ID)
		return nil, fmt.Errorf("seal ownership label: %w", err)
	}

	// Initial snapshot is the pre-Hivemind rollback floor; best-effort.
	if s.snapshots != nil {
		if _, err := s.snapshots.Capture(ctx, svc.ID, "adoption", in.UserID); err != nil {
			warnings = append(warnings, "initial snapshot could not be recorded")
		}
	}

	return &AdoptResult{ServiceID: svc.ID, Warnings: warnings}, nil
}

// Release hands an adopted service (identified by its Swarm service id) back to
// unmanaged: it clears the ownership label on the live service (which keeps
// running) and deletes the Hivemind record. The Swarm service must currently be
// owned by a Hivemind record on this cluster.
func (s *DiscoveryService) Release(ctx context.Context, clusterID uuid.UUID, swarmServiceID string) error {
	orch, err := s.orchFor(ctx, clusterID)
	if err != nil {
		return err
	}
	svc, err := s.findBySwarmID(ctx, clusterID, swarmServiceID)
	if err != nil {
		return err
	}
	if svc == nil {
		return fmt.Errorf("%w", ErrServiceNotAdopted)
	}
	if err := orch.ClearHivemindLabel(ctx, swarmServiceID); err != nil {
		return fmt.Errorf("clear ownership label: %w", err)
	}
	if err := s.services.Delete(ctx, svc.ID); err != nil {
		return fmt.Errorf("delete released record: %w", err)
	}
	return nil
}

// ErrServiceNotAdopted is returned when release targets a Swarm service that no
// Hivemind record owns.
var ErrServiceNotAdopted = errors.New("no managed Hivemind service owns this Swarm service")

// OwnedHive returns the hive of the managed Hivemind service that owns swarmID on
// the cluster (nil = no hive), or ErrServiceNotAdopted when none does. The API
// uses it to authorize release against the owning hive (ADR 0003/0004) before
// acting.
func (s *DiscoveryService) OwnedHive(ctx context.Context, clusterID uuid.UUID, swarmServiceID string) (*uuid.UUID, error) {
	svc, err := s.findBySwarmID(ctx, clusterID, swarmServiceID)
	if err != nil {
		return nil, err
	}
	if svc == nil {
		return nil, ErrServiceNotAdopted
	}
	return svc.HiveID, nil
}

// findBySwarmID returns the cluster's persisted service that owns swarmID, or
// nil when none does.
func (s *DiscoveryService) findBySwarmID(ctx context.Context, clusterID uuid.UUID, swarmID string) (*service.Service, error) {
	filter := ports.ServiceFilter{ClusterID: &clusterID}
	items, _, err := s.services.List(ctx, filter, pageAll())
	if err != nil {
		return nil, fmt.Errorf("list known services: %w", err)
	}
	for _, svc := range items {
		if svc.SwarmServiceID == swarmID {
			return svc, nil
		}
	}
	return nil, nil
}

// buildAdoptedService maps a reconstructed spec to a deployed Service record. It
// fills the update config with sensible defaults when the live service did not
// report a complete one, so the record stays editable post-adoption.
func buildAdoptedService(in AdoptInput, spec ports.ServiceSpec) (*service.Service, error) {
	image, tag := splitImageTag(spec.Image)
	svc, err := service.NewAdopted(spec.Name, image, tag, spec.Replicas, in.HiveID)
	if err != nil {
		return nil, err
	}
	svc.ClusterID = in.ClusterID
	svc.Status = service.StatusDeployed
	svc.SwarmServiceID = in.SwarmServiceID
	svc.Command = spec.Command
	svc.Entrypoint = spec.Entrypoint
	svc.Resources = service.Resources{
		CPUReservation: spec.Resources.CPUReservation,
		CPULimit:       spec.Resources.CPULimit,
		MemReservation: spec.Resources.MemReservation,
		MemLimit:       spec.Resources.MemLimit,
	}
	svc.Placement = service.Placement{
		Constraints: spec.Placement.Constraints,
		Preferences: spec.Placement.Preferences,
		MaxReplicas: spec.Placement.MaxReplicas,
	}
	svc.UpdateConfig = adoptedUpdateConfig(spec.UpdateConfig)
	return svc, nil
}

// adoptedUpdateConfig converts the inspected update config, falling back to the
// defaults when the live service did not expose a complete one (an empty
// failure-action/order would otherwise be an invalid record).
func adoptedUpdateConfig(uc ports.UpdateConfigSpec) service.UpdateConfig {
	if uc.FailureAction == "" || uc.Order == "" {
		return service.DefaultUpdateConfig()
	}
	return service.UpdateConfig{
		Parallelism:     uc.Parallelism,
		Delay:           uc.Delay,
		FailureAction:   uc.FailureAction,
		Monitor:         uc.Monitor,
		MaxFailureRatio: uc.MaxFailureRatio,
		Order:           uc.Order,
	}
}

func envVarsFromSpec(serviceID uuid.UUID, env map[string]string) []service.EnvVar {
	if len(env) == 0 {
		return nil
	}
	out := make([]service.EnvVar, 0, len(env))
	for k, v := range env {
		out = append(out, service.EnvVar{ID: uuid.New(), ServiceID: serviceID, Key: k, Value: v})
	}
	return out
}

func portsFromSpec(in []ports.PortSpec) []service.Port {
	out := make([]service.Port, 0, len(in))
	for _, p := range in {
		out = append(out, service.Port{
			TargetPort:    p.TargetPort,
			PublishedPort: p.PublishedPort,
			Protocol:      p.Protocol,
			Mode:          p.Mode,
		})
	}
	return out
}

// splitImageTag splits a reference into image and tag, mirroring Service.FullImage
// (image + ":" + tag). It only treats a colon after the last slash as the tag
// separator, so a registry port (e.g. "reg:5000/img") is not mistaken for a tag.
func splitImageTag(ref string) (image, tag string) {
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, ""
}
