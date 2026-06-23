package dto

import "time"

// CreateGrantRequest grants a verb to a user on the resource named by the route.
type CreateGrantRequest struct {
	SubjectID string     `json:"subject_id" binding:"required,uuid"`
	Verb      string     `json:"verb" binding:"required,oneof=read write manage"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// GrantResponse is a single access grant.
type GrantResponse struct {
	ID           string     `json:"id"`
	SubjectID    string     `json:"subject_id"`
	ResourceType string     `json:"resource_type"`
	ResourceID   string     `json:"resource_id"`
	Verb         string     `json:"verb"`
	CreatedBy    string     `json:"created_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

// GrantListResponse lists the grants on a resource.
type GrantListResponse struct {
	Items []GrantResponse `json:"items"`
}
