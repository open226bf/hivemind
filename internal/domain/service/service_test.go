package service_test

import (
	"testing"

	"github.com/orange/hivemind/internal/domain/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ValidName(t *testing.T) {
	cases := []string{"my-api", "wallet-api", "a", "ab", "a1b2c3"}
	for _, name := range cases {
		s, err := service.New(name, "nginx", "latest", 1)
		require.NoError(t, err, "name=%s", name)
		assert.Equal(t, name, s.Name)
		assert.Equal(t, service.StatusDraft, s.Status)
	}
}

func TestNew_InvalidName(t *testing.T) {
	cases := []string{"", "My-API", "my_api", "-api", "api-", "a" + string(make([]byte, 63))}
	for _, name := range cases {
		_, err := service.New(name, "nginx", "latest", 1)
		assert.ErrorIs(t, err, service.ErrInvalidName, "expected error for name=%q", name)
	}
}

func TestFullImage(t *testing.T) {
	s, _ := service.New("my-api", "registry.example.com/my-api", "v1.0.0", 2)
	assert.Equal(t, "registry.example.com/my-api:v1.0.0", s.FullImage())
}

func TestFullImage_NoTag(t *testing.T) {
	s, _ := service.New("my-api", "nginx", "", 1)
	assert.Equal(t, "nginx", s.FullImage())
}

func TestSetResources_Valid(t *testing.T) {
	s, _ := service.New("api", "nginx", "latest", 1)
	err := s.SetResources(service.Resources{
		CPUReservation: 0.25,
		CPULimit:       0.5,
		MemReservation: 128 * 1024 * 1024,
		MemLimit:       256 * 1024 * 1024,
	})
	require.NoError(t, err)
}

func TestSetResources_LimitBelowReservation(t *testing.T) {
	s, _ := service.New("api", "nginx", "latest", 1)
	err := s.SetResources(service.Resources{
		CPUReservation: 1.0,
		CPULimit:       0.5,
	})
	assert.ErrorIs(t, err, service.ErrResourceConflict)
}

func TestSetPlacement_Valid(t *testing.T) {
	s, _ := service.New("api", "nginx", "latest", 1)
	err := s.SetPlacement(service.Placement{
		Constraints: []string{"node.role==worker", "node.labels.zone!=dmz"},
		Preferences: []string{"node.labels.zone"},
		MaxReplicas: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, uint64(2), s.Placement.MaxReplicas)
}

func TestSetPlacement_Empty(t *testing.T) {
	s, _ := service.New("api", "nginx", "latest", 1)
	require.NoError(t, s.SetPlacement(service.Placement{}))
}

func TestSetPlacement_InvalidConstraint(t *testing.T) {
	cases := []string{"node.role", "==worker", "node.role=worker", "  "}
	for _, c := range cases {
		s, _ := service.New("api", "nginx", "latest", 1)
		err := s.SetPlacement(service.Placement{Constraints: []string{c}})
		assert.ErrorIs(t, err, service.ErrInvalidConstraint, "constraint=%q", c)
	}
}

func TestSetPlacement_EmptyPreference(t *testing.T) {
	s, _ := service.New("api", "nginx", "latest", 1)
	err := s.SetPlacement(service.Placement{Preferences: []string{"  "}})
	assert.ErrorIs(t, err, service.ErrInvalidPreference)
}

func TestDefaultUpdateConfig(t *testing.T) {
	cfg := service.DefaultUpdateConfig()
	assert.Equal(t, uint64(1), cfg.Parallelism)
	assert.Equal(t, "rollback", cfg.FailureAction)
	assert.Equal(t, "start-first", cfg.Order)
}

func TestUpdateConfigOverlay_PreservesDefaults(t *testing.T) {
	base := service.DefaultUpdateConfig()
	// Only override parallelism — everything else must survive.
	got := base.Overlay(service.UpdateConfig{Parallelism: 5})

	assert.Equal(t, uint64(5), got.Parallelism)
	assert.Equal(t, base.Delay, got.Delay)
	assert.Equal(t, "rollback", got.FailureAction)
	assert.Equal(t, base.Monitor, got.Monitor)
	assert.Equal(t, "start-first", got.Order)
}

func TestUpdateConfigOverlay_ReplacesNonZero(t *testing.T) {
	base := service.DefaultUpdateConfig()
	got := base.Overlay(service.UpdateConfig{
		FailureAction: "pause",
		Order:         "stop-first",
	})

	assert.Equal(t, "pause", got.FailureAction)
	assert.Equal(t, "stop-first", got.Order)
	assert.Equal(t, uint64(1), got.Parallelism) // untouched
}
