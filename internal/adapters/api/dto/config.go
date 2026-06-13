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
	Comment string `json:"comment" example:"tune worker_processes"`
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
