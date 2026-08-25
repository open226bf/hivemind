package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// SecretService manages write-only secrets and their attachment to services
// (F-MVP-06). Secret values are encrypted at rest by the repository and never
// returned by any read path.
type SecretService struct {
	secrets       ports.SecretRepository
	services      ports.ServiceRepository
	orchestrators ports.OrchestratorRegistry
	tx            ports.TxManager
}

// NewSecretService wires the secret use case. orchestrators and tx may be nil
// (tests, or a deployment with no orchestrator): writes then only touch the
// database, as they did before secrets were pushed to the cluster up front.
func NewSecretService(
	secrets ports.SecretRepository,
	services ports.ServiceRepository,
	orchestrators ports.OrchestratorRegistry,
	tx ports.TxManager,
) *SecretService {
	return &SecretService{secrets: secrets, services: services, orchestrators: orchestrators, tx: tx}
}

type CreateSecretInput struct {
	Name       string
	TargetPath string
	Value      []byte
	CreatedBy  uuid.UUID
	// Cluster is the target cluster id. Empty selects the default cluster.
	Cluster uuid.UUID
}

// ServiceSecret pairs an attached secret with the mount path chosen for a
// specific service.
type ServiceSecret struct {
	Secret     *secret.Secret
	TargetPath string
}

// Create stores a new secret with its first (encrypted) version and pushes that
// version to the target cluster in the same transaction: either the secret
// exists on both sides, or on neither.
func (s *SecretService) Create(ctx context.Context, in CreateSecretInput) (*secret.Secret, error) {
	sec, ver, err := secret.New(in.Name, in.TargetPath, in.Value, in.CreatedBy)
	if err != nil {
		return nil, err
	}
	sec.ClusterID = in.Cluster

	res := clusterResource{
		Persist: func(ctx context.Context) error { return s.secrets.Save(ctx, sec, ver, in.Value) },
		Create:  s.pushVersion(sec, in.Value),
		Record:  s.recordVersion(sec, ver),
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveSecret(ctx, swarmID)
		},
	}
	if err := res.create(ctx, s.tx, s.orchestrators, sec.ClusterID, "secret "+sec.Name); err != nil {
		return nil, err
	}
	return sec, nil
}

// pushVersion creates the secret's current version on the cluster. Swarm
// secrets are immutable, so every version is its own object, named
// <secret>_v<n> — exactly the name the deployment engine looks for.
func (s *SecretService) pushVersion(sec *secret.Secret, value []byte) func(context.Context, ports.Orchestrator) (string, error) {
	return func(ctx context.Context, orch ports.Orchestrator) (string, error) {
		return orch.CreateSecret(ctx, sec.SwarmSecretName(), value)
	}
}

func (s *SecretService) recordVersion(sec *secret.Secret, ver *secret.SecretVersion) func(context.Context, string) error {
	return func(ctx context.Context, swarmID string) error {
		ver.SwarmSecretID = swarmID
		return s.secrets.SetVersionSwarmID(ctx, sec.ID, ver.Version, swarmID)
	}
}

func (s *SecretService) Get(ctx context.Context, id uuid.UUID) (*secret.Secret, error) {
	return s.secrets.FindByID(ctx, id)
}

func (s *SecretService) List(ctx context.Context, clusterID uuid.UUID, page pagination.Page) ([]*secret.Secret, int64, error) {
	return s.secrets.List(ctx, clusterID, page)
}

// Rotate stores a new encrypted version, bumps the current version counter and
// pushes the new version to the cluster — same transaction, same guarantee as
// Create. Without it the cluster would hold every version but the current one
// until the next deployment.
func (s *SecretService) Rotate(ctx context.Context, id uuid.UUID, newValue []byte) (*secret.Secret, error) {
	if len(newValue) == 0 {
		return nil, secret.ErrEmptyValue
	}
	sec, err := s.secrets.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	ver := sec.Rotate(newValue)

	res := clusterResource{
		Persist: func(ctx context.Context) error { return s.secrets.Update(ctx, sec, ver, newValue) },
		Create:  s.pushVersion(sec, newValue),
		Record:  s.recordVersion(sec, ver),
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveSecret(ctx, swarmID)
		},
	}
	if err := res.create(ctx, s.tx, s.orchestrators, sec.ClusterID, "secret "+sec.Name); err != nil {
		return nil, err
	}
	return sec, nil
}

// Delete removes a secret and every Swarm object its versions own in the same
// transaction, refusing if it is still attached to any service. Leaving the
// cluster objects behind would make a secret re-created under the same name
// resolve to the old value (Swarm addresses secrets by name).
func (s *SecretService) Delete(ctx context.Context, id uuid.UUID) error {
	sec, err := s.secrets.FindByID(ctx, id)
	if err != nil {
		return err
	}
	attached, err := s.secrets.IsAttachedToService(ctx, id)
	if err != nil {
		return err
	}
	if attached {
		return secret.ErrSecretInUse
	}

	del := clusterDeletion{
		SwarmIDs: func(ctx context.Context) ([]string, error) {
			return s.secrets.ListVersionSwarmIDs(ctx, id)
		},
		Delete: func(ctx context.Context) error { return s.secrets.Delete(ctx, id) },
		Remove: func(ctx context.Context, orch ports.Orchestrator, swarmID string) error {
			return orch.RemoveSecret(ctx, swarmID)
		},
	}
	return del.run(ctx, s.tx, s.orchestrators, sec.ClusterID, "secret "+sec.Name)
}

// AttachToService links a secret to a service at the given mount path. If
// targetPath is empty, the secret's default target path is used.
func (s *SecretService) AttachToService(ctx context.Context, serviceID, secretID uuid.UUID, targetPath string) error {
	svc, err := s.services.FindByID(ctx, serviceID)
	if err != nil {
		return err
	}
	sec, err := s.secrets.FindByID(ctx, secretID)
	if err != nil {
		return err
	}
	if sec.ClusterID != svc.ClusterID {
		return ErrClusterMismatch
	}
	if targetPath == "" {
		targetPath = sec.TargetPath
	}
	return s.services.AttachSecret(ctx, serviceID, secretID, targetPath)
}

func (s *SecretService) DetachFromService(ctx context.Context, serviceID, secretID uuid.UUID) error {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return err
	}
	return s.services.DetachSecret(ctx, serviceID, secretID)
}

// ListServiceSecrets returns the secrets attached to a service, each paired
// with the mount path chosen for that service.
func (s *SecretService) ListServiceSecrets(ctx context.Context, serviceID uuid.UUID) ([]ServiceSecret, error) {
	if _, err := s.services.FindByID(ctx, serviceID); err != nil {
		return nil, err
	}
	attachments, err := s.services.GetSecretAttachments(ctx, serviceID)
	if err != nil {
		return nil, err
	}

	out := make([]ServiceSecret, 0, len(attachments))
	for _, a := range attachments {
		sec, err := s.secrets.FindByID(ctx, a.SecretID)
		if errors.Is(err, domainerrors.ErrNotFound) {
			continue // tolerate a dangling attachment row
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ServiceSecret{Secret: sec, TargetPath: a.TargetPath})
	}
	return out, nil
}
