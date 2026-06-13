package dto

import "time"

// DeploymentResponse is the canonical deployment representation.
type DeploymentResponse struct {
	ID           string     `json:"id"`
	ServiceID    string     `json:"service_id"`
	UserID       string     `json:"user_id,omitempty"`
	ImageTag     string     `json:"image_tag"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	ErrorMessage string     `json:"error_message,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	DurationMs   *int64     `json:"duration_ms,omitempty"`
}

// DeploymentListResponse wraps a paginated list of deployments.
type DeploymentListResponse struct {
	Items []DeploymentResponse `json:"items"`
	Total int64                `json:"total"`
	Page  int                  `json:"page"`
	Size  int                  `json:"size"`
}
