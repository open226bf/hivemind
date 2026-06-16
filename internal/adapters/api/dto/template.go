package dto

import "time"

// ─── Requests ─────────────────────────────────────────────────────────────────

// TemplateSpecDTO is the default service definition a template applies.
type TemplateSpecDTO struct {
	Image        string          `json:"image" binding:"required" example:"nginx"`
	Tag          string          `json:"tag" example:"1.25"`
	Replicas     uint64          `json:"replicas" example:"2"`
	Resources    ResourcesDTO    `json:"resources"`
	UpdateConfig UpdateConfigDTO `json:"update_config"`
	Placement    PlacementDTO    `json:"placement"`
	NetworkIDs   []string        `json:"network_ids"`
}

// CreateTemplateRequest is the body for POST /templates.
type CreateTemplateRequest struct {
	Name         string          `json:"name" binding:"required" example:"java-api"`
	Description  string          `json:"description"`
	Spec         TemplateSpecDTO `json:"spec"`
	LockedFields []string        `json:"locked_fields" example:"resources"`
}

// UpdateTemplateRequest is the body for PUT /templates/{id}. The name is immutable.
type UpdateTemplateRequest struct {
	Description  string          `json:"description"`
	Spec         TemplateSpecDTO `json:"spec"`
	LockedFields []string        `json:"locked_fields"`
}

// InstantiateTemplateRequest is the body for POST /services/from-template/{id}.
// Overrides are optional; supplying one for a locked field is rejected.
type InstantiateTemplateRequest struct {
	Name        string        `json:"name" binding:"required" example:"orders-api"`
	Description string        `json:"description"`
	Tag         *string       `json:"tag"`
	Replicas    *uint64       `json:"replicas"`
	Resources   *ResourcesDTO `json:"resources"`
}

// ─── Responses ────────────────────────────────────────────────────────────────

type TemplateResponse struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Version      int             `json:"version"`
	Spec         TemplateSpecDTO `json:"spec"`
	LockedFields []string        `json:"locked_fields"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type TemplateListResponse struct {
	Items []TemplateResponse `json:"items"`
	Total int64              `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}
