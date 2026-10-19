package dto

import "time"

// CreateSecretRequest is the body for POST /secrets. The value is write-only:
// it is encrypted at rest and never returned by any endpoint.
type CreateSecretRequest struct {
	Name       string `json:"name"        binding:"required" example:"db_password"`
	TargetPath string `json:"target_path" example:"/run/secrets/db_password"`
	Value      string `json:"value"       binding:"required"`
}

// RotateSecretRequest is the body for POST /secrets/{id}/rotate.
type RotateSecretRequest struct {
	Value string `json:"value" binding:"required"`
}

// AttachSecretRequest is the body for POST /services/{id}/secrets.
type AttachSecretRequest struct {
	SecretID   string `json:"secret_id"   binding:"required" example:"3f2504e0-4f89-11d3-9a0c-0305e82c3301"`
	TargetPath string `json:"target_path" example:"/run/secrets/db_password"`
	// UID and GID own the mounted file. Leave them empty to let the orchestrator
	// decide (root); "0" is root chosen deliberately. Named users are rejected —
	// the container runtime only understands numeric ids.
	UID string `json:"uid,omitempty" example:"101"`
	GID string `json:"gid,omitempty" example:"101"`
	// Mode holds permission bits only (0000–0777, decimal in JSON). Omit it or
	// send 0 to keep the default 0444 (288 in decimal).
	Mode uint32 `json:"mode,omitempty" example:"288"`
}

// UpdateSecretAttachmentRequest is the body for
// PUT /services/{id}/secrets/{secretId}: it replaces the file mount of an
// already-attached secret. Every field is optional and absent means "default",
// so the body always describes the mount in full rather than patching it.
type UpdateSecretAttachmentRequest struct {
	TargetPath string `json:"target_path" example:"/run/secrets/db_password"`
	// UID and GID own the mounted file. Leave them empty to let the orchestrator
	// decide (root); "0" is root chosen deliberately. Named users are rejected —
	// the container runtime only understands numeric ids.
	UID string `json:"uid,omitempty" example:"101"`
	GID string `json:"gid,omitempty" example:"101"`
	// Mode holds permission bits only (0000–0777, decimal in JSON). Omit it or
	// send 0 to keep the default 0444 (288 in decimal).
	Mode uint32 `json:"mode,omitempty" example:"288"`
}

// SecretResponse exposes secret metadata only — the value is never included.
type SecretResponse struct {
	ID             string    `json:"id"`
	ClusterID      string    `json:"cluster_id,omitempty"`
	Name           string    `json:"name"`
	TargetPath     string    `json:"target_path"`
	CurrentVersion int       `json:"current_version"`
	Checksum       string    `json:"checksum"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SecretListResponse wraps a paginated list of secrets.
type SecretListResponse struct {
	Items []SecretResponse `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// ServiceSecretResponse is a secret attached to a service, with the file mount
// chosen for that service. Mode is the stored value: 0 means "not chosen", and
// the deployment then applies the default 0444.
type ServiceSecretResponse struct {
	SecretID   string `json:"secret_id"`
	Name       string `json:"name"`
	TargetPath string `json:"target_path"`
	UID        string `json:"uid,omitempty"`
	GID        string `json:"gid,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
}
