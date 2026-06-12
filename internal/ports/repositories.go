package ports

import (
	"context"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/pkg/pagination"
)

type UserRepository interface {
	Save(ctx context.Context, u *user.User) error
	FindByID(ctx context.Context, id uuid.UUID) (*user.User, error)
	FindByEmail(ctx context.Context, email string) (*user.User, error)
	List(ctx context.Context, p pagination.Page) ([]*user.User, int64, error)
	Update(ctx context.Context, u *user.User) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type ServiceRepository interface {
	Save(ctx context.Context, s *service.Service) error
	FindByID(ctx context.Context, id uuid.UUID) (*service.Service, error)
	FindByName(ctx context.Context, name string) (*service.Service, error)
	List(ctx context.Context, filter ServiceFilter, p pagination.Page) ([]*service.Service, int64, error)
	Update(ctx context.Context, s *service.Service) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type ServiceFilter struct {
	Name   string
	Status string
}

type DeploymentRepository interface {
	Save(ctx context.Context, d *deployment.Deployment) error
	FindByID(ctx context.Context, id uuid.UUID) (*deployment.Deployment, error)
	FindActiveByServiceID(ctx context.Context, serviceID uuid.UUID) (*deployment.Deployment, error)
	ListByServiceID(ctx context.Context, serviceID uuid.UUID, p pagination.Page) ([]*deployment.Deployment, int64, error)
	List(ctx context.Context, filter DeploymentFilter, p pagination.Page) ([]*deployment.Deployment, int64, error)
	Update(ctx context.Context, d *deployment.Deployment) error
}

type DeploymentFilter struct {
	ServiceID *uuid.UUID
	Status    string
	From, To  *string
}

type SecretRepository interface {
	Save(ctx context.Context, s *secret.Secret, v *secret.SecretVersion) error
	FindByID(ctx context.Context, id uuid.UUID) (*secret.Secret, error)
	List(ctx context.Context, p pagination.Page) ([]*secret.Secret, int64, error)
	Update(ctx context.Context, s *secret.Secret, newVersion *secret.SecretVersion) error
	Delete(ctx context.Context, id uuid.UUID) error
	IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error)
}

type NetworkRepository interface {
	Save(ctx context.Context, n *network.Network) error
	FindByID(ctx context.Context, id uuid.UUID) (*network.Network, error)
	List(ctx context.Context, p pagination.Page) ([]*network.Network, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
	IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error)
}

type ConfigRepository interface {
	Save(ctx context.Context, c *config.Config, v *config.ConfigVersion) error
	FindByID(ctx context.Context, id uuid.UUID) (*config.Config, error)
	ListVersions(ctx context.Context, configID uuid.UUID) ([]*config.ConfigVersion, error)
	List(ctx context.Context, p pagination.Page) ([]*config.Config, int64, error)
	Update(ctx context.Context, c *config.Config, newVersion *config.ConfigVersion) error
	Delete(ctx context.Context, id uuid.UUID) error
	IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error)
}
