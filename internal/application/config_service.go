package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/config"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// ConfigService manages versioned, cleartext configuration files and their
// attachment to services (F-MVP-07). Unlike secrets, config content is
// readable through the API.
type ConfigService struct {
	configs  ports.ConfigRepository
	services ports.ServiceRepository
}

func NewConfigService(configs ports.ConfigRepository, services ports.ServiceRepository) *ConfigService {
	return &ConfigService{configs: configs, services: services}
}

type CreateConfigInput struct {
	Name       string
	TargetPath string
	Content    []byte
	Comment    string
	CreatedBy  uuid.UUID
}

// ServiceConfig pairs an attached config with the mount path chosen for a
// specific service.
type ServiceConfig struct {
	Config     *config.Config
	TargetPath string
}

// Create stores a new config with its first version.
func (s *ConfigService) Create(ctx context.Context, in CreateConfigInput) (*config.Config, error) {
	cfg, ver, err := config.New(in.Name, in.TargetPath, in.Content, in.Comment, in.CreatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.configs.Save(ctx, cfg, ver); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *ConfigService) Get(ctx context.Context, id uuid.UUID) (*config.Config, error) {
	return s.configs.FindByID(ctx, id)
}

func (s *ConfigService) List(ctx context.Context, page pagination.Page) ([]*config.Config, int64, error) {
	return s.configs.List(ctx, page)
}

// ListVersions returns the full version history (with content) of a config,
// newest first.
func (s *ConfigService) ListVersions(ctx context.Context, id uuid.UUID) ([]*config.ConfigVersion, error) {
	if _, err := s.configs.FindByID(ctx, id); err != nil {
		return nil, err
	}
	return s.configs.ListVersions(ctx, id)
}

// AddVersion stores a new content version and bumps the current version.
func (s *ConfigService) AddVersion(ctx context.Context, id uuid.UUID, content []byte, comment string, createdBy uuid.UUID) (*config.Config, error) {
	cfg, err := s.configs.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	ver, err := cfg.NewVersion(content, comment, createdBy)
	if err != nil {
		return nil, err
	}
	if err := s.configs.Update(ctx, cfg, ver); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Delete removes a config, refusing if it is still attached to any service.
func (s *ConfigService) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.configs.FindByID(ctx, id); err != nil {
		return err
	}
	attached, err := s.configs.IsAttachedToService(ctx, id)
	if err != nil {
		return err
	}
	if attached {
		return config.ErrConfigInUse
	}
	return s.configs.Delete(ctx, id)
}

// AttachToService links a config to a service at the given mount path. If
// targetPath is empty, the config's default target path is used.
func (s *ConfigService) AttachToService(ctx context.Context, serviceID, configID uuid.UUID, targetPath string) error {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return err
	}
	cfg, err := s.configs.FindByID(ctx, configID)
	if err != nil {
		return err
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
