package dto

import "time"

// ─── Requests ─────────────────────────────────────────────────────────────────

// CreateVolumeRequest is the body for POST /volumes.
type CreateVolumeRequest struct {
	Name   string `json:"name"   binding:"required"`
	Driver string `json:"driver" example:"local"`
}

// MountDTO declares one filesystem mount of a service.
type MountDTO struct {
	Type     string `json:"type"      example:"volume"` // volume | bind | tmpfs
	Source   string `json:"source"    example:"app-data"`
	Target   string `json:"target"    example:"/var/lib/app"`
	ReadOnly bool   `json:"read_only" example:"false"`
}

// SetMountsRequest is the body for PUT /services/{id}/mounts (full replacement).
type SetMountsRequest struct {
	Mounts []MountDTO `json:"mounts"`
}

// ─── Responses ────────────────────────────────────────────────────────────────

type VolumeResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Driver    string    `json:"driver"`
	CreatedAt time.Time `json:"created_at"`
}

type VolumeListResponse struct {
	Items []VolumeResponse `json:"items"`
	Total int64            `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// SwarmVolumeInfo is a named volume discovered on a cluster node.
type SwarmVolumeInfo struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
	Scope      string `json:"scope"`
}

// MountsResponse returns a service's mounts plus non-blocking warnings.
type MountsResponse struct {
	Mounts   []MountDTO `json:"mounts"`
	Warnings []string   `json:"warnings"`
}
