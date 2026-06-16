package template_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/domain/template"
)

func validSpec() template.Spec {
	return template.Spec{Image: "nginx", Tag: "1.25", Replicas: 2}
}

func TestNew_Defaults(t *testing.T) {
	tmpl, err := template.New("java-api", "API Java", validSpec(), nil, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 1, tmpl.Version)
	// Update strategy is defaulted when omitted.
	assert.Equal(t, "rollback", tmpl.Spec.UpdateConfig.FailureAction)
}

func TestNew_InvalidName(t *testing.T) {
	_, err := template.New("Bad Name", "", validSpec(), nil, uuid.New())
	assert.ErrorIs(t, err, template.ErrInvalidName)
}

func TestNew_ImageRequired(t *testing.T) {
	_, err := template.New("api", "", template.Spec{}, nil, uuid.New())
	assert.ErrorIs(t, err, template.ErrInvalidImage)
}

func TestNew_InvalidLockedField(t *testing.T) {
	_, err := template.New("api", "", validSpec(), []string{"nonsense"}, uuid.New())
	assert.ErrorIs(t, err, template.ErrInvalidLock)
}

func TestNew_PropagatesResourceValidation(t *testing.T) {
	spec := validSpec()
	spec.Resources = service.Resources{CPUReservation: 1, CPULimit: 0.5} // limit < reservation
	_, err := template.New("api", "", spec, nil, uuid.New())
	assert.ErrorIs(t, err, service.ErrResourceConflict)
}

func TestUpdate_BumpsVersion(t *testing.T) {
	tmpl, _ := template.New("api", "", validSpec(), nil, uuid.New())
	spec := validSpec()
	spec.Replicas = 5
	require.NoError(t, tmpl.Update("new desc", spec, []string{"resources"}))
	assert.Equal(t, 2, tmpl.Version)
	assert.Equal(t, uint64(5), tmpl.Spec.Replicas)
	assert.True(t, tmpl.IsLocked("resources"))
}

func TestIsLocked(t *testing.T) {
	tmpl, _ := template.New("api", "", validSpec(), []string{"resources", "image"}, uuid.New())
	assert.True(t, tmpl.IsLocked("image"))
	assert.False(t, tmpl.IsLocked("tag"))
}
