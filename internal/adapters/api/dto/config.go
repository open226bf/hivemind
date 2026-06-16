package dto

import "time"

// CreateConfigRequest is the body for POST /configs. Content is the raw config
// file text (UTF-8, max 500 KB).
type CreateConfigRequest struct {
	Name       string `json:"name"        binding:"required" example:"nginx.conf"`
	TargetPath string `json:"target_path" example:"/etc/nginx/nginx.conf"`
	Content    string `json:"content"     binding:"required"`
	Comment    string `json:"comment"     example:"initial version"`
}

// AddConfigVersionRequest is the body for POST /configs/{id}/versions.
type AddConfigVersionRequest struct {
	Content string `json:"content" binding:"required"`
	Comment string `json:"comment" binding:"required" example:"tune worker_processes"`
}

// RestoreConfigRequest is the body for restoring a config version. The comment
// is optional; when blank a default referencing the restored version is used.
type RestoreConfigRequest struct {
	Comment string `json:"comment" example:"rollback to known-good config"`
}

// AttachConfigRequest is the body for POST /services/{id}/configs.
type AttachConfigRequest struct {
	ConfigID   string `json:"config_id"   binding:"required" example:"3f2504e0-4f89-11d3-9a0c-0305e82c3301"`
	TargetPath string `json:"target_path" example:"/etc/nginx/nginx.conf"`
}

// ConfigResponse exposes config metadata (no content).
type ConfigResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	TargetPath     string    `json:"target_path"`
	CurrentVersion int       `json:"current_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ConfigListResponse wraps a paginated list of configs.
type ConfigListResponse struct {
	Items []ConfigResponse `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// ConfigVersionResponse is a single content version, newest first in listings.
type ConfigVersionResponse struct {
	Version   int       `json:"version"`
	Content   string    `json:"content"`
	Comment   string    `json:"comment"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ServiceConfigResponse is a config attached to a service, with the mount path
// chosen for that service.
type ServiceConfigResponse struct {
	ConfigID   string `json:"config_id"`
	Name       string `json:"name"`
	TargetPath string `json:"target_path"`
}

// DiffLineDTO is one line of a config version diff (F-V2-08).
type DiffLineDTO struct {
	Op      string `json:"op"` // equal | add | del
	Text    string `json:"text"`
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
}

// ConfigDiffResponse is a line-by-line diff between two config versions.
type ConfigDiffResponse struct {
	FromVersion int           `json:"from_version"`
	ToVersion   int           `json:"to_version"`
	Lines       []DiffLineDTO `json:"lines"`
}

// ImpactedServiceResponse is a service affected by a config change.
type ImpactedServiceResponse struct {
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}
