package secret_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ChecksumNotEmpty(t *testing.T) {
	s, v := secret.New("db_password", "/run/secrets/db_password", []byte("s3cr3t"), uuid.New())
	require.NotNil(t, s)
	require.NotNil(t, v)
	assert.NotEmpty(t, s.Checksum)
	assert.Equal(t, s.Checksum, v.Checksum)
	assert.Equal(t, 1, s.CurrentVersion)
}

func TestRotate_IncrementsVersion(t *testing.T) {
	s, _ := secret.New("db_password", "/run/secrets/db_password", []byte("old"), uuid.New())
	v2 := s.Rotate([]byte("new"))
	assert.Equal(t, 2, s.CurrentVersion)
	assert.Equal(t, 2, v2.Version)
	assert.NotEqual(t, v2.Checksum, "")
}

func TestSwarmSecretName(t *testing.T) {
	s, _ := secret.New("db_password", "/run/secrets/db_password", []byte("x"), uuid.New())
	assert.Equal(t, "db_password_v1", s.SwarmSecretName())
	s.Rotate([]byte("y"))
	assert.Equal(t, "db_password_v2", s.SwarmSecretName())
}
