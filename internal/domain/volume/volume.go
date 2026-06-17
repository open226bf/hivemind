package volume

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidName          = errors.New("volume name must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$")
	ErrVolumeInUse          = errors.New("volume is mounted by one or more services")
	ErrInvalidMountType     = errors.New("mount type must be one of: volume, bind, tmpfs")
	ErrInvalidMountTarget   = errors.New("mount target must be an absolute container path")
	ErrMountSourceRequired  = errors.New("mount source is required for volume and bind mounts")
	ErrTmpfsNoSource        = errors.New("tmpfs mounts must not declare a source")
	ErrInvalidBindSource    = errors.New("bind mount source must be an absolute host path")
	ErrDuplicateMountTarget = errors.New("two mounts share the same target path")
	ErrUnknownVolume        = errors.New("named volume does not exist in the catalog")
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// Volume is a named volume in the catalog. Local-driver volumes are created
// per-node by Docker when a task first mounts them; the catalog records the
// declaration so volumes can be listed, reused and protected from deletion
// while in use.
type Volume struct {
	ID        uuid.UUID
	ClusterID uuid.UUID // orchestration target; zero value = the default cluster
	Name      string
	Driver    string // "local" by default
	CreatedAt time.Time
}

func New(name, driver string) (*Volume, error) {
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
	}
	if strings.TrimSpace(driver) == "" {
		driver = "local"
	}
	return &Volume{
		ID:        uuid.New(),
		Name:      name,
		Driver:    driver,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// MountType enumerates the supported mount kinds.
type MountType string

const (
	MountVolume MountType = "volume" // named volume (Source = volume name)
	MountBind   MountType = "bind"   // host path bind (Source = absolute host path)
	MountTmpfs  MountType = "tmpfs"  // ephemeral in-memory FS (no Source)
)

func (t MountType) IsValid() bool {
	switch t {
	case MountVolume, MountBind, MountTmpfs:
		return true
	default:
		return false
	}
}

// Mount declares one filesystem mount of a service's tasks.
type Mount struct {
	Type     MountType
	Source   string // volume name (volume) or host path (bind); empty for tmpfs
	Target   string // absolute path inside the container
	ReadOnly bool
}

func (m Mount) Validate() error {
	if !m.Type.IsValid() {
		return ErrInvalidMountType
	}
	if !strings.HasPrefix(m.Target, "/") || strings.TrimSpace(m.Target) == "" {
		return ErrInvalidMountTarget
	}
	switch m.Type {
	case MountVolume:
		if strings.TrimSpace(m.Source) == "" {
			return ErrMountSourceRequired
		}
		if !nameRegex.MatchString(m.Source) {
			return ErrInvalidName
		}
	case MountBind:
		if strings.TrimSpace(m.Source) == "" {
			return ErrMountSourceRequired
		}
		if !strings.HasPrefix(m.Source, "/") {
			return ErrInvalidBindSource
		}
	case MountTmpfs:
		if strings.TrimSpace(m.Source) != "" {
			return ErrTmpfsNoSource
		}
	}
	return nil
}

// ValidateMounts checks each mount and rejects duplicate target paths (a
// container cannot mount two sources at the same place).
func ValidateMounts(mounts []Mount) error {
	seen := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		if err := m.Validate(); err != nil {
			return err
		}
		if seen[m.Target] {
			return ErrDuplicateMountTarget
		}
		seen[m.Target] = true
	}
	return nil
}

// HasBind reports whether any mount is a host bind mount (Admin-only, F-V2-06).
func HasBind(mounts []Mount) bool {
	for _, m := range mounts {
		if m.Type == MountBind {
			return true
		}
	}
	return false
}
