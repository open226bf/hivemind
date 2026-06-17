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

// ServiceSecretResponse is a secret attached to a service, with the mount path
// chosen for that service.
type ServiceSecretResponse struct {
	SecretID   string `json:"secret_id"`
	Name       string `json:"name"`
	TargetPath string `json:"target_path"`
}
