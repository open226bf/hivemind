package secret_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ChecksumNotEmpty(t *testing.T) {
	s, v, err := secret.New("db_password", "/run/secrets/db_password", []byte("s3cr3t"), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, s)
	require.NotNil(t, v)
	assert.NotEmpty(t, s.Checksum)
	assert.Equal(t, s.Checksum, v.Checksum)
	assert.Equal(t, 1, s.CurrentVersion)
}

func TestNew_InvalidName(t *testing.T) {
	_, _, err := secret.New("bad name!", "/run/secrets/x", []byte("v"), uuid.New())
	assert.ErrorIs(t, err, secret.ErrInvalidName)
}

func TestNew_EmptyValue(t *testing.T) {
	_, _, err := secret.New("db_password", "/run/secrets/x", nil, uuid.New())
	assert.ErrorIs(t, err, secret.ErrEmptyValue)
}

func TestRotate_IncrementsVersion(t *testing.T) {
	s, _, err := secret.New("db_password", "/run/secrets/db_password", []byte("old"), uuid.New())
	require.NoError(t, err)
	v2 := s.Rotate([]byte("new"))
	assert.Equal(t, 2, s.CurrentVersion)
	assert.Equal(t, 2, v2.Version)
	assert.NotEqual(t, v2.Checksum, "")
}

func TestSwarmSecretName(t *testing.T) {
	s, _, err := secret.New("db_password", "/run/secrets/db_password", []byte("x"), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "db_password_v1", s.SwarmSecretName())
	s.Rotate([]byte("y"))
	assert.Equal(t, "db_password_v2", s.SwarmSecretName())
}
