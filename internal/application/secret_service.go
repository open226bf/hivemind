package application

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/secret"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
)

// SecretService manages write-only secrets and their attachment to services
// (F-MVP-06). Secret values are encrypted at rest by the repository and never
// returned by any read path.
type SecretService struct {
	secrets  ports.SecretRepository
	services ports.ServiceRepository
}

func NewSecretService(secrets ports.SecretRepository, services ports.ServiceRepository) *SecretService {
	return &SecretService{secrets: secrets, services: services}
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

// Create stores a new secret and its first (encrypted) version.
func (s *SecretService) Create(ctx context.Context, in CreateSecretInput) (*secret.Secret, error) {
	sec, ver, err := secret.New(in.Name, in.TargetPath, in.Value, in.CreatedBy)
	if err != nil {
		return nil, err
	}
	sec.ClusterID = in.Cluster
	if err := s.secrets.Save(ctx, sec, ver, in.Value); err != nil {
		return nil, err
	}
	return sec, nil
}

func (s *SecretService) Get(ctx context.Context, id uuid.UUID) (*secret.Secret, error) {
	return s.secrets.FindByID(ctx, id)
}

func (s *SecretService) List(ctx context.Context, clusterID uuid.UUID, page pagination.Page) ([]*secret.Secret, int64, error) {
	return s.secrets.List(ctx, clusterID, page)
}

// Rotate stores a new encrypted version and bumps the current version counter.
func (s *SecretService) Rotate(ctx context.Context, id uuid.UUID, newValue []byte) (*secret.Secret, error) {
	if len(newValue) == 0 {
		return nil, secret.ErrEmptyValue
	}
	sec, err := s.secrets.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	ver := sec.Rotate(newValue)
	if err := s.secrets.Update(ctx, sec, ver, newValue); err != nil {
		return nil, err
	}
	return sec, nil
}

// Delete removes a secret, refusing if it is still attached to any service.
func (s *SecretService) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.secrets.FindByID(ctx, id); err != nil {
		return err
	}
	attached, err := s.secrets.IsAttachedToService(ctx, id)
	if err != nil {
		return err
	}
	if attached {
		return secret.ErrSecretInUse
	}
	return s.secrets.Delete(ctx, id)
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
