package dto

import "time"

// CreateNetworkRequest is the body for POST /networks.
type CreateNetworkRequest struct {
	Name       string `json:"name"       binding:"required" example:"backend-net"`
	Attachable bool   `json:"attachable" example:"true"`
	External   bool   `json:"external"   example:"false"`
}

// AttachNetworkRequest is the body for POST /services/{id}/networks.
type AttachNetworkRequest struct {
	NetworkID string `json:"network_id" binding:"required" example:"3f2504e0-4f89-11d3-9a0c-0305e82c3301"`
}

// NetworkResponse is the canonical network representation.
type NetworkResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Driver     string    `json:"driver"`
	Scope      string    `json:"scope"`
	Attachable bool      `json:"attachable"`
	External   bool      `json:"external"`
	SwarmID    string    `json:"swarm_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// NetworkListResponse wraps a paginated list of networks.
type NetworkListResponse struct {
	Items []NetworkResponse `json:"items"`
	Total int64             `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}
