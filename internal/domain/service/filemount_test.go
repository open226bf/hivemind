package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/domain/service"
)

func TestFileMountValidate_TrimsAndAccepts(t *testing.T) {
	m := service.FileMount{TargetPath: "  /etc/nginx/nginx.conf  ", UID: " 101 ", GID: "101", Mode: 0o440}

	require.NoError(t, m.Validate())
	assert.Equal(t, "/etc/nginx/nginx.conf", m.TargetPath)
	assert.Equal(t, "101", m.UID)
	assert.Equal(t, uint32(0o440), m.Mode)
}

func TestFileMountValidate_EmptyIsValid(t *testing.T) {
	// Nothing chosen: the catalog default path, the orchestrator's own owner and
	// the default mode all apply.
	m := service.FileMount{}

	require.NoError(t, m.Validate())
	assert.Equal(t, service.DefaultFileMode, m.EffectiveMode())
}

func TestFileMountValidate_RejectsRelativePath(t *testing.T) {
	m := service.FileMount{TargetPath: "etc/nginx.conf"}

	assert.ErrorIs(t, m.Validate(), service.ErrRelativeMount)
}

func TestFileMountValidate_RejectsNamedOwner(t *testing.T) {
	// A user name would be stored and then silently ignored by the runtime.
	byUID := service.FileMount{UID: "nginx"}
	byGID := service.FileMount{GID: "nginx"}

	assert.ErrorIs(t, byUID.Validate(), service.ErrInvalidFileOwner)
	assert.ErrorIs(t, byGID.Validate(), service.ErrInvalidFileOwner)
}

func TestFileMountValidate_AcceptsRootOwnerExplicitly(t *testing.T) {
	m := service.FileMount{UID: "0", GID: "0"}

	require.NoError(t, m.Validate())
	assert.Equal(t, "0", m.UID, `"0" must survive as a deliberate choice, unlike ""`)
}

func TestFileMountValidate_RejectsModeAbovePermissionBits(t *testing.T) {
	m := service.FileMount{Mode: 0o4755} // setuid

	assert.ErrorIs(t, m.Validate(), service.ErrInvalidFileMode)
}

func TestFileMountEffectiveMode_KeepsAnExplicitMode(t *testing.T) {
	assert.Equal(t, uint32(0o400), service.FileMount{Mode: 0o400}.EffectiveMode())
}
