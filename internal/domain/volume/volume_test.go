package volume_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/domain/volume"
)

func TestNew_DefaultsDriverToLocal(t *testing.T) {
	v, err := volume.New("app-data", "")
	require.NoError(t, err)
	assert.Equal(t, "local", v.Driver)
	assert.Equal(t, "app-data", v.Name)
}

func TestNew_InvalidName(t *testing.T) {
	for _, name := range []string{"", "_bad", "with space", "-leading"} {
		_, err := volume.New(name, "local")
		assert.ErrorIs(t, err, volume.ErrInvalidName, "name=%q", name)
	}
}

func TestMountValidate_Volume(t *testing.T) {
	m := volume.Mount{Type: volume.MountVolume, Source: "app-data", Target: "/data"}
	require.NoError(t, m.Validate())

	bad := volume.Mount{Type: volume.MountVolume, Target: "/data"} // no source
	assert.ErrorIs(t, bad.Validate(), volume.ErrMountSourceRequired)
}

func TestMountValidate_Bind(t *testing.T) {
	m := volume.Mount{Type: volume.MountBind, Source: "/host/path", Target: "/in/container"}
	require.NoError(t, m.Validate())

	rel := volume.Mount{Type: volume.MountBind, Source: "relative/path", Target: "/in/container"}
	assert.ErrorIs(t, rel.Validate(), volume.ErrInvalidBindSource)
}

func TestMountValidate_Tmpfs(t *testing.T) {
	m := volume.Mount{Type: volume.MountTmpfs, Target: "/tmp/cache"}
	require.NoError(t, m.Validate())

	withSrc := volume.Mount{Type: volume.MountTmpfs, Source: "nope", Target: "/tmp/cache"}
	assert.ErrorIs(t, withSrc.Validate(), volume.ErrTmpfsNoSource)
}

func TestMountValidate_TargetMustBeAbsolute(t *testing.T) {
	m := volume.Mount{Type: volume.MountVolume, Source: "v", Target: "relative"}
	assert.ErrorIs(t, m.Validate(), volume.ErrInvalidMountTarget)
}

func TestMountValidate_BadType(t *testing.T) {
	m := volume.Mount{Type: "nfs", Source: "x", Target: "/x"}
	assert.ErrorIs(t, m.Validate(), volume.ErrInvalidMountType)
}

func TestValidateMounts_DuplicateTarget(t *testing.T) {
	mounts := []volume.Mount{
		{Type: volume.MountVolume, Source: "a", Target: "/data"},
		{Type: volume.MountVolume, Source: "b", Target: "/data"},
	}
	assert.ErrorIs(t, volume.ValidateMounts(mounts), volume.ErrDuplicateMountTarget)
}

func TestHasBind(t *testing.T) {
	assert.True(t, volume.HasBind([]volume.Mount{{Type: volume.MountBind, Source: "/h", Target: "/c"}}))
	assert.False(t, volume.HasBind([]volume.Mount{{Type: volume.MountVolume, Source: "v", Target: "/c"}}))
}
