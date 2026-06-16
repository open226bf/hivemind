package dto

import "time"

// ─── Requests ─────────────────────────────────────────────────────────────────

// CreateSnapshotRequest is the optional body of POST /services/{id}/snapshots.
type CreateSnapshotRequest struct {
	Label string `json:"label"`
}

// ─── Responses ────────────────────────────────────────────────────────────────

// SnapshotResponse is the metadata view of a snapshot (no sensitive values).
type SnapshotResponse struct {
	ID            string          `json:"id"`
	ServiceID     string          `json:"service_id"`
	Label         string          `json:"label,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
	SchemaVersion int             `json:"schema_version"`
	CreatedAt     time.Time       `json:"created_at"`
	Summary       SnapshotSummary `json:"summary"`
	Detail        *SnapshotDetail `json:"detail,omitempty"`
}

// SnapshotSummary is a glanceable digest shown in the list.
type SnapshotSummary struct {
	FullImage    string `json:"full_image"`
	Replicas     uint64 `json:"replicas"`
	EnvCount     int    `json:"env_count"`
	NetworkCount int    `json:"network_count"`
	SecretCount  int    `json:"secret_count"`
	ConfigCount  int    `json:"config_count"`
	MountCount   int    `json:"mount_count"`
}

// SnapshotDetail is the full captured definition with all sensitive values
// masked. Returned only by GET /snapshots/{id}.
type SnapshotDetail struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Image       string              `json:"image"`
	Tag         string              `json:"tag"`
	Replicas    uint64              `json:"replicas"`
	Command     []string            `json:"command"`
	Entrypoint  []string            `json:"entrypoint"`
	HiveID      string              `json:"hive_id,omitempty"`
	EnvVars     []SnapshotEnvVar    `json:"env_vars"`
	Networks    []SnapshotNetwork   `json:"networks"`
	Secrets     []SnapshotSecretRef `json:"secrets"`
	Configs     []SnapshotConfigRef `json:"configs"`
	Mounts      []SnapshotMount     `json:"mounts"`
}

type SnapshotEnvVar struct {
	Key      string `json:"key"`
	Value    string `json:"value"` // masked when is_secret
	IsSecret bool   `json:"is_secret"`
}

type SnapshotNetwork struct {
	Name   string `json:"name"`
	Subnet string `json:"subnet,omitempty"`
}

type SnapshotSecretRef struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	TargetPath string `json:"target_path,omitempty"`
	// Value is never returned; only its presence/checksum metadata.
	Checksum string `json:"checksum"`
}

type SnapshotConfigRef struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	TargetPath string `json:"target_path,omitempty"`
	Checksum   string `json:"checksum"`
}

type SnapshotMount struct {
	Type     string `json:"type"`
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// SnapshotListResponse wraps a paginated list of snapshots.
type SnapshotListResponse struct {
	Items []SnapshotResponse `json:"items"`
	Total int64              `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}

// RollbackResponse is returned by POST /snapshots/{id}/rollback: the deployment
// that was triggered plus any non-fatal warnings surfaced to the operator.
type RollbackResponse struct {
	Deployment DeploymentResponse `json:"deployment"`
	Warnings   []string           `json:"warnings"`
}
