package ports

import (
	"context"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/auditlog"
	"github.com/open226bf/hivemind/internal/domain/cluster"
	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/hive"
	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/snapshot"
	"github.com/open226bf/hivemind/internal/domain/template"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/domain/volume"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Cluster ─────────────────────────────────────────────────────────────────

type ClusterRepository interface {
	Save(ctx context.Context, c *cluster.Cluster) error
	FindByID(ctx context.Context, id uuid.UUID) (*cluster.Cluster, error)
	FindByName(ctx context.Context, name string) (*cluster.Cluster, error)
	// FindByAgentID resolves the cluster an enrolled agent is bound to.
	FindByAgentID(ctx context.Context, agentID string) (*cluster.Cluster, error)
	// FindByEnrollmentTokenHash resolves the cluster awaiting enrollment with the
	// given token hash (used to enroll an agent that only knows the token).
	FindByEnrollmentTokenHash(ctx context.Context, tokenHash string) (*cluster.Cluster, error)
	// FindDefault returns the cluster flagged as default (ErrNotFound if none
	// has been seeded yet).
	FindDefault(ctx context.Context) (*cluster.Cluster, error)
	List(ctx context.Context, p pagination.Page) ([]*cluster.Cluster, int64, error)
	Update(ctx context.Context, c *cluster.Cluster) error
	Delete(ctx context.Context, id uuid.UUID) error
	// ClearDefault unsets the default flag on every cluster — used to enforce a
	// single default when promoting another one.
	ClearDefault(ctx context.Context) error
}

// ─── User ────────────────────────────────────────────────────────────────────

type UserRepository interface {
	Save(ctx context.Context, u *user.User) error
	FindByID(ctx context.Context, id uuid.UUID) (*user.User, error)
	FindByEmail(ctx context.Context, email string) (*user.User, error)
	List(ctx context.Context, p pagination.Page) ([]*user.User, int64, error)
	Update(ctx context.Context, u *user.User) error
	Delete(ctx context.Context, id uuid.UUID) error
	// CountActiveAdmins returns the number of active users with the admin role.
	// Used to enforce the "last admin" invariant (F-V1-01).
	CountActiveAdmins(ctx context.Context) (int64, error)
}

// ─── Service ─────────────────────────────────────────────────────────────────

type ServiceRepository interface {
	Save(ctx context.Context, s *service.Service) error
	FindByID(ctx context.Context, id uuid.UUID) (*service.Service, error)
	FindByName(ctx context.Context, name string) (*service.Service, error)
	List(ctx context.Context, filter ServiceFilter, p pagination.Page) ([]*service.Service, int64, error)
	Update(ctx context.Context, s *service.Service) error
	Delete(ctx context.Context, id uuid.UUID) error

	// Env vars (atomic replacement)
	SetEnvVars(ctx context.Context, serviceID uuid.UUID, vars []service.EnvVar) error
	GetEnvVars(ctx context.Context, serviceID uuid.UUID) ([]service.EnvVar, error)

	// Network attachments
	AttachNetwork(ctx context.Context, serviceID, networkID uuid.UUID) error
	DetachNetwork(ctx context.Context, serviceID, networkID uuid.UUID) error
	GetNetworkIDs(ctx context.Context, serviceID uuid.UUID) ([]uuid.UUID, error)

	// Secret attachments
	AttachSecret(ctx context.Context, serviceID, secretID uuid.UUID, targetPath string) error
	DetachSecret(ctx context.Context, serviceID, secretID uuid.UUID) error
	GetSecretAttachments(ctx context.Context, serviceID uuid.UUID) ([]ServiceSecretAttachment, error)

	// Config attachments
	AttachConfig(ctx context.Context, serviceID, configID uuid.UUID, targetPath string) error
	DetachConfig(ctx context.Context, serviceID, configID uuid.UUID) error
	GetConfigAttachments(ctx context.Context, serviceID uuid.UUID) ([]ServiceConfigAttachment, error)
	// ServiceIDsByConfigID returns the IDs of services that attach a given config
	// — the "impacted services" of a config change (F-V2-08).
	ServiceIDsByConfigID(ctx context.Context, configID uuid.UUID) ([]uuid.UUID, error)

	// Mounts (atomic replacement, F-V2-06)
	SetMounts(ctx context.Context, serviceID uuid.UUID, mounts []volume.Mount) error
	GetMounts(ctx context.Context, serviceID uuid.UUID) ([]volume.Mount, error)

	// Published ports (atomic replacement)
	SetPorts(ctx context.Context, serviceID uuid.UUID, ports []service.Port) error
	GetPorts(ctx context.Context, serviceID uuid.UUID) ([]service.Port, error)
	// CountMountsByVolumeName returns how many service mounts reference a named
	// volume — used to refuse deletion of an in-use volume.
	CountMountsByVolumeName(ctx context.Context, name string) (int64, error)

	// CountServicesByHive counts the services assigned to a hive — used to refuse
	// deletion of a non-empty hive (project).
	CountServicesByHive(ctx context.Context, hiveID uuid.UUID) (int64, error)

	// CountServicesByCluster counts the services targeting a cluster — used to
	// refuse deletion of a non-empty cluster.
	CountServicesByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
}

type ServiceFilter struct {
	Name   string
	Status string
	// HiveID filters by project. When set, only services of that hive are
	// returned; when Unassigned is true, only services without a hive.
	HiveID     *uuid.UUID
	Unassigned bool
	// ClusterID filters by orchestration target. Nil = all clusters.
	ClusterID *uuid.UUID
}

type ServiceSecretAttachment struct {
	SecretID   uuid.UUID
	TargetPath string
}

type ServiceConfigAttachment struct {
	ConfigID   uuid.UUID
	TargetPath string
}

// ─── Deployment ───────────────────────────────────────────────────────────────

type DeploymentRepository interface {
	Save(ctx context.Context, d *deployment.Deployment) error
	FindByID(ctx context.Context, id uuid.UUID) (*deployment.Deployment, error)
	FindActiveByServiceID(ctx context.Context, serviceID uuid.UUID) (*deployment.Deployment, error)
	ListByServiceID(ctx context.Context, serviceID uuid.UUID, p pagination.Page) ([]*deployment.Deployment, int64, error)
	List(ctx context.Context, filter DeploymentFilter, p pagination.Page) ([]*deployment.Deployment, int64, error)
	Update(ctx context.Context, d *deployment.Deployment) error
	FailOrphaned(ctx context.Context) (int64, error)
}

type DeploymentFilter struct {
	ServiceID *uuid.UUID
	Status    string
	From, To  *string
}

// ─── Secret ───────────────────────────────────────────────────────────────────

type SecretRepository interface {
	// Save persists a new secret and its first version. The plaintext value is
	// encrypted at rest by the adapter and never returned by any read method.
	Save(ctx context.Context, s *secret.Secret, v *secret.SecretVersion, value []byte) error
	FindByID(ctx context.Context, id uuid.UUID) (*secret.Secret, error)
	List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*secret.Secret, int64, error)
	// Update rotates a secret: bumps the parent record and stores the new
	// encrypted version value.
	Update(ctx context.Context, s *secret.Secret, newVersion *secret.SecretVersion, value []byte) error
	Delete(ctx context.Context, id uuid.UUID) error
	IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error)
	// GetValue returns the decrypted value of the secret's current version.
	// Server-side only (used by the deployment engine to push the value to
	// the orchestrator); never exposed through the API.
	GetValue(ctx context.Context, id uuid.UUID) ([]byte, error)
}

// ─── Network ──────────────────────────────────────────────────────────────────

type NetworkRepository interface {
	Save(ctx context.Context, n *network.Network) error
	FindByID(ctx context.Context, id uuid.UUID) (*network.Network, error)
	List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*network.Network, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
	IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error)
}

// ─── Volume ───────────────────────────────────────────────────────────────────

type VolumeRepository interface {
	Save(ctx context.Context, v *volume.Volume) error
	FindByID(ctx context.Context, id uuid.UUID) (*volume.Volume, error)
	FindByName(ctx context.Context, name string) (*volume.Volume, error)
	List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*volume.Volume, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// ─── Hive (project) ───────────────────────────────────────────────────────────

type HiveRepository interface {
	Save(ctx context.Context, h *hive.Hive) error
	FindByID(ctx context.Context, id uuid.UUID) (*hive.Hive, error)
	List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*hive.Hive, int64, error)
	Update(ctx context.Context, h *hive.Hive) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// ─── Template ─────────────────────────────────────────────────────────────────

type TemplateRepository interface {
	Save(ctx context.Context, t *template.Template) error
	FindByID(ctx context.Context, id uuid.UUID) (*template.Template, error)
	List(ctx context.Context, p pagination.Page) ([]*template.Template, int64, error)
	Update(ctx context.Context, t *template.Template) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// ─── Config ───────────────────────────────────────────────────────────────────

type ConfigRepository interface {
	Save(ctx context.Context, c *config.Config, v *config.ConfigVersion) error
	FindByID(ctx context.Context, id uuid.UUID) (*config.Config, error)
	ListVersions(ctx context.Context, configID uuid.UUID) ([]*config.ConfigVersion, error)
	List(ctx context.Context, clusterID uuid.UUID, p pagination.Page) ([]*config.Config, int64, error)
	Update(ctx context.Context, c *config.Config, newVersion *config.ConfigVersion) error
	Delete(ctx context.Context, id uuid.UUID) error
	IsAttachedToService(ctx context.Context, id uuid.UUID) (bool, error)
}

// ─── ServiceSnapshot ──────────────────────────────────────────────────────────

type SnapshotRepository interface {
	Save(ctx context.Context, s *snapshot.ServiceSnapshot) error
	FindByID(ctx context.Context, id uuid.UUID) (*snapshot.ServiceSnapshot, error)
	ListByServiceID(ctx context.Context, serviceID uuid.UUID, p pagination.Page) ([]*snapshot.ServiceSnapshot, int64, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// ─── AuditLog ─────────────────────────────────────────────────────────────────

type AuditLogRepository interface {
	Save(ctx context.Context, log *auditlog.AuditLog) error
	List(ctx context.Context, filter AuditLogFilter, p pagination.Page) ([]*auditlog.AuditLog, int64, error)
}

type AuditLogFilter struct {
	UserID       *uuid.UUID
	ResourceType string
	From, To     *string
}
