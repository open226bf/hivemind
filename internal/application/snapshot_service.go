package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/snapshot"
	"github.com/open226bf/hivemind/internal/domain/volume"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// SnapshotService captures complete, point-in-time snapshots of a service and
// restores them on demand (manual rollback). A snapshot embeds the resolved
// values of every element the service uses (env, secrets, configs, networks,
// mounts) so it is a self-contained restore point that survives later deletion
// or rotation of those resources.
type SnapshotService struct {
	snapshots ports.SnapshotRepository
	services  ports.ServiceRepository
	networks  ports.NetworkRepository
	secrets   ports.SecretRepository
	configs   ports.ConfigRepository
	deployer  *DeploymentService
}

func NewSnapshotService(
	snapshots ports.SnapshotRepository,
	services ports.ServiceRepository,
	networks ports.NetworkRepository,
	secrets ports.SecretRepository,
	configs ports.ConfigRepository,
	deployer *DeploymentService,
) *SnapshotService {
	return &SnapshotService{
		snapshots: snapshots,
		services:  services,
		networks:  networks,
		secrets:   secrets,
		configs:   configs,
		deployer:  deployer,
	}
}

// ─── Capture ────────────────────────────────────────────────────────────────

// Capture builds a snapshot from the service's current definition, resolving
// and embedding the live values of every attached element.
func (s *SnapshotService) Capture(ctx context.Context, serviceID uuid.UUID, label string, userID *uuid.UUID) (*snapshot.ServiceSnapshot, error) {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	payload := snapshot.Payload{
		Name:        svc.Name,
		Description: svc.Description,
		Image:       svc.Image,
		Tag:         svc.Tag,
		Replicas:    svc.Replicas,
		Command:     svc.Command,
		Entrypoint:  svc.Entrypoint,
		Resources: snapshot.Resources{
			CPUReservation: svc.Resources.CPUReservation,
			CPULimit:       svc.Resources.CPULimit,
			MemReservation: svc.Resources.MemReservation,
			MemLimit:       svc.Resources.MemLimit,
		},
		Placement: snapshot.Placement{
			Constraints: svc.Placement.Constraints,
			Preferences: svc.Placement.Preferences,
			MaxReplicas: svc.Placement.MaxReplicas,
		},
		UpdateConfig: snapshot.UpdateConfig{
			Parallelism:     svc.UpdateConfig.Parallelism,
			DelayNs:         int64(svc.UpdateConfig.Delay),
			FailureAction:   svc.UpdateConfig.FailureAction,
			MonitorNs:       int64(svc.UpdateConfig.Monitor),
			MaxFailureRatio: svc.UpdateConfig.MaxFailureRatio,
			Order:           svc.UpdateConfig.Order,
		},
	}
	if svc.HiveID != nil {
		payload.HiveID = svc.HiveID.String()
	}

	// Env vars (values come back decrypted from the repository).
	envVars, err := s.services.GetEnvVars(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, e := range envVars {
		payload.EnvVars = append(payload.EnvVars, snapshot.EnvVar{Key: e.Key, Value: e.Value, IsSecret: e.IsSecret})
	}

	// Networks — capture defining attributes.
	netIDs, err := s.services.GetNetworkIDs(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, nid := range netIDs {
		n, err := s.networks.FindByID(ctx, nid)
		if err != nil {
			return nil, fmt.Errorf("resolve network %s: %w", nid, err)
		}
		payload.Networks = append(payload.Networks, snapshot.Network{
			ID:         n.ID.String(),
			Name:       n.Name,
			Subnet:     n.Subnet,
			Attachable: n.Attachable,
		})
	}

	// Secrets — embed the resolved value at its current version.
	secAtt, err := s.services.GetSecretAttachments(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, a := range secAtt {
		sec, err := s.secrets.FindByID(ctx, a.SecretID)
		if err != nil {
			return nil, fmt.Errorf("resolve secret %s: %w", a.SecretID, err)
		}
		value, err := s.secrets.GetValue(ctx, a.SecretID)
		if err != nil {
			return nil, fmt.Errorf("read secret value %s: %w", a.SecretID, err)
		}
		payload.Secrets = append(payload.Secrets, snapshot.Secret{
			ID:         sec.ID.String(),
			Name:       sec.Name,
			Version:    sec.CurrentVersion,
			Checksum:   sec.Checksum,
			TargetPath: a.TargetPath,
			Value:      string(value),
		})
	}

	// Configs — embed the current version content.
	cfgAtt, err := s.services.GetConfigAttachments(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, a := range cfgAtt {
		cfg, err := s.configs.FindByID(ctx, a.ConfigID)
		if err != nil {
			return nil, fmt.Errorf("resolve config %s: %w", a.ConfigID, err)
		}
		content, err := s.currentConfigContent(ctx, cfg.ID, cfg.CurrentVersion)
		if err != nil {
			return nil, err
		}
		payload.Configs = append(payload.Configs, snapshot.Config{
			ID:         cfg.ID.String(),
			Name:       cfg.Name,
			Version:    cfg.CurrentVersion,
			Checksum:   checksum(content),
			TargetPath: a.TargetPath,
			Content:    string(content),
		})
	}

	// Mounts.
	mounts, err := s.services.GetMounts(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	for _, m := range mounts {
		payload.Mounts = append(payload.Mounts, snapshot.Mount{
			Type:     string(m.Type),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}

	snap, err := snapshot.New(serviceID, label, userID, payload)
	if err != nil {
		return nil, err
	}
	if err := s.snapshots.Save(ctx, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *SnapshotService) Get(ctx context.Context, id uuid.UUID) (*snapshot.ServiceSnapshot, error) {
	return s.snapshots.FindByID(ctx, id)
}

func (s *SnapshotService) ListForService(ctx context.Context, serviceID uuid.UUID, page pagination.Page) ([]*snapshot.ServiceSnapshot, int64, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, 0, err
	}
	return s.snapshots.ListByServiceID(ctx, serviceID, page)
}

func (s *SnapshotService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.snapshots.Delete(ctx, id)
}

// ─── Rollback ─────────────────────────────────────────────────────────────────

// RollbackResult reports the outcome of a rollback: the new deployment that was
// triggered plus any non-fatal warnings (e.g. a secret was recreated from the
// snapshot, or its live value has drifted since capture).
type RollbackResult struct {
	Deployment *deployment.Deployment
	Warnings   []string
}

// Rollback restores the service definition from a snapshot, then triggers a new
// deployment (Trigger=rollback). Service-owned data (spec, env, mounts) is
// restored with full fidelity. Shared resources (networks, secrets, configs)
// are re-attached to match the snapshot; a resource that no longer exists is
// recreated from the embedded value, and one whose value has drifted since
// capture is attached at its current version with a warning.
func (s *SnapshotService) Rollback(ctx context.Context, snapshotID uuid.UUID, userID *uuid.UUID) (*RollbackResult, error) {
	if s.deployer == nil {
		return nil, ErrOrchestratorUnavailable
	}
	snap, err := s.snapshots.FindByID(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	if snap.SchemaVersion != snapshot.SchemaVersion {
		return nil, snapshot.ErrSchemaUnknown
	}
	svc, err := s.services.FindByID(ctx, snap.ServiceID)
	if err != nil {
		return nil, err
	}
	p := snap.Payload
	warnings := []string{}

	// 1. Restore the core service definition.
	svc.Description = p.Description
	svc.Image = p.Image
	svc.Tag = p.Tag
	svc.Replicas = p.Replicas
	svc.Command = p.Command
	svc.Entrypoint = p.Entrypoint
	if err := svc.SetResources(service.Resources{
		CPUReservation: p.Resources.CPUReservation,
		CPULimit:       p.Resources.CPULimit,
		MemReservation: p.Resources.MemReservation,
		MemLimit:       p.Resources.MemLimit,
	}); err != nil {
		return nil, err
	}
	if err := svc.SetPlacement(service.Placement{
		Constraints: p.Placement.Constraints,
		Preferences: p.Placement.Preferences,
		MaxReplicas: p.Placement.MaxReplicas,
	}); err != nil {
		return nil, err
	}
	svc.UpdateConfig = service.UpdateConfig{
		Parallelism:     p.UpdateConfig.Parallelism,
		Delay:           time.Duration(p.UpdateConfig.DelayNs),
		FailureAction:   p.UpdateConfig.FailureAction,
		Monitor:         time.Duration(p.UpdateConfig.MonitorNs),
		MaxFailureRatio: p.UpdateConfig.MaxFailureRatio,
		Order:           p.UpdateConfig.Order,
	}
	svc.UpdatedAt = time.Now().UTC()
	if err := s.services.Update(ctx, svc); err != nil {
		return nil, err
	}

	// 2. Env vars (service-owned — full fidelity).
	vars := make([]service.EnvVar, 0, len(p.EnvVars))
	for _, e := range p.EnvVars {
		vars = append(vars, service.EnvVar{ID: uuid.New(), ServiceID: svc.ID, Key: e.Key, Value: e.Value, IsSecret: e.IsSecret})
	}
	if err := s.services.SetEnvVars(ctx, svc.ID, vars); err != nil {
		return nil, err
	}

	// 3. Mounts (service-owned).
	mounts := make([]volume.Mount, 0, len(p.Mounts))
	for _, m := range p.Mounts {
		mounts = append(mounts, volume.Mount{Type: volume.MountType(m.Type), Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	if err := s.services.SetMounts(ctx, svc.ID, mounts); err != nil {
		return nil, err
	}

	// 4. Re-sync attachment topology.
	createdBy := uuid.Nil
	if userID != nil {
		createdBy = *userID
	}
	if w := s.restoreNetworks(ctx, svc.ID, p.Networks); len(w) > 0 {
		warnings = append(warnings, w...)
	}
	if w := s.restoreSecrets(ctx, svc.ID, p.Secrets, createdBy); len(w) > 0 {
		warnings = append(warnings, w...)
	}
	if w := s.restoreConfigs(ctx, svc.ID, p.Configs, createdBy); len(w) > 0 {
		warnings = append(warnings, w...)
	}

	// 5. Trigger a rollback deployment.
	dep, err := s.deployer.DeployAsync(ctx, BeginDeploymentInput{
		ServiceID: svc.ID,
		UserID:    userID,
		Trigger:   deployment.TriggerRollback,
	})
	if err != nil {
		return nil, err
	}

	return &RollbackResult{Deployment: dep, Warnings: warnings}, nil
}

// restoreNetworks detaches the current networks and attaches the snapshot's set,
// recreating any that were deleted meanwhile.
func (s *SnapshotService) restoreNetworks(ctx context.Context, serviceID uuid.UUID, want []snapshot.Network) []string {
	var warnings []string
	current, err := s.services.GetNetworkIDs(ctx, serviceID)
	if err == nil {
		for _, id := range current {
			_ = s.services.DetachNetwork(ctx, serviceID, id)
		}
	}
	for _, n := range want {
		nid, _ := uuid.Parse(n.ID)
		if _, err := s.networks.FindByID(ctx, nid); errors.Is(err, domainerrors.ErrNotFound) {
			recreated, rerr := network.New(n.Name)
			if rerr != nil {
				warnings = append(warnings, fmt.Sprintf("réseau %q introuvable et non recréé: %v", n.Name, rerr))
				continue
			}
			recreated.ID = nid
			recreated.Subnet = n.Subnet
			recreated.Attachable = n.Attachable
			if rerr := s.networks.Save(ctx, recreated); rerr != nil {
				warnings = append(warnings, fmt.Sprintf("réseau %q introuvable et non recréé: %v", n.Name, rerr))
				continue
			}
			warnings = append(warnings, fmt.Sprintf("réseau %q recréé depuis le snapshot", n.Name))
		} else if err != nil {
			warnings = append(warnings, fmt.Sprintf("réseau %q non vérifiable: %v", n.Name, err))
			continue
		}
		if err := s.services.AttachNetwork(ctx, serviceID, nid); err != nil {
			warnings = append(warnings, fmt.Sprintf("réseau %q non rattaché: %v", n.Name, err))
		}
	}
	return warnings
}

func (s *SnapshotService) restoreSecrets(ctx context.Context, serviceID uuid.UUID, want []snapshot.Secret, createdBy uuid.UUID) []string {
	var warnings []string
	current, err := s.services.GetSecretAttachments(ctx, serviceID)
	if err == nil {
		for _, a := range current {
			_ = s.services.DetachSecret(ctx, serviceID, a.SecretID)
		}
	}
	for _, sec := range want {
		sid, _ := uuid.Parse(sec.ID)
		live, err := s.secrets.FindByID(ctx, sid)
		switch {
		case errors.Is(err, domainerrors.ErrNotFound):
			recreated, _, rerr := secret.New(sec.Name, sec.TargetPath, []byte(sec.Value), createdBy)
			if rerr != nil {
				warnings = append(warnings, fmt.Sprintf("secret %q introuvable et non recréé: %v", sec.Name, rerr))
				continue
			}
			recreated.ID = sid
			v := &secret.SecretVersion{ID: uuid.New(), SecretID: sid, Version: recreated.CurrentVersion, Checksum: recreated.Checksum, CreatedAt: time.Now().UTC()}
			if rerr := s.secrets.Save(ctx, recreated, v, []byte(sec.Value)); rerr != nil {
				warnings = append(warnings, fmt.Sprintf("secret %q introuvable et non recréé: %v", sec.Name, rerr))
				continue
			}
			warnings = append(warnings, fmt.Sprintf("secret %q recréé depuis le snapshot", sec.Name))
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("secret %q non vérifiable: %v", sec.Name, err))
			continue
		default:
			if live.Checksum != sec.Checksum {
				warnings = append(warnings, fmt.Sprintf("secret %q a changé depuis le snapshot — la valeur actuelle (v%d) sera déployée", sec.Name, live.CurrentVersion))
			}
		}
		if err := s.services.AttachSecret(ctx, serviceID, sid, sec.TargetPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("secret %q non rattaché: %v", sec.Name, err))
		}
	}
	return warnings
}

func (s *SnapshotService) restoreConfigs(ctx context.Context, serviceID uuid.UUID, want []snapshot.Config, createdBy uuid.UUID) []string {
	var warnings []string
	current, err := s.services.GetConfigAttachments(ctx, serviceID)
	if err == nil {
		for _, a := range current {
			_ = s.services.DetachConfig(ctx, serviceID, a.ConfigID)
		}
	}
	for _, cfg := range want {
		cid, _ := uuid.Parse(cfg.ID)
		live, err := s.configs.FindByID(ctx, cid)
		switch {
		case errors.Is(err, domainerrors.ErrNotFound):
			recreated, v, rerr := config.New(cfg.Name, cfg.TargetPath, []byte(cfg.Content), "restauré depuis un snapshot", createdBy)
			if rerr != nil {
				warnings = append(warnings, fmt.Sprintf("config %q introuvable et non recréée: %v", cfg.Name, rerr))
				continue
			}
			recreated.ID = cid
			v.ConfigID = cid
			if rerr := s.configs.Save(ctx, recreated, v); rerr != nil {
				warnings = append(warnings, fmt.Sprintf("config %q introuvable et non recréée: %v", cfg.Name, rerr))
				continue
			}
			warnings = append(warnings, fmt.Sprintf("config %q recréée depuis le snapshot", cfg.Name))
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("config %q non vérifiable: %v", cfg.Name, err))
			continue
		default:
			if liveContent, cerr := s.currentConfigContent(ctx, live.ID, live.CurrentVersion); cerr == nil && checksum(liveContent) != cfg.Checksum {
				warnings = append(warnings, fmt.Sprintf("config %q a changé depuis le snapshot — la version actuelle (v%d) sera déployée", cfg.Name, live.CurrentVersion))
			}
		}
		if err := s.services.AttachConfig(ctx, serviceID, cid, cfg.TargetPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("config %q non rattachée: %v", cfg.Name, err))
		}
	}
	return warnings
}

func (s *SnapshotService) currentConfigContent(ctx context.Context, configID uuid.UUID, version int) ([]byte, error) {
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

func checksum(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
