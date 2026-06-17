package secret

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSecretInUse      = errors.New("secret is attached to one or more services")
	ErrValueNotReadable = errors.New("secret value is write-only")
	ErrInvalidName      = errors.New("secret name must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$")
	ErrEmptyValue       = errors.New("secret value must not be empty")
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type Secret struct {
	ID             uuid.UUID
	ClusterID      uuid.UUID // orchestration target; zero value = the default cluster
	Name           string
	CurrentVersion int
	TargetPath     string
	Checksum       string
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type SecretVersion struct {
	ID            uuid.UUID
	SecretID      uuid.UUID
	Version       int
	SwarmSecretID string
	Checksum      string
	CreatedAt     time.Time
}

func New(name, targetPath string, value []byte, createdBy uuid.UUID) (*Secret, *SecretVersion, error) {
	if !nameRegex.MatchString(name) {
		return nil, nil, ErrInvalidName
	}
	if len(value) == 0 {
		return nil, nil, ErrEmptyValue
	}
	id := uuid.New()
	checksum := computeChecksum(value)
	s := &Secret{
		ID:             id,
		Name:           name,
		CurrentVersion: 1,
		TargetPath:     targetPath,
		Checksum:       checksum,
		CreatedBy:      createdBy,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	v := &SecretVersion{
		ID:        uuid.New(),
		SecretID:  id,
		Version:   1,
		Checksum:  checksum,
		CreatedAt: time.Now().UTC(),
	}
	return s, v, nil
}

func (s *Secret) Rotate(newValue []byte) *SecretVersion {
	s.CurrentVersion++
	s.Checksum = computeChecksum(newValue)
	s.UpdatedAt = time.Now().UTC()
	return &SecretVersion{
		ID:        uuid.New(),
		SecretID:  s.ID,
		Version:   s.CurrentVersion,
		Checksum:  s.Checksum,
		CreatedAt: time.Now().UTC(),
	}
}

// SwarmSecretName returns the versioned Swarm secret name (e.g. db_password_v2).
func (s *Secret) SwarmSecretName() string {
	return fmt.Sprintf("%s_v%d", s.Name, s.CurrentVersion)
}

func computeChecksum(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
