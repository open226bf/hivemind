package dto

import "time"

// ─── Requests ─────────────────────────────────────────────────────────────────

// CreateHiveRequest is the body for POST /hives.
type CreateHiveRequest struct {
	Name        string `json:"name" binding:"required" example:"Plateforme paiement"`
	Description string `json:"description"`
	Color       string `json:"color" example:"#1e88e5"`
}

// UpdateHiveRequest is the body for PUT /hives/{id}.
type UpdateHiveRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Color       string `json:"color" example:"#1e88e5"`
}

// AssignHiveRequest is the body for PUT /services/{id}/hive. A null hive_id
// removes the service from its hive (unassign).
type AssignHiveRequest struct {
	HiveID *string `json:"hive_id" example:"3f2504e0-4f89-11d3-9a0c-0305e82c3301"`
}

// ─── Responses ────────────────────────────────────────────────────────────────

type HiveResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Color        string    `json:"color"`
	ServiceCount int64     `json:"service_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type HiveListResponse struct {
	Items []HiveResponse `json:"items"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
}
