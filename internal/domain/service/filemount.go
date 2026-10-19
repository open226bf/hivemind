package service

import (
	"errors"
	"regexp"
	"strings"
)

// DefaultFileMode is applied to a mounted secret or config when no mode was
// chosen: read-only for everyone, which is what Docker itself defaults to.
const DefaultFileMode uint32 = 0o444

var (
	ErrInvalidFileOwner = errors.New("file owner must be a numeric uid/gid")
	ErrInvalidFileMode  = errors.New("file mode must hold permission bits only (0000–0777)")
	ErrRelativeMount    = errors.New("mount path must be absolute")
)

// Swarm passes the owner straight to the container runtime, which only knows
// numeric ids — a user name would be stored and then silently ignored.
var numericOwner = regexp.MustCompile(`^[0-9]{1,10}$`)

// FileMount is how a secret or config is exposed inside a service's containers:
// where the file lands, who owns it, and what its permission bits are.
//
// UID and GID are strings rather than numbers because empty has to stay
// distinguishable from "0": empty means "leave it to the orchestrator" (which
// uses root), while "0" means the caller asked for root deliberately. Swarm's
// API takes them as strings for the same reason.
type FileMount struct {
	// TargetPath is where the file appears in the container. Empty falls back to
	// the catalog entry's own default path.
	TargetPath string
	UID        string
	GID        string
	// Mode carries permission bits only. Zero means "not chosen", so an
	// attachment stored before mounts became configurable still deploys as
	// DefaultFileMode instead of an unreadable 0000.
	Mode uint32
}

// Validate trims the fields and checks them, normalising in place.
func (m *FileMount) Validate() error {
	m.TargetPath = strings.TrimSpace(m.TargetPath)
	m.UID = strings.TrimSpace(m.UID)
	m.GID = strings.TrimSpace(m.GID)

	if m.TargetPath != "" && !strings.HasPrefix(m.TargetPath, "/") {
		return ErrRelativeMount
	}
	if m.UID != "" && !numericOwner.MatchString(m.UID) {
		return ErrInvalidFileOwner
	}
	if m.GID != "" && !numericOwner.MatchString(m.GID) {
		return ErrInvalidFileOwner
	}
	// 0o777 is the whole permission range; anything above would be setuid bits
	// or file-type bits, which Swarm does not accept here.
	if m.Mode > 0o777 {
		return ErrInvalidFileMode
	}
	return nil
}

// EffectiveMode is the permission bits to hand the orchestrator, substituting
// the default when none was chosen.
func (m FileMount) EffectiveMode() uint32 {
	if m.Mode == 0 {
		return DefaultFileMode
	}
	return m.Mode
}
