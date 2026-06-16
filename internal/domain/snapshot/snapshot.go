// Package snapshot models a complete, point-in-time capture of a service and
// every element it uses (spec, environment, networks, secrets, configs,
// mounts), resolved to the values that were live at capture time. A snapshot
// is a self-contained restore point: it embeds secret/config values (encrypted
// at rest by the persistence layer) so it survives later deletion or rotation
// of the underlying resources, and can be reused for a manual rollback.
package snapshot

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// SchemaVersion is the version of the Payload layout. Bump it whenever the
// payload shape changes so older snapshots can be migrated or read defensively.
const SchemaVersion = 1

var (
	ErrEmptyPayload  = errors.New("snapshot payload is empty")
	ErrSchemaUnknown = errors.New("snapshot schema version is not supported")
)

// ServiceSnapshot is the persisted restore point. The Payload carries the full
// captured definition; the surrounding fields are queryable metadata.
type ServiceSnapshot struct {
	ID            uuid.UUID
	ServiceID     uuid.UUID
	Label         string     // optional human label ("avant migration v2")
	CreatedBy     *uuid.UUID // nil for system-triggered captures
	SchemaVersion int
	Payload       Payload
	CreatedAt     time.Time
}

// Payload is the self-contained content of a snapshot. It deliberately uses its
// own value types (not the live domain types) so the on-disk format is decoupled
// from the evolution of the service/secret/config domains.
type Payload struct {
	// Core service definition.
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Image        string       `json:"image"`
	Tag          string       `json:"tag"`
	Replicas     uint64       `json:"replicas"`
	Command      []string     `json:"command,omitempty"`
	Entrypoint   []string     `json:"entrypoint,omitempty"`
	Resources    Resources    `json:"resources"`
	Placement    Placement    `json:"placement"`
	UpdateConfig UpdateConfig `json:"update_config"`
	HiveID       string       `json:"hive_id,omitempty"`

	// Attached elements, resolved at capture time.
	EnvVars  []EnvVar  `json:"env_vars,omitempty"`
	Networks []Network `json:"networks,omitempty"`
	Secrets  []Secret  `json:"secrets,omitempty"`
	Configs  []Config  `json:"configs,omitempty"`
	Mounts   []Mount   `json:"mounts,omitempty"`
}

type Resources struct {
	CPUReservation float64 `json:"cpu_reservation"`
	CPULimit       float64 `json:"cpu_limit"`
	MemReservation int64   `json:"mem_reservation"`
	MemLimit       int64   `json:"mem_limit"`
}

type Placement struct {
	Constraints []string `json:"constraints,omitempty"`
	Preferences []string `json:"preferences,omitempty"`
	MaxReplicas uint64   `json:"max_replicas"`
}

type UpdateConfig struct {
	Parallelism     uint64  `json:"parallelism"`
	DelayNs         int64   `json:"delay_ns"`
	FailureAction   string  `json:"failure_action"`
	MonitorNs       int64   `json:"monitor_ns"`
	MaxFailureRatio float64 `json:"max_failure_ratio"`
	Order           string  `json:"order"`
}

// EnvVar embeds the value. For secret env vars the Value is the sensitive
// payload that the persistence layer encrypts at rest and the API masks.
type EnvVar struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// Network captures the defining attributes of an attached overlay network so a
// rollback can re-resolve (or recreate) it even if it was deleted meanwhile.
type Network struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Subnet     string `json:"subnet,omitempty"`
	Attachable bool   `json:"attachable"`
}

// Secret embeds the resolved value (encrypted at rest) plus the version and
// checksum that were live at capture time. Checksum lets a rollback detect that
// the live secret has since been rotated.
type Secret struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    int    `json:"version"`
	Checksum   string `json:"checksum"`
	TargetPath string `json:"target_path,omitempty"`
	Value      string `json:"value"` // sensitive; encrypted at rest, masked by the API
}

// Config embeds the resolved content (treated as sensitive at rest) plus the
// version and checksum live at capture time.
type Config struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    int    `json:"version"`
	Checksum   string `json:"checksum"`
	TargetPath string `json:"target_path,omitempty"`
	Content    string `json:"content"`
}

type Mount struct {
	Type     string `json:"type"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// New builds a snapshot from an already-assembled payload.
func New(serviceID uuid.UUID, label string, createdBy *uuid.UUID, payload Payload) (*ServiceSnapshot, error) {
	if payload.Name == "" || payload.Image == "" {
		return nil, ErrEmptyPayload
	}
	return &ServiceSnapshot{
		ID:            uuid.New(),
		ServiceID:     serviceID,
		Label:         label,
		CreatedBy:     createdBy,
		SchemaVersion: SchemaVersion,
		Payload:       payload,
		CreatedAt:     time.Now().UTC(),
	}, nil
}
