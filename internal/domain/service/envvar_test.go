package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/orange/hivemind/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEnvVar_ValidKey(t *testing.T) {
	cases := []string{"KEY", "MY_VAR", "DB_HOST", "_PRIVATE", "A1"}
	for _, k := range cases {
		v, err := service.NewEnvVar(uuid.New(), k, "val", false)
		require.NoError(t, err, "key=%s", k)
		assert.Equal(t, k, v.Key)
	}
}

func TestNewEnvVar_InvalidKey(t *testing.T) {
	cases := []string{"", "lowercase", "1DIGIT_FIRST", "KEY-DASH", "KEY SPACE"}
	for _, k := range cases {
		_, err := service.NewEnvVar(uuid.New(), k, "val", false)
		assert.ErrorIs(t, err, service.ErrInvalidEnvKey, "key=%q", k)
	}
}

func TestValidateEnvVars_Duplicate(t *testing.T) {
	vars := []service.EnvVar{
		{Key: "FOO", Value: "a"},
		{Key: "FOO", Value: "b"},
	}
	err := service.ValidateEnvVars(vars)
	assert.ErrorIs(t, err, service.ErrDuplicateKey)
}

func TestValidateEnvVars_Valid(t *testing.T) {
	vars := []service.EnvVar{
		{Key: "FOO", Value: "a"},
		{Key: "BAR", Value: "b"},
	}
	assert.NoError(t, service.ValidateEnvVars(vars))
}
