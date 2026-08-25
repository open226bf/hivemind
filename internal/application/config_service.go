package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/linediff"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ErrVersionNotFound is returned when a diff/restore references a version
// number that does not exist for the config.
var ErrVersionNotFound = errors.New("config version not found")

// ConfigService manages versioned, cleartext configuration files and their
// attachment to services (F-MVP-07). Unlike secrets, config content is
// readable through the API.
type ConfigService struct {
	configs       ports.ConfigRepository
	services      ports.ServiceRepository
	orchestrators ports.OrchestratorRegistry
	tx            ports.TxManager
}

// NewConfigService wires the config use case. orchestrators and tx may be nil
// (tests, or a deployment with no orchestrator): writes then only touch the
// database, as they did before configs were pushed to the cluster up front.
func NewConfigService(
	configs ports.ConfigRepository,
	services ports.ServiceRepository,
	orchestrators ports.OrchestratorRegistry,
	tx ports.TxManager,
) *ConfigService {
	return &ConfigService{configs: configs, services: services, orchestrators: orchestrators, tx: tx}
}

type CreateConfigInput struct {
	Name       string
	TargetPath string
	Content    []byte
	Comment    string
	CreatedBy  uuid.UUID
	// Cluster is the target cluster id. Empty selects the default cluster.
	Cluster uuid.UUID
}

// ServiceConfig pairs an attached config with the mount path chosen for a
// specific service.
type ServiceConfig struct {
	Config     *config.Config
	TargetPath string
}

// Create stores a new config with its first version and pushes that version to
// the target cluster in the same transaction: either the config exists on both
// sides, or on neither.
func (s *ConfigService) Create(ctx context.Context, in CreateConfigInput) (*config.Config, error) {
	cfg, ver, err := config.New(in.Name, in.TargetPath, in.Content, in.Comment, in.CreatedBy)
	if err != nil {
		return nil, err
	}
	cfg.ClusterID = in.Cluster

	res := clusterResource{
		Persist: func(ctx context.Context) error { return s.configs.Save(ctx, cfg, ver) },
		Create:  s.pushVersion(cfg, ver),
		Record:  s.recordVersion(cfg, ver),
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveConfig(ctx, swarmID)
		},
	}
	if err := res.create(ctx, s.tx, s.orchestrators, cfg.ClusterID, "config "+cfg.Name); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SwarmConfigName is the name a config version carries on the cluster. Swarm
// configs are immutable, so every version is its own object.
func SwarmConfigName(name string, version int) string {
	return fmt.Sprintf("%s_v%d", name, version)
}

func (s *ConfigService) pushVersion(cfg *config.Config, ver *config.ConfigVersion) func(context.Context, ports.Orchestrator) (string, error) {
	return func(ctx context.Context, orch ports.Orchestrator) (string, error) {
		return orch.CreateConfig(ctx, SwarmConfigName(cfg.Name, ver.Version), ver.Content)
	}
}

func (s *ConfigService) recordVersion(cfg *config.Config, ver *config.ConfigVersion) func(context.Context, string) error {
	return func(ctx context.Context, swarmID string) error {
		ver.SwarmConfigID = swarmID
		return s.configs.SetVersionSwarmID(ctx, cfg.ID, ver.Version, swarmID)
	}
}

// addVersion persists a new version and materialises it on the cluster as one
// unit. Shared by AddVersion and RestoreVersion, which differ only in where the
// content comes from.
func (s *ConfigService) addVersion(ctx context.Context, cfg *config.Config, ver *config.ConfigVersion) error {
	res := clusterResource{
		Persist: func(ctx context.Context) error { return s.configs.Update(ctx, cfg, ver) },
		Create:  s.pushVersion(cfg, ver),
		Record:  s.recordVersion(cfg, ver),
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveConfig(ctx, swarmID)
		},
	}
	return res.create(ctx, s.tx, s.orchestrators, cfg.ClusterID, "config "+cfg.Name)
}

func (s *ConfigService) Get(ctx context.Context, id uuid.UUID) (*config.Config, error) {
	return s.configs.FindByID(ctx, id)
}

func (s *ConfigService) List(ctx context.Context, clusterID uuid.UUID, page pagination.Page) ([]*config.Config, int64, error) {
	return s.configs.List(ctx, clusterID, page)
}

// ListVersions returns the full version history (with content) of a config,
// newest first.
func (s *ConfigService) ListVersions(ctx context.Context, id uuid.UUID) ([]*config.ConfigVersion, error) {
	if _, err := s.configs.FindByID(ctx, id); err != nil {
		return nil, err
	}
	return s.configs.ListVersions(ctx, id)
}

// AddVersion stores a new content version, bumps the current version and pushes
// it to the cluster in the same transaction. Without it the cluster would hold
// every version but the current one until the next deployment.
func (s *ConfigService) AddVersion(ctx context.Context, id uuid.UUID, content []byte, comment string, createdBy uuid.UUID) (*config.Config, error) {
	cfg, err := s.configs.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	ver, err := cfg.NewVersion(content, comment, createdBy)
	if err != nil {
		return nil, err
	}
	if err := s.addVersion(ctx, cfg, ver); err != nil {
		return nil, err
	}
	return cfg, nil
}

// VersionDiff is a line-by-line comparison between two config versions.
type VersionDiff struct {
	FromVersion int
	ToVersion   int
	Lines       []linediff.Line
}

// DiffVersions returns the line diff turning version `from` into version `to`.
func (s *ConfigService) DiffVersions(ctx context.Context, id uuid.UUID, from, to int) (*VersionDiff, error) {
	if _, err := s.configs.FindByID(ctx, id); err != nil {
		return nil, err
	}
	versions, err := s.configs.ListVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	fromContent, ok := contentForVersion(versions, from)
	if !ok {
		return nil, fmt.Errorf("%w: v%d", ErrVersionNotFound, from)
	}
	toContent, ok := contentForVersion(versions, to)
	if !ok {
		return nil, fmt.Errorf("%w: v%d", ErrVersionNotFound, to)
	}
	return &VersionDiff{
		FromVersion: from,
		ToVersion:   to,
		Lines:       linediff.Diff(string(fromContent), string(toContent)),
	}, nil
}

// RestoreVersion creates a new version whose content is identical to an earlier
// version (F-V2-08). The comment is mandatory; when blank a default referencing
// the restored version is used so the version history stays self-documenting.
func (s *ConfigService) RestoreVersion(ctx context.Context, id uuid.UUID, version int, comment string, createdBy uuid.UUID) (*config.Config, error) {
	cfg, err := s.configs.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	versions, err := s.configs.ListVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	content, ok := contentForVersion(versions, version)
	if !ok {
		return nil, fmt.Errorf("%w: v%d", ErrVersionNotFound, version)
	}
	if comment == "" {
		comment = fmt.Sprintf("Restauration de la version %d", version)
	}
	ver, err := cfg.NewVersion(content, comment, createdBy)
	if err != nil {
		return nil, err
	}
	if err := s.addVersion(ctx, cfg, ver); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ImpactedService is a service affected by a config change.
type ImpactedService struct {
	ID     uuid.UUID
	Name   string
	Status string
}

// ImpactedServices lists the services that attach a config — those that would
// pick up a new version on their next deployment (F-V2-08).
func (s *ConfigService) ImpactedServices(ctx context.Context, configID uuid.UUID) ([]ImpactedService, error) {
	if _, err := s.configs.FindByID(ctx, configID); err != nil {
		return nil, err
	}
	ids, err := s.services.ServiceIDsByConfigID(ctx, configID)
	if err != nil {
		return nil, err
	}
	out := make([]ImpactedService, 0, len(ids))
	for _, sid := range ids {
		svc, err := s.services.FindByID(ctx, sid)
		if errors.Is(err, domainerrors.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ImpactedService{ID: svc.ID, Name: svc.Name, Status: string(svc.Status)})
	}
	return out, nil
}

func contentForVersion(versions []*config.ConfigVersion, version int) ([]byte, bool) {
	for _, v := range versions {
		if v.Version == version {
			return v.Content, true
		}
	}
	return nil, false
}

// Delete removes a config and every Swarm object its versions own in the same
// transaction, refusing if it is still attached to any service. Leaving the
// cluster objects behind would make a config re-created under the same name
// serve the old content (Swarm addresses configs by name).
func (s *ConfigService) Delete(ctx context.Context, id uuid.UUID) error {
	cfg, err := s.configs.FindByID(ctx, id)
	if err != nil {
		return err
	}
	attached, err := s.configs.IsAttachedToService(ctx, id)
	if err != nil {
		return err
	}
	if attached {
		return config.ErrConfigInUse
	}

	del := clusterDeletion{
		SwarmIDs: func(ctx context.Context) ([]string, error) {
			versions, err := s.configs.ListVersions(ctx, id)
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(versions))
			for _, v := range versions {
				ids = append(ids, v.SwarmConfigID)
			}
			return ids, nil
		},
		Delete: func(ctx context.Context) error { return s.configs.Delete(ctx, id) },
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveConfig(ctx, swarmID)
		},
	}
	return del.run(ctx, s.tx, s.orchestrators, cfg.ClusterID, "config "+cfg.Name)
}

// AttachToService links a config to a service at the given mount path. If
// targetPath is empty, the config's default target path is used.
func (s *ConfigService) AttachToService(ctx context.Context, serviceID, configID uuid.UUID, targetPath string) error {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return err
	}
	cfg, err := s.configs.FindByID(ctx, configID)
	if err != nil {
		return err
	}
	if cfg.ClusterID != svc.ClusterID {
		return ErrClusterMismatch
	}
	if targetPath == "" {
		targetPath = cfg.TargetPath
	}
	return s.services.AttachConfig(ctx, serviceID, configID, targetPath)
}

func (s *ConfigService) DetachFromService(ctx context.Context, serviceID, configID uuid.UUID) error {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return err
	}
	return s.services.DetachConfig(ctx, serviceID, configID)
}

// ListServiceConfigs returns the configs attached to a service, each paired
// with the mount path chosen for that service.
func (s *ConfigService) ListServiceConfigs(ctx context.Context, serviceID uuid.UUID) ([]ServiceConfig, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, err
	}
	attachments, err := s.services.GetConfigAttachments(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	out := make([]ServiceConfig, 0, len(attachments))
	for _, a := range attachments {
		cfg, err := s.configs.FindByID(ctx, a.ConfigID)
		if errors.Is(err, domainerrors.ErrNotFound) {
			continue // tolerate a dangling attachment row
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ServiceConfig{Config: cfg, TargetPath: a.TargetPath})
	}
	return out, nil
}
