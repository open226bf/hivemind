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

// ServiceStatusResponse is the aggregated live health of a service (F-MVP-10):
// desired vs. effective replicas and the high-level phase. Served by
// GET /services/{id}/status.
type ServiceStatusResponse struct {
	Running  int  `json:"running"`
	Desired  int  `json:"desired"`
	Pending  int  `json:"pending"`
	Failed   int  `json:"failed"`
	Updating bool `json:"updating"`
}

// TaskStateResponse is a single task (container) of a service, with the node it
// runs on, its current/desired state, last update and any Swarm error message.
type TaskStateResponse struct {
	ID           string              `json:"id"`
	ContainerID  string              `json:"container_id,omitempty"`
	Node         string              `json:"node"`
	Image        string              `json:"image,omitempty"`
	Slot         int                 `json:"slot"`
	CurrentState string              `json:"current_state"`
	DesiredState string              `json:"desired_state"`
	Message      string              `json:"message,omitempty"`
	ErrorMessage string              `json:"error_message,omitempty"`
	ExitCode     *int                `json:"exit_code,omitempty"`
	PID          int                 `json:"pid,omitempty"`
	Networks     []TaskNetworkDetail `json:"networks,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

type TaskNetworkDetail struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// ServiceTasksResponse is the per-task detail of a service (F-MVP-10). Served by
// GET /services/{id}/tasks.
type ServiceTasksResponse struct {
	Tasks []TaskStateResponse `json:"tasks"`
}
